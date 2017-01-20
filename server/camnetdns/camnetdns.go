/*
Copyright 2016 The Camlistore Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// The camnetdns server serves camlistore.net's DNS server and its
// DNS challenges
package main

import (
	"crypto/rand"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"camlistore.org/pkg/gpgchallenge"
	"camlistore.org/pkg/lru"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/sorted"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/datastore"
	"cloud.google.com/go/logging"
	"github.com/miekg/dns"
	"go4.org/cloud/cloudlaunch"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/net/context"
	"golang.org/x/net/http2"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
)

var (
	addr         = flag.String("addr", defaultListenAddr(), "specify address for server to listen on")
	flagServerIP = flag.String("server_ip", "104.154.231.160", "The IP address of the authoritative name server for camlistore.net, i.e. the address where this program will run.")
)

var launchConfig = &cloudlaunch.Config{
	Name:         "camnetdns",
	BinaryBucket: "camlistore-dnsserver-resource",
	GCEProjectID: GCEProjectID,
	Scopes: []string{
		compute.ComputeScope,
		logging.Scope,
		datastore.ScopeDatastore,
	},
}

const (
	GCEProjectID = "camlistore-website"
	// DefaultResponseTTL is the record TTL in seconds
	DefaultResponseTTL = 300
	// even if record already existed in store, we overwrite it if it is older than 30 days, for analytics.
	staleRecord = 30 * 24 * time.Hour
	// max number of records in the lru cache
	cacheSize = 1e6
	// stagingCamwebHost is the FQDN of the staging version of the
	// Camlistore website. We handle it differently from the rest as:
	// 1) we discover its IP using the GCE API, 2) we only trust the stored
	// version of it for 5 minutes.
	stagingCamwebHost = "staging.camlistore.net."
	lowTTL            = 300 // in seconds
)

var (
	errRecordNotFound = errors.New("record not found")
	// lastCamwebUpdate is the last time we updated the stored value for stagingCamwebHost
	lastCamwebUpdate time.Time
)

func defaultListenAddr() string {
	if metadata.OnGCE() {
		return ":53"
	}
	return ":5300"
}

type keyValue interface {
	// Get fetches the value for key. It returns errRecordNotFound when
	// there is no such record.
	Get(key string) (string, error)
	Set(key, value string) error
}

// cachedStore is a keyValue implementation that stores in Google's datastore
// with dsClient. It automatically stores to cache as well on writes, and always
// tries to read from cache first.
type cachedStore struct {
	// datastore client to store the records. It should not be nil.
	dsClient *datastore.Client
	// cache stores the most recent records. It should not be nil.
	cache *lru.Cache
}

// dsValue is the value type written to the datastore
type dsValue struct {
	// Record is the RHS of an A or AAAA DNS record, i.e. an IPV4 or IPV6
	// address.
	Record string
	// Updated is the last time this key value pair was inserted. Values
	// older than 30 days are rewritten on writes.
	Updated time.Time
}

func (cs cachedStore) Get(key string) (string, error) {
	val, ok := cs.cache.Get(key)
	if ok {
		return val.(string), nil
	}
	// Cache Miss. hit the datastore.
	ctx := context.Background()
	dk := datastore.NewKey(ctx, "camnetdns", key, 0, nil)
	var value dsValue
	if err := cs.dsClient.Get(ctx, dk, &value); err != nil {
		if err != datastore.ErrNoSuchEntity {
			return "", fmt.Errorf("error getting value for %q from datastore: %v", key, err)
		}
		return "", errRecordNotFound
	}
	// And cache it.
	cs.cache.Add(key, value.Record)
	return value.Record, nil
}

func (cs cachedStore) put(ctx context.Context, key, value string) error {
	dk := datastore.NewKey(ctx, "camnetdns", key, 0, nil)
	val := &dsValue{
		Record:  value,
		Updated: time.Now(),
	}
	if _, err := cs.dsClient.Put(ctx, dk, val); err != nil {
		return fmt.Errorf("error writing (%q : %q) record to datastore: %v", key, value, err)
	}
	// and cache it.
	cs.cache.Add(key, value)
	return nil
}

// Set writes the key, value pair to cs. But it does not actually write if the
// value already exists, is up to date, and is more recent than 30 days.
func (cs cachedStore) Set(key, value string) error {
	// check if record already exists
	ctx := context.Background()
	dk := datastore.NewKey(ctx, "camnetdns", key, 0, nil)
	var oldValue dsValue
	if err := cs.dsClient.Get(ctx, dk, &oldValue); err != nil {
		if err != datastore.ErrNoSuchEntity {
			return fmt.Errorf("error checking if record exists for %q from datastore: %v", key, err)
		}
		// record does not exist, write it.
		return cs.put(ctx, key, value)
	}
	// record already exists
	if oldValue.Record != value {
		// new record is different, overwrite old one.
		return cs.put(ctx, key, value)
	}
	// record is the same as before
	if oldValue.Updated.Add(staleRecord).After(time.Now()) {
		// record is still fresh, nothing to do.
		return nil
	}
	// record is older than 30 days, so we rewrite it, for analytics.
	return cs.put(ctx, key, value)
}

type memkv struct {
	skv sorted.KeyValue
}

func (kv memkv) Get(key string) (string, error) {
	val, err := kv.skv.Get(key)
	if err != nil {
		if err != sorted.ErrNotFound {
			return "", err
		}
		return "", errRecordNotFound
	}
	return val, nil
}

func (kv memkv) Set(key, value string) error {
	return kv.skv.Set(key, value)
}

// DNSServer implements the dns.Handler interface to serve A and AAAA
// records, using a KeyValue store for the lookups.
type DNSServer struct {
	dataSource keyValue
}

func newDNSServer(src keyValue) *DNSServer {
	return &DNSServer{
		dataSource: src,
	}
}

func (ds *DNSServer) HandleLookup(name string) (string, error) {
	// Lowercase it all, to satisfy https://tools.ietf.org/html/draft-vixie-dnsext-dns0x20-00
	loName := strings.ToLower(name)
	if loName != stagingCamwebHost {
		return ds.dataSource.Get(loName)
	}
	if time.Now().Before(lastCamwebUpdate.Add(lowTTL * time.Second)) {
		return ds.dataSource.Get(loName)
	}
	stagingIP, err := stagingCamwebIP()
	if err != nil {
		log.Printf("Could not get new IP of %v: %v. Serving old value instead.", stagingCamwebHost, err)
		return ds.dataSource.Get(loName)
	}
	if err := ds.dataSource.Set(stagingCamwebHost, stagingIP); err != nil {
		log.Printf("Could not update (%v, %v) entry: %v", stagingCamwebHost, stagingIP, err)
	} else {
		lastCamwebUpdate = time.Now()
		log.Printf("%v -> %v updated successfully", stagingCamwebHost, stagingIP)
	}
	return stagingIP, nil
}

const (
	domain      = "camlistore.net."
	authorityNS = "camnetdns.camlistore.org."
	// Increment after every change with format YYYYMMDDnn.
	soaSerial = 2017012301
)

var (
	authoritySection = &dns.NS{
		Ns: authorityNS,
		Hdr: dns.RR_Header{
			Name:   domain,
			Rrtype: dns.TypeNS,
			Class:  dns.ClassINET,
			Ttl:    DefaultResponseTTL,
		},
	}
	additionalSection = &dns.A{
		A: net.ParseIP(*flagServerIP),
		Hdr: dns.RR_Header{
			Name:   authorityNS,
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    DefaultResponseTTL,
		},
	}
	startOfAuthoritySection = &dns.SOA{
		Hdr: dns.RR_Header{
			Name:   domain,
			Rrtype: dns.TypeSOA,
			Class:  dns.ClassINET,
			Ttl:    DefaultResponseTTL,
		},
		Ns:      authorityNS,
		Mbox:    "admin.camlistore.org.",
		Serial:  soaSerial,
		Refresh: 3600, // TODO(mpl): set them lower once we got everything right.
		Retry:   3600,
		Expire:  86500,
		Minttl:  DefaultResponseTTL,
	}
)

func commonHeader(q dns.Question) dns.RR_Header {
	return dns.RR_Header{
		Name:   q.Name,
		Rrtype: q.Qtype,
		Class:  dns.ClassINET,
		Ttl:    DefaultResponseTTL,
	}
}

func (ds *DNSServer) ServeDNS(rw dns.ResponseWriter, mes *dns.Msg) {
	resp := new(dns.Msg)

	if mes.IsEdns0() != nil {
		// Because apparently, if we're not going to handle EDNS
		// properly, i.e. by returning an OPT section as well, we should
		// return an RcodeFormatError. Not seen in the RFC, but doing that
		// addresses some of the warnings from
		// http://dnsviz.net/d/granivo.re/dnssec/
		log.Print("unhandled EDNS message\n")
		resp.SetRcode(mes, dns.RcodeFormatError)
		if err := rw.WriteMsg(resp); err != nil {
			log.Printf("error responding to DNS query: %s", err)
		}
		return
	}
	resp.SetReply(mes)
	// TODO(mpl): Should we make sure that at least q.Name ends in
	// "camlistore.net" before claiming we're authoritative on that response?
	resp.Authoritative = true

	for _, q := range mes.Question {
		log.Printf("DNS request from %s: %s", rw.RemoteAddr(), &q)

		answer, err := ds.HandleLookup(q.Name)
		if err == errRecordNotFound {
			resp.SetRcode(mes, dns.RcodeNameError)
			if err := rw.WriteMsg(resp); err != nil {
				log.Printf("error responding to DNS query: %s", err)
			}
			return
		}
		if err != nil {
			log.Printf("error looking up %q: %v", q.Name, err)
			continue
		}

		if q.Qclass != dns.ClassINET {
			log.Printf("error: got invalid DNS question class %d\n", q.Qclass)
			continue
		}

		switch q.Qtype {
		// As long as we send a reply (even an empty one), we apparently
		// look compliant. Or at least more than if we replied with
		// RcodeNotImplemented.
		case dns.TypeDNSKEY, dns.TypeTXT, dns.TypeMX:
			break

		case dns.TypeSOA:
			resp.Answer = []dns.RR{startOfAuthoritySection}
			resp.Extra = []dns.RR{additionalSection}

		case dns.TypeNS:
			resp.Answer = []dns.RR{authoritySection}
			resp.Extra = []dns.RR{additionalSection}

		case dns.TypeCAA:
			header := commonHeader(q)
			rr := &dns.CAA{
				Hdr:   header,
				Flag:  1,
				Tag:   "issue",
				Value: "letsencrypt.org",
			}
			resp.Answer = []dns.RR{rr}

		case dns.TypeA, dns.TypeAAAA:
			val := answer
			ip := net.ParseIP(val)
			// TODO(mpl): maybe we should have a distinct memstore for each type?
			isIP6 := strings.Contains(ip.String(), ":")
			header := commonHeader(q)
			if strings.ToLower(q.Name) == stagingCamwebHost {
				header.Ttl = lowTTL
			}
			var rr dns.RR
			if q.Qtype == dns.TypeA {
				if isIP6 {
					break
				}
				rr = &dns.A{
					A:   ip,
					Hdr: header,
				}
			} else if q.Qtype == dns.TypeAAAA {
				if !isIP6 {
					break
				}
				rr = &dns.AAAA{
					AAAA: ip,
					Hdr:  header,
				}
			} else {
				panic("unreachable")
			}
			resp.Answer = []dns.RR{rr}
			// Not necessary, but I think they help.
			resp.Ns = []dns.RR{authoritySection}
			resp.Extra = []dns.RR{additionalSection}

		default:
			log.Printf("unhandled qtype: %d\n", q.Qtype)
			resp.SetRcode(mes, dns.RcodeNotImplemented)
			if err := rw.WriteMsg(resp); err != nil {
				log.Printf("error responding to DNS query: %s", err)
			}
			return
		}
		break
	}

	if err := rw.WriteMsg(resp); err != nil {
		log.Printf("error responding to DNS query: %s", err)
	}
}

func stagingCamwebIP() (string, error) {
	const (
		projectID = "camlistore-website"
		instName  = "camweb-staging"
		zone      = "us-central1-f"
	)
	hc, err := google.DefaultClient(oauth2.NoContext)
	if err != nil {
		return "", fmt.Errorf("error getting an http client: %v", err)
	}
	s, err := compute.New(hc)
	if err != nil {
		return "", fmt.Errorf("error getting compute service: %v", err)
	}
	inst, err := compute.NewInstancesService(s).Get(projectID, zone, instName).Do()
	if err != nil {
		return "", fmt.Errorf("error getting instance: %v", err)
	}
	for _, netInt := range inst.NetworkInterfaces {
		for _, ac := range netInt.AccessConfigs {
			if ac.Type != "ONE_TO_ONE_NAT" {
				continue
			}
			return ac.NatIP, nil
		}
	}
	return "", errors.New("not found")
}

func main() {
	launchConfig.MaybeDeploy()
	flag.Parse()

	var kv keyValue
	var httpsListenAddr string
	if metadata.OnGCE() {
		httpsListenAddr = ":443"
		dsClient, err := datastore.NewClient(context.Background(), GCEProjectID)
		if err != nil {
			log.Fatalf("Error creating datastore client for records: %v", err)
		}
		kv = cachedStore{
			dsClient: dsClient,
			cache:    lru.New(cacheSize),
		}
	} else {
		httpsListenAddr = ":4430"
		kv = memkv{skv: sorted.NewMemoryKeyValue()}
	}
	if err := kv.Set("6401800c.camlistore.net.", "159.203.246.79"); err != nil {
		log.Fatalf("Error adding %v:%v record: %v", "6401800c.camlistore.net.", "159.203.246.79", err)
	}
	if err := kv.Set(domain, *flagServerIP); err != nil {
		log.Fatalf("Error adding %v:%v record: %v", domain, *flagServerIP, err)
	}
	if err := kv.Set("www.camlistore.net.", *flagServerIP); err != nil {
		log.Fatalf("Error adding %v:%v record: %v", "www.camlistore.net.", *flagServerIP, err)
	}
	lastCamwebUpdate = time.Now()
	if stagingIP, err := stagingCamwebIP(); err == nil {
		if err := kv.Set(stagingCamwebHost+".", stagingIP); err != nil {
			log.Fatalf("Error adding %v:%v record: %v", stagingCamwebHost+".", stagingIP, err)
		}
	}

	ds := newDNSServer(kv)
	cs := &gpgchallenge.Server{
		OnSuccess: func(identity string, address string) error {
			log.Printf("Adding %v.camlistore.net. as %v", identity, address)
			return ds.dataSource.Set(strings.ToLower(identity+".camlistore.net."), address)
		},
	}

	tcperr := make(chan error, 1)
	udperr := make(chan error, 1)
	httperr := make(chan error, 1)
	log.Printf("serving DNS on %s\n", *addr)
	go func() {
		tcperr <- dns.ListenAndServe(*addr, "tcp", ds)
	}()
	go func() {
		udperr <- dns.ListenAndServe(*addr, "udp", ds)
	}()
	if metadata.OnGCE() {
		// TODO(mpl): if we want to get a cert for anything
		// *.camlistore.net, it's a bit of a chicken and egg problem, since
		// we need camnetdns itself to be already running and answering DNS
		// queries. It's probably doable, but easier for now to just ask
		// one for camnetdns.camlistore.org, since that name is not
		// resolved by camnetdns.
		hostname := strings.TrimSuffix(authorityNS, ".")
		m := autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(hostname),
			Cache:      autocert.DirCache(osutil.DefaultLetsEncryptCache()),
		}
		ln, err := tls.Listen("tcp", httpsListenAddr, &tls.Config{
			Rand:           rand.Reader,
			Time:           time.Now,
			NextProtos:     []string{http2.NextProtoTLS, "http/1.1"},
			MinVersion:     tls.VersionTLS12,
			GetCertificate: m.GetCertificate,
		})
		if err != nil {
			log.Fatalf("Error listening on %v: %v", httpsListenAddr, err)
		}
		go func() {
			httperr <- http.Serve(ln, cs)
		}()
	}
	select {
	case err := <-tcperr:
		log.Fatalf("DNS over TCP error: %v", err)
	case err := <-udperr:
		log.Fatalf("DNS error: %v", err)
	case err := <-httperr:
		log.Fatalf("HTTP server error: %v", err)
	}
}
