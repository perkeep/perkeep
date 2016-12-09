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

// The server command is an example server of the gpgchallenge package.
package main

import (
	"flag"
	"log"
	"net/http"

	"camlistore.org/pkg/gpgchallenge"
)

var (
	flagHost       = flag.String("host", "", "host:port to listen on for https connections")
	flagTLSCert    = flag.String("cert", "", "TLS certificate file")
	flagTLSKey     = flag.String("key", "", "TLS key file")
	flagClientPort = flag.Int("client_port", 443, "the port to challenge the client on")
)

func main() {
	flag.Parse()

	if *flagHost == "" {
		log.Fatal("you need to specify -host")
	}
	if *flagTLSCert == "" {
		log.Fatal("you need to specify -cert")
	}
	if *flagTLSKey == "" {
		log.Fatal("you need to specify -key")
	}

	gpgchallenge.ClientChallengedPort = *flagClientPort
	cs := &gpgchallenge.Server{
		OnSuccess: func(identity, address string) error {
			// This is where and when camnetdns would add the name entry.
			log.Printf("Server says challenge is success for %v at %v", identity, address)
			return nil
		},
	}
	log.Fatal(http.ListenAndServeTLS(*flagHost, *flagTLSCert, *flagTLSKey, cs))
}
