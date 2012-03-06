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
	"io/ioutil"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"camlistore.org/pkg/jsonconfig"
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
	defKey  = "config/selfgen_key.pem"
)

var (
	flagConfigFile = flag.String("configfile", "",
		"Config file to use, relative to camli config dir root, or blank to use the default config.")
)

func exitf(pattern string, args ...interface{}) {
	if !strings.HasSuffix(pattern, "\n") {
		pattern = pattern + "\n"
	}
	fmt.Fprintf(os.Stderr, pattern, args...)
	os.Exit(1)
}

// Mostly copied from $GOROOT/src/pkg/crypto/tls/generate_cert.go
func genSelfTLS(config *serverconfig.Config) error {
	priv, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %s", err)
	}

	now := time.Now()

	baseurl := config.RequiredString("baseURL")
	if baseurl == "" {
		return fmt.Errorf("baseURL not in config")
	}
	hostname, _, err := net.SplitHostPort(baseurl)
	if err != nil {
		return fmt.Errorf("splitting baseURL failed: %q", err)
	}

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

// checkConfigFile tests if the given config file is a
// valid path to an existing file, and returns the same
// value if yes, an error if not.
// If file is the empty string, it checks for the existence
// of a default config and creates it if absent. It returns
// newfile as the path to that file, or an error if a
// problem occured.
func checkConfigFile(file string) (newfile string, err error) {
	newfile = file
	if newfile == "" {
		newfile = osutil.UserServerConfigPath()
		_, err := os.Stat(newfile)
		if err != nil {
			log.Printf("Can't open default conf file, now creating a new one at %s \n", newfile)
			err = newDefaultConfigFile(newfile)
			if err != nil {
				return "", err
			}
		}
		return newfile, nil
	}
	if !filepath.IsAbs(newfile) {
		newfile = filepath.Join(osutil.CamliConfigDir(), newfile)
	}
	_, err = os.Stat(newfile)
	if err != nil {
		return "", fmt.Errorf("can't stat %s: %q", file, err)
	}
	return newfile, nil
}

func newDefaultConfigFile(path string) error {
	serverConf :=
		`{
   "listen": "localhost:3179",
   "TLS": false,
   "blobPath": "%BLOBPATH%",
   "mysql": "",
   "mongo": "",
   "s3": "",
   "replicateTo": []
}
`
	blobDir := filepath.Join(osutil.HomeDir(), "var", "camlistore", "blobs")
	if err := os.MkdirAll(blobDir, 0700); err != nil {
		return fmt.Errorf("Could not create default blobs directory: %v", err)
	}
	serverConf = strings.Replace(serverConf, "%BLOBPATH%", blobDir, 1)
	if err := ioutil.WriteFile(path, []byte(serverConf), 0700); err != nil {
		return fmt.Errorf("Could not create or write default server config: %v", err)
	}
	return nil
}

type configPrefixesParams struct {
	secretRing string
	indexerPath string
	mongo bool
	mysql bool
	dbname string
	blobPath string
}

