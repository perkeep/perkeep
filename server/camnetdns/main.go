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
)

// DefaultResponseTTL is the record TTL in seconds
const DefaultResponseTTL = 300

var ErrRecordNotFound = errors.New("record not found")

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

	resp.Id = mes.Id
	resp.Response = true

	if err := rw.WriteMsg(resp); err != nil {
		log.Printf("error responding to DNS query: %s", err)
	}
}

func main() {
	addr := flag.String("addr", "0.0.0.0:5300", "specify address for server to listen on")
	flag.Parse()

	memkv := sorted.NewMemoryKeyValue()
	if err := memkv.Set("6401800c.camlistore.net.", "159.203.246.79"); err != nil {
		panic(err)
	}

	ds := NewDNSServer(memkv)

	log.Printf("serving DNS on %s\n", *addr)
	if err := dns.ListenAndServe(*addr, "udp", ds); err != nil {
		log.Fatal(err)
	}
}
