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
	"errors"
	"flag"
	"log"
	"net"
	"strings"

	"camlistore.org/pkg/sorted"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/datastore"
	"cloud.google.com/go/logging"
	"github.com/miekg/dns"
	"go4.org/cloud/cloudlaunch"
	compute "google.golang.org/api/compute/v1"
)

var flagServerIP = flag.String("server_ip", "104.154.231.160", "The IP address of the authoritative name server for camlistore.net, i.e. the address where this program will run.")

// TODO(mpl): pass the server ip to the launchConfig, so we create the instance
// with this specific IP. Which means, we'll have to book it as a static address in
// Google Cloud I suppose?
// Or, we hope we're lucky and we never have to destroy the camnet-dns VM (and lose
// its current IP)?

var launchConfig = &cloudlaunch.Config{
	Name:         "camnetdns",
	BinaryBucket: "camlistore-dnsserver-resource",
	GCEProjectID: "camlistore-website",
	Scopes: []string{
		compute.ComputeScope,
		logging.Scope,
		datastore.ScopeDatastore,
	},
}

// DefaultResponseTTL is the record TTL in seconds
const DefaultResponseTTL = 300

var ErrRecordNotFound = errors.New("record not found")

func defaultListenAddr() string {
	if metadata.OnGCE() {
		return ":53"
	}
	return ":5300"
}

// DNSServer implements the dns.Handler interface to serve A and AAAA
// records using a sorted.KeyValue for the lookups.
type DNSServer struct {
	dataSource sorted.KeyValue
}

func NewDNSServer(src sorted.KeyValue) *DNSServer {
	return &DNSServer{
		dataSource: src,
	}
}

func (ds *DNSServer) HandleLookup(name string) (string, error) {
	// Lowercase it all, to satisfy https://tools.ietf.org/html/draft-vixie-dnsext-dns0x20-00
	return ds.dataSource.Get(strings.ToLower(name))
}

const (
	domain      = "camlistore.net."
	authorityNS = "camnetdns.camlistore.org."
	// Increment after every change with format YYYYMMDDnn.
	soaSerial = 2016102101
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
		if err == sorted.ErrNotFound {
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

func main() {
	launchConfig.MaybeDeploy()
	addr := flag.String("addr", defaultListenAddr(), "specify address for server to listen on")
	flag.Parse()

	memkv := sorted.NewMemoryKeyValue()
	if err := memkv.Set("6401800c.camlistore.net.", "159.203.246.79"); err != nil {
		panic(err)
	}
	if err := memkv.Set(domain, *flagServerIP); err != nil {
		panic(err)
	}
	if err := memkv.Set("www.camlistore.net.", *flagServerIP); err != nil {
		panic(err)
	}
	if err := memkv.Set("wip.camlistore.net.", "104.199.42.193"); err != nil {
		panic(err)
	}

	ds := NewDNSServer(memkv)

	log.Printf("serving DNS on %s\n", *addr)
	tcperr := make(chan error, 1)
	udperr := make(chan error, 1)
	go func() {
		tcperr <- dns.ListenAndServe(*addr, "tcp", ds)
	}()
	go func() {
		udperr <- dns.ListenAndServe(*addr, "udp", ds)
	}()
	select {
	case err := <-tcperr:
		log.Fatalf("DNS over TCP error: %v", err)
	case err := <-udperr:
		log.Fatalf("DNS error: %v", err)
	}
}
