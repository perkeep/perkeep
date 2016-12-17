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

// The client command is an example client of the gpgchallenge package.
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"golang.org/x/net/http2"

	"camlistore.org/pkg/gpgchallenge"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/osutil"
)

var (
	flagPort      = flag.Int("p", 443, "port that the server will challenge us on.")
	flagKeyRing   = flag.String("keyring", osutil.DefaultSecretRingFile(), "path to the GPG keyring file")
	flagKeyID     = flag.String("keyid", "", "GPG key ID")
	flagClaimedIP = flag.String("ip", "", "IP address to prove control over")
	flagServer    = flag.String("server", "camnetdns.camlistore.org", "server we want to run the challenge against")
)

func main() {
	flag.Parse()

	if *flagKeyID == "" {
		log.Fatal("you need to specify -keyid")
	}
	if *flagClaimedIP == "" {
		log.Fatal("you need to specify -ip")
	}

	gpgchallenge.ClientChallengedPort = *flagPort
	cl, err := gpgchallenge.NewClient(
		*flagKeyRing,
		*flagKeyID,
		*flagClaimedIP,
	)
	if err != nil {
		log.Fatal(err)
	}

	config := &tls.Config{
		NextProtos: []string{http2.NextProtoTLS, "http/1.1"},
		MinVersion: tls.VersionTLS12,
	}
	selfCert, selfKey, err := httputil.GenSelfTLS(*flagClaimedIP + "-challenge")
	if err != nil {
		log.Fatalf("could no generate self-signed certificate: %v", err)
	}
	config.Certificates = make([]tls.Certificate, 1)
	config.Certificates[0], err = tls.X509KeyPair(selfCert, selfKey)
	if err != nil {
		log.Fatalf("could not load TLS certificate: %v", err)
	}
	laddr := fmt.Sprintf(":%d", *flagPort)
	l, err := tls.Listen("tcp", laddr, config)
	if err != nil {
		log.Fatalf("could not listen on %v for challenge: %v", laddr, err)
	}

	pattern, handler := cl.Handler()
	http.Handle(pattern, handler)
	errc := make(chan error, 1)
	go func() {
		errc <- http.Serve(l, handler)
	}()
	time.Sleep(time.Second)
	if err := cl.Challenge(*flagServer); err != nil {
		log.Fatal(err)
	}
	log.Printf("Challenge success")
}
