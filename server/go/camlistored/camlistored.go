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
	"big"
	"crypto/x509/pkix"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"
	"os"
	"path/filepath"

	"camli/osutil"
	"camli/serverconfig"
	"camli/webserver"

	// Storage options:
	_ "camli/blobserver/cond"
	_ "camli/blobserver/localdisk"
	_ "camli/blobserver/remote"
	_ "camli/blobserver/replica"
	_ "camli/blobserver/s3"
	_ "camli/blobserver/shard"
	_ "camli/mysqlindexer" // indexer, but uses storage interface
	// Handlers:
	_ "camli/search"
	_ "camli/server" // UI, publish, etc
)

const defCert = "config/selfgen_cert.pem"
const defKey = "config/selfgen_key.pem"

var flagConfigFile = flag.String("configfile", "serverconfig",
	"Config file to use, relative to camli config dir root, or blank to not use config files.")

func exitFailure(pattern string, args ...interface{}) {
	if !strings.HasSuffix(pattern, "\n") {
		pattern = pattern + "\n"
	}
	fmt.Fprintf(os.Stderr, pattern, args...)
	os.Exit(1)
}

// Mostly copied from $GOROOT/src/pkg/crypto/tls/generate_cert.go
func genSelfTLS() os.Error {
	priv, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %s", err)
	}

	now := time.Seconds()

	baseurl := os.Getenv("CAMLI_BASEURL")
	if baseurl == "" {
		return fmt.Errorf("CAMLI_BASEURL is not set")
	}
	split := strings.Split(baseurl, ":")
	hostname := split[1]
	hostname = hostname[2:len(hostname)]
	println(hostname)

	template := x509.Certificate{
		SerialNumber: new(big.Int).SetInt64(0),
		Subject: pkix.Name{
			CommonName:   hostname,
			Organization: []string{hostname},
		},
		NotBefore: time.SecondsToUTC(now - 300),
		NotAfter:  time.SecondsToUTC(now + 60*60*24*365), // valid for 1 year.

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
		exitFailure("Could not load server config: %v", err)
	}

	ws := webserver.New()
	baseURL := ws.BaseURL()

	{
		secure := config.OptionalBool("httpsOnly", true)
		if secure {
			cert, key := config.OptionalString("TLSCertFile", ""), config.OptionalString("TLSKeyFile", "")
			if (cert != "") != (key != "") {
				exitFailure("TLSCertFile and TLSKeyFile must both be either present or absent")
			}

			if cert == "" && key == "" {
				err = genSelfTLS()
				if err != nil {
					exitFailure("pb generating the self signed creds: %q", err)
				}
				cert = defCert
				key = defKey
			}
			ws.SetTLS(cert, key)
		}
	}

	err = config.InstallHandlers(ws, baseURL, nil)
	if err != nil {
		exitFailure("Error parsing config: %v", err)
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
