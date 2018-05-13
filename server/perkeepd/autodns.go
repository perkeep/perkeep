/*
Copyright 2018 The Perkeep Authors

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

// Code related to obtaining camlistore.net DNS subdomains.

package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"
	"perkeep.org/internal/httputil"
	"perkeep.org/internal/osutil"
	"perkeep.org/internal/osutil/gce"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/gpgchallenge"
	"perkeep.org/pkg/serverinit"
	"perkeep.org/pkg/webserver"
)

// For getting a name in camlistore.net
const (
	camliNetDNS    = serverinit.CamliNetDNS
	camliNetDomain = serverinit.CamliNetDomain
)

var camliNetHostName string // <keyId>.camlistore.net

// listenForCamliNet prepares the TLS listener for both the GPG challenge, and
// for Let's Encrypt. It then starts listening and returns the baseURL derived from
// the hostname we should obtain from the GPG challenge.
func listenForCamliNet(ws *webserver.Server, config *serverinit.Config) (baseURL string, err error) {
	camliNetIP := config.CamliNetIP()
	if camliNetIP == "" {
		return "", errors.New("no camliNetIP")
	}
	if ip := net.ParseIP(camliNetIP); ip == nil {
		return "", fmt.Errorf("camliNetIP value %q is not a valid IP address", camliNetIP)
	} else if ip.To4() == nil {
		// TODO: support IPv6 when GCE supports IPv6: https://code.google.com/p/google-compute-engine/issues/detail?id=8
		return "", errors.New("CamliNetIP should be an IPv4, as IPv6 is not yet supported on GCE")
	}
	challengeHostname := camliNetIP + gpgchallenge.SNISuffix
	selfCert, selfKey, err := httputil.GenSelfTLS(challengeHostname)
	if err != nil {
		return "", fmt.Errorf("could not generate self-signed certificate: %v", err)
	}
	gpgchallengeCert, err := tls.X509KeyPair(selfCert, selfKey)
	if err != nil {
		return "", fmt.Errorf("could not load TLS certificate: %v", err)
	}
	_, keyId, err := config.KeyRingAndId()
	if err != nil {
		return "", fmt.Errorf("could not get keyId for camliNet hostname: %v", err)
	}
	// catch future length changes
	if len(keyId) != 16 {
		panic("length of GPG keyId is not 16 anymore")
	}
	shortKeyId := keyId[8:]
	camliNetHostName = strings.ToLower(shortKeyId + "." + camliNetDomain)
	m := autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(camliNetHostName),
		Cache:      autocert.DirCache(osutil.DefaultLetsEncryptCache()),
	}
	go func() {
		err := http.ListenAndServe(":http", m.HTTPHandler(nil))
		log.Fatalf("Could not start server for http-01 challenge: %v", err)
	}()
	getCertificate := func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if hello.ServerName == challengeHostname {
			return &gpgchallengeCert, nil
		}
		return m.GetCertificate(hello)
	}
	log.Printf("TLS enabled, with Let's Encrypt for %v", camliNetHostName)
	ws.SetTLS(webserver.TLSSetup{CertManager: getCertificate})

	err = ws.Listen(fmt.Sprintf(":%d", gpgchallenge.ClientChallengedPort))
	if err != nil {
		return "", fmt.Errorf("Listen: %v", err)
	}
	return fmt.Sprintf("https://%s", camliNetHostName), nil
}

// registerDNSChallengeHandler initializes and returns the
// gpgchallenge Client if camliNetIP is configured and if so,
// registers its handler with Perkeep's muxer.
//
// If camlistore.net support isn't enabled, it returns (nil, nil).
func registerDNSChallengeHandler(ws *webserver.Server, config *serverinit.Config) (*gpgchallenge.Client, error) {
	camliNetIP := config.CamliNetIP()
	if camliNetIP == "" {
		return nil, nil
	}
	if ip := net.ParseIP(camliNetIP); ip == nil {
		return nil, fmt.Errorf("camliNetIP value %q is not a valid IP address", camliNetIP)
	}

	keyRing, keyId, err := config.KeyRingAndId()
	if err != nil {
		return nil, err
	}

	cl, err := gpgchallenge.NewClient(keyRing, keyId, camliNetIP)
	if err != nil {
		return nil, fmt.Errorf("could not init gpgchallenge client: %v", err)
	}
	ws.Handle(cl.Handler())
	return cl, nil
}

// requestHostName performs the GPG challenge to register/obtain a name in the
// camlistore.net domain. The acquired name should be "<gpgKeyId>.camlistore.net",
// where <gpgKeyId> is the short form (8 trailing chars) of Perkeep's keyId.
// It also starts a goroutine that will rerun the challenge every hour, to keep
// the camlistore.net DNS server up to date.
func requestHostName(cl *gpgchallenge.Client) error {
	if err := cl.Challenge(camliNetDNS); err != nil {
		return err
	}

	if env.OnGCE() {
		if err := gce.SetInstanceHostname(camliNetHostName); err != nil {
			return fmt.Errorf("error setting instance camlistore-hostname: %v", err)
		}
	}

	var repeatChallengeFn func()
	repeatChallengeFn = func() {
		if err := cl.Challenge(camliNetDNS); err != nil {
			log.Printf("error with hourly DNS challenge: %v", err)
		}
		time.AfterFunc(time.Hour, repeatChallengeFn)
	}
	time.AfterFunc(time.Hour, repeatChallengeFn)
	return nil
}
