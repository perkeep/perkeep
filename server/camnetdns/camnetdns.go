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
	"github.com/miekg/dns"
	"go4.org/cloud/cloudlaunch"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/cloud/compute/metadata"
	"google.golang.org/cloud/datastore"
	"google.golang.org/cloud/logging"
)

var launchConfig = &cloudlaunch.Config{
	Name:         "camnetdns",
	BinaryBucket: "camlistore-dnsserver-resource",
	GCEProjectID: "camlistore-website",
	Scopes: []string{
		compute.ComputeScope,
		logging.Scope,
		datastore.ScopeDatastore,
		datastore.ScopeUserEmail, // whose email? https://github.com/GoogleCloudPlatform/gcloud-golang/issues/201
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

			resp.Answer = append(resp.Answer, rr)

		default:
			log.Printf("unhandled qtype: %d\n", q.Qtype)
		}
	}

	resp.Authoritative = true
	resp.Id = mes.Id
	resp.Response = true

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
	if err := memkv.Set("camlistore.net.", "104.154.231.160"); err != nil {
		panic(err)
	}
	if err := memkv.Set("www.camlistore.net.", "104.154.231.160"); err != nil {
		panic(err)
	}

	ds := NewDNSServer(memkv)

	log.Printf("serving DNS on %s\n", *addr)
	if err := dns.ListenAndServe(*addr, "udp", ds); err != nil {
		log.Fatal(err)
	}
}