func genDefaultConfigPrefixes(params *configPrefixesParams) jsonconfig.Obj {
	prefixes := jsonconfig.Obj{}

	ob := jsonconfig.Obj{}
	ob["handler"] = "root"
	ob["handlerArgs"] = jsonconfig.Obj{"stealth": false}
	prefixes["/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "publish"
	ob["handlerArgs"] = jsonconfig.Obj{
		"rootName": "blog",
		"blobRoot": "/bs-and-maybe-also-index/",
		"searchRoot": "/my-search/",
		"cache": "/cache/",
		"devBootstrapPermanodeUsing": "/sighelper/",
	}
	prefixes["/blog/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "publish"
	ob["handlerArgs"] = jsonconfig.Obj{
		"rootName": "pics",
		"blobRoot": "/bs-and-maybe-also-index/",
		"searchRoot": "/my-search/",
		"cache": "/cache/",
		"scaledImage": "lrucache",
		"css": []interface{}{"pics.css"},
		"js": []interface{}{"camli.js", "pics.js"},
		"devBootstrapPermanodeUsing": "/sighelper/",
	}
	prefixes["/pics/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "ui"
	ob["handlerArgs"] = jsonconfig.Obj{
		"blobRoot": "/bs-and-maybe-also-index/",
		"searchRoot": "/my-search/",
		"jsonSignRoot": "/sighelper/",
		"cache": "/cache/",
		"scaledImage": "lrucache",
		"publishRoots": []interface{}{"/blog/", "/pics/"},
	}
	prefixes["/ui/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "sync"
	ob["handlerArgs"] = jsonconfig.Obj{
		"from": "/bs/",
		"to": params.indexerPath,
	}
	prefixes["/sync/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "jsonsign"
	ob["handlerArgs"] = jsonconfig.Obj{
		"secretRing": params.secretRing,
		"keyId": "26F5ABDA",
		"publicKeyDest": "/bs/",
	}
	prefixes["/sighelper/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "storage-replica"
	ob["handlerArgs"] = jsonconfig.Obj{
		"backends": []interface{}{"/bs/", params.indexerPath},
	}
	prefixes["/bs-and-index/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "storage-cond"
// TODO(mpl): fix Obj/map inconsistencies
	ob["handlerArgs"] = jsonconfig.Obj{
		"write": jsonconfig.Obj{
			"if": "isSchema",
			"then": "/bs-and-index/",
			"else": "/bs/",
		},
		"read": "/bs/",
	}
	prefixes["/bs-and-maybe-also-index/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "storage-filesystem"
	ob["handlerArgs"] = jsonconfig.Obj{
		"path": params.blobPath,
	}
	prefixes["/bs/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "storage-filesystem"
	ob["handlerArgs"] = jsonconfig.Obj{
		"path": filepath.Join(params.blobPath, "/cache"),
	}
	prefixes["/cache/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "storage-shard"
	ob["handlerArgs"] = jsonconfig.Obj{
		"backends": []interface{}{"/s1/", "/s2/"},
	}
	prefixes["/sharder/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "storage-filesystem"
	ob["handlerArgs"] = jsonconfig.Obj{
		"path": filepath.Join(params.blobPath, "/s1/"),
	}
	prefixes["/s1/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "storage-filesystem"
	ob["handlerArgs"] = jsonconfig.Obj{
		"path": filepath.Join(params.blobPath, "/s2/"),
	}
	prefixes["/s2/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "storage-replica"
	ob["handlerArgs"] = jsonconfig.Obj{
		"backends": []interface{}{"/r1/", "/r2/", "/r3/"},
		"minWritesForSuccess": 2,
	}
	prefixes["/repl/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "storage-filesystem"
	ob["handlerArgs"] = jsonconfig.Obj{
		"path": filepath.Join(params.blobPath, "/r1/"),
	}
	prefixes["/r1/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "storage-filesystem"
	ob["handlerArgs"] = jsonconfig.Obj{
		"path": filepath.Join(params.blobPath, "/r2/"),
	}
	prefixes["/r2/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "storage-filesystem"
	ob["handlerArgs"] = jsonconfig.Obj{
		"path": filepath.Join(params.blobPath, "/r3/"),
	}
	prefixes["/r3/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "storage-memory-only-dev-indexer"
	ob["handlerArgs"] = jsonconfig.Obj{
		"blobSource": "/bs/",
	}
	prefixes["/index-mem/"] = ob

	ob = jsonconfig.Obj{}
	ob["enabled"] = params.mongo
	ob["handler"] = "storage-mongodbindexer"
	ob["handlerArgs"] = jsonconfig.Obj{
		"servers": "localhost",
		"database": params.dbname,
		"blobSource": "/bs/",
	}
	prefixes["/index-mongo/"] = ob

	ob = jsonconfig.Obj{}
	ob["enabled"] = params.mysql
	ob["handler"] = "storage-mysqlindexer"
	ob["handlerArgs"] = jsonconfig.Obj{
		"host": "127.0.0.1",
		"user": "root",
		"password": "root",
		"database": params.dbname,
		"blobSource": "/bs/",
	}
	prefixes["/index-mysql/"] = ob

	ob = jsonconfig.Obj{}
	ob["handler"] = "search"
	ob["handlerArgs"] = jsonconfig.Obj{
		"index": params.indexerPath,
		"owner": "sha1-f2b0b7da718b97ce8c31591d8ed4645c777f3ef4",
	}
	prefixes["/my-search/"] = ob

	return prefixes
}

func genLowLevelConfig(simpleConf *serverconfig.Config) (conf *serverconfig.Config, err error) {
	obj := jsonconfig.Obj{}
	baseUrl := simpleConf.RequiredString("listen")
	if baseUrl == "" {
		return nil, fmt.Errorf("\"listen\" missing in user config file")
	}
	// I move for baseURL to not contain the protocol prefix from now on. you ok with that?
	_, port, err := net.SplitHostPort(baseUrl)
	if err != nil {
		return nil, fmt.Errorf("failed splitting baseUrl: %v", err)
	}
	obj["baseURL"] = baseUrl
	obj["auth"] = "userpass:camlistore:pass" + port
	tls := simpleConf.RequiredBool("TLS")
	obj["https"] = tls
	obj["TLSCertFile"] = "config/selfgen_cert.pem"
	obj["TLSKeyFile"] = "config/selfgen_key.pem"

	// TODO(mpl): do not use env vars. use default values or derive them from
	// end user conf. Will do soon.
/*
	dbname := os.Getenv("CAMLI_DBNAME")
	if dbname == "" {
		exitf("CAMLI_DBNAME not defined")
	}
	secretRing := os.Getenv("CAMLI_SECRET_RING")
	if secretRing == "" {
		exitf("CAMLI_SECRET_RING not defined")
	}
	indexerPath := os.Getenv("CAMLI_INDEXER_PATH")
	if indexerPath == "" {
		exitf("CAMLI_INDEXER_PATH not defined")
	}
*/

	// TODO(mpl): mkdir all the blobs dirs
	dbname := "devcamlimpl"
	secretRing := "/home/mpl/gocode/src/camlistore.org/pkg/jsonsign/testdata/test-secring.gpg"
	indexerPath := "/index-mem/"
	blobPath := simpleConf.RequiredString("blobPath")
	prefixesParams := &configPrefixesParams{
		secretRing: secretRing,
		indexerPath: indexerPath,
		mongo: false,
		mysql: false,
		dbname: dbname,
		blobPath: blobPath,
	}

	obj["prefixes"] = genDefaultConfigPrefixes(prefixesParams)

	conf = &serverconfig.Config{
		jsonconfig.Obj: obj,
	}
	return conf, nil
}

func main() {
	flag.Parse()

	file, err := checkConfigFile(*flagConfigFile)
	if err != nil {
		exitf("Problem with config file: %q", err)
	}
	conf, err := serverconfig.Load(file)
	if err != nil {
		exitf("Could not load server config file %v: %v", file, err)
	}
	config, err := genLowLevelConfig(conf)
	if err != nil {
		exitf("Could not gen low level server config: %v", err)
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
						err = genSelfTLS(config)
						if err != nil {
							exitf("Could not generate self signed creds: %q", err)
						}
					} else {
						exitf("Could not stat cert or key: %q, %q", err1, err2)
					}
				}
			}
			if cert == "" && key == "" {
				err = genSelfTLS(config)
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
