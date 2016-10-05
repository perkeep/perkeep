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
	return ds.dataSource.Get(name)
}

var (
	authoritySection = &dns.NS{
		Ns: "camnetdns.camlistore.org.",
		Hdr: dns.RR_Header{
			Name:   "camlistore.net.",
			Rrtype: dns.TypeNS,
			Class:  dns.ClassINET,
			Ttl:    DefaultResponseTTL,
		},
	}
	additionalSection = &dns.A{
		A: net.ParseIP(*flagServerIP),
		Hdr: dns.RR_Header{
			Name:   "camnetdns.camlistore.org.",
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    DefaultResponseTTL,
		},
	}
)

func (ds *DNSServer) ServeDNS(rw dns.ResponseWriter, mes *dns.Msg) {
	resp := new(dns.Msg)

	for _, q := range mes.Question {
		log.Printf("DNS request from %s: %s", rw.RemoteAddr(), &q)
		switch q.Qtype {
		case dns.TypeA, dns.TypeAAAA:
			val, err := ds.HandleLookup(q.Name)
			if err != nil {
				log.Println(err)
				continue
			}

			if q.Qclass != dns.ClassINET {
				log.Printf("error: got invalid DNS question class %d\n", q.Qclass)
				continue
			}

			header := dns.RR_Header{
				Name:   q.Name,
				Rrtype: q.Qtype,
				Class:  dns.ClassINET,
				Ttl:    DefaultResponseTTL,
			}

			var rr dns.RR
			// not really super sure why these have to be different types
			if q.Qtype == dns.TypeA {
				rr = &dns.A{
					A:   net.ParseIP(val),
					Hdr: header,
				}
			} else if q.Qtype == dns.TypeAAAA {
				rr = &dns.AAAA{
					AAAA: net.ParseIP(val),
					Hdr:  header,
				}
			} else {
				panic("unreachable")
			}

			resp.Answer = []dns.RR{rr}

		default:
			log.Printf("unhandled qtype: %d\n", q.Qtype)
			resp.SetRcode(mes, dns.RcodeNotImplemented)
			rw.WriteMsg(resp)
			return
		}
		break
	}
	resp.SetReply(mes)
	resp.Authoritative = true

	// Not necessary, but I think they can help.
	resp.Ns = []dns.RR{authoritySection}
	resp.Extra = []dns.RR{additionalSection}

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
	if err := memkv.Set("camlistore.net.", *flagServerIP); err != nil {
		panic(err)
	}
	if err := memkv.Set("www.camlistore.net.", *flagServerIP); err != nil {
		panic(err)
	}

	ds := NewDNSServer(memkv)

	log.Printf("serving DNS on %s\n", *addr)
	if err := dns.ListenAndServe(*addr, "udp", ds); err != nil {
		log.Fatal(err)
	}
}
