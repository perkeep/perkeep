/*
Copyright 2011 Google Inc.

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

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/serverconfig"
	"camlistore.org/pkg/webserver"

	// Storage options:
	_ "camlistore.org/pkg/blobserver/cond"
	_ "camlistore.org/pkg/blobserver/localdisk"
	_ "camlistore.org/pkg/blobserver/remote"
	_ "camlistore.org/pkg/blobserver/replica"
	_ "camlistore.org/pkg/blobserver/s3"
	_ "camlistore.org/pkg/blobserver/shard"
	_ "camlistore.org/pkg/index"

	// BROKEN TODO GO1
	// _ "camlistore/pkg/mysqlindexer" // indexer, but uses storage interface

	// Handlers:
	_ "camlistore.org/pkg/search"
	_ "camlistore.org/pkg/server" // UI, publish, etc
)

const (
	defCert = "config/selfgen_cert.pem"
	defKey = "config/selfgen_key.pem"
)

var (
	flagConfigFile = flag.String("configfile", "serverconfig",
		"Config file to use, relative to camli config dir root, or blank to not use config files.")
)

func exitf(pattern string, args ...interface{}) {
	if !strings.HasSuffix(pattern, "\n") {
		pattern = pattern + "\n"
	}
	fmt.Fprintf(os.Stderr, pattern, args...)
	os.Exit(1)
}

// Mostly copied from $GOROOT/src/pkg/crypto/tls/generate_cert.go
func genSelfTLS() error {
	priv, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %s", err)
	}

	now := time.Now()

	baseurl := os.Getenv("CAMLI_BASEURL")
	if baseurl == "" {
		return fmt.Errorf("CAMLI_BASEURL is not set")
	}
	split := strings.Split(baseurl, ":")
	hostname := split[1]
	hostname = hostname[2:len(hostname)]

	template := x509.Certificate{
		SerialNumber: new(big.Int).SetInt64(0),
		Subject: pkix.Name{
			CommonName:   hostname,
			Organization: []string{hostname},
		},
		NotBefore:    now.Add(-5 * time.Minute).UTC(),
		NotAfter:     now.AddDate(1, 0, 0).UTC(),
		SubjectKeyId: []byte{1, 2, 3, 4},
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("Failed to create certificate: %s", err)
	}

	certOut, err := os.Create(defCert)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing: %s", defCert, err)
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	certOut.Close()
	log.Printf("written %s\n", defCert)

	keyOut, err := os.OpenFile(defKey, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing:", defKey, err)
	}
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	keyOut.Close()
	log.Printf("written %s\n", defKey)
	return nil
}

func main() {
	flag.Parse()

	file := *flagConfigFile
	if !filepath.IsAbs(file) {
		file = filepath.Join(osutil.CamliConfigDir(), file)
	}
	config, err := serverconfig.Load(file)
	if err != nil {
		exitf("Could not load server config: %v", err)
	}

	ws := webserver.New()
	baseURL := ws.BaseURL()

	{
		cert, key := config.OptionalString("TLSCertFile", ""), config.OptionalString("TLSKeyFile", "")
		secure := config.OptionalBool("https", true)
		if secure {
			if (cert != "") != (key != "") {
				exitf("TLSCertFile and TLSKeyFile must both be either present or absent")
			}

			if cert == defCert && key == defKey {
				_, err1 := os.Stat(cert)
				_, err2 := os.Stat(key)
				if err1 != nil || err2 != nil {
					if os.IsNotExist(err1) || os.IsNotExist(err2) {
						err = genSelfTLS()
						if err != nil {
							exitf("Could not generate self signed creds: %q", err)
						}
					} else {
						exitf("Could not stat cert or key: %q, %q", err1, err2)
					}
				}
			}
			if cert == "" && key == "" {
				err = genSelfTLS()
				if err != nil {
					exitf("Could not generate self signed creds: %q", err)
				}
				cert = defCert
				key = defKey
			}
			ws.SetTLS(cert, key)
		}
	}

	err = config.InstallHandlers(ws, baseURL, nil)
	if err != nil {
		exitf("Error parsing config: %v", err)
	}

	ws.Listen()

	if config.UIPath != "" {
		uiURL := ws.BaseURL() + config.UIPath
		log.Printf("UI available at %s", uiURL)
		if runtime.GOOS == "windows" {
			// Might be double-clicking an icon with no shell window?
			// Just open the URL for them.
			osutil.OpenURL(uiURL)
		}
	}
	ws.Serve()
}
