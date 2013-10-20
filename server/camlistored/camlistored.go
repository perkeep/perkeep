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

// The camlistored binary is the Camlistore server.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/big"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"camlistore.org/pkg/buildinfo"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/misc"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/serverconfig"
	"camlistore.org/pkg/webserver"

	// Storage options:
	_ "camlistore.org/pkg/blobserver/cond"
	_ "camlistore.org/pkg/blobserver/diskpacked"
	_ "camlistore.org/pkg/blobserver/encrypt"
	_ "camlistore.org/pkg/blobserver/google/cloudstorage"
	_ "camlistore.org/pkg/blobserver/google/drive"
	_ "camlistore.org/pkg/blobserver/localdisk"
	_ "camlistore.org/pkg/blobserver/remote"
	_ "camlistore.org/pkg/blobserver/replica"
	_ "camlistore.org/pkg/blobserver/s3"
	_ "camlistore.org/pkg/blobserver/shard"
	// Indexers: (also present themselves as storage targets)
	_ "camlistore.org/pkg/index" // base indexer + in-memory dev index
	_ "camlistore.org/pkg/index/kvfile"
	_ "camlistore.org/pkg/index/mongo"
	_ "camlistore.org/pkg/index/mysql"
	_ "camlistore.org/pkg/index/postgres"
	"camlistore.org/pkg/index/sqlite"

	// Handlers:
	_ "camlistore.org/pkg/search"
	_ "camlistore.org/pkg/server" // UI, publish, etc

	// Importers:
	_ "camlistore.org/pkg/importer/dummy"
	_ "camlistore.org/pkg/importer/flickr"
)

const (
	defCert = serverconfig.DefaultTLSCert
	defKey  = serverconfig.DefaultTLSKey
)

var (
	flagVersion    = flag.Bool("version", false, "show version")
	flagConfigFile = flag.String("configfile", "",
		"Config file to use, relative to the Camlistore configuration directory root. If blank, the default is used or auto-generated.")
	listenFlag      = flag.String("listen", "", "host:port to listen on, or :0 to auto-select. If blank, the value in the config will be used instead.")
	flagOpenBrowser = flag.Bool("openbrowser", true, "Launches the UI on startup")
	flagPollParent  bool
)

func init() {
	if debug, _ := strconv.ParseBool(os.Getenv("CAMLI_DEBUG")); debug {
		flag.BoolVar(&flagPollParent, "pollparent", false, "Camlistored regularly polls its parent process to detect if it has been orphaned, and terminates in that case. Mainly useful for tests.")
	}
}

func exitf(pattern string, args ...interface{}) {
	if !strings.HasSuffix(pattern, "\n") {
		pattern = pattern + "\n"
	}
	fmt.Fprintf(os.Stderr, pattern, args...)
	os.Exit(1)
}

// 1) We do not want to force the user to buy a cert.
// 2) We still want our client (camput) to be able to
// verify the cert's authenticity.
// 3) We want to avoid MITM attacks and warnings in
// the browser.
// Using a simple self-signed won't do because of 3),
// as Chrome offers no way to set a self-signed as
// trusted when importing it. (same on android).
// We could have created a self-signed CA (that we
// would import in the browsers) and create another
// cert (signed by that CA) which would be the one
// used in camlistore.
// We're doing even simpler: create a self-signed
// CA and directly use it as a self-signed cert
// (and install it as a CA in the browsers).
// 2) is satisfied by doing our own checks,
// See pkg/client
func genSelfTLS(listen string) error {
	priv, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %s", err)
	}

	now := time.Now()

	hostname, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("splitting listen failed: %q", err)
	}

	// TODO(mpl): if no host is specified in the listening address
	// (e.g ":3179") we'll end up in this case, and the self-signed
	// will have "localhost" as a CommonName. But I don't think
	// there's anything we can do about it. Maybe warn...
	if hostname == "" {
		hostname = "localhost"
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
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:         true,
		BasicConstraintsValid: true,
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
	cert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return fmt.Errorf("Failed to parse certificate: %v", err)
	}
	sig := misc.SHA1Prefix(cert.Raw)
	hint := "You must add this certificate's fingerprint to your client's trusted certs list to use it. Like so:\n" +
		`"trustedCerts": ["` + sig + `"],`
	log.Printf(hint)

	keyOut, err := os.OpenFile(defKey, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing:", defKey, err)
	}
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	keyOut.Close()
	log.Printf("written %s\n", defKey)
	return nil
}

// findConfigFile returns the absolute path of the user's
// config file.
// The provided file may be absolute or relative
// to the user's configuration directory.
// If file is empty, a default high-level config is written
// for the user.
func findConfigFile(file string) (absPath string, isNewConfig bool, err error) {
	switch {
	case file == "":
		absPath = osutil.UserServerConfigPath()
		_, err = os.Stat(absPath)
		if os.IsNotExist(err) {
			err = os.MkdirAll(osutil.CamliConfigDir(), 0700)
			if err != nil {
				return
			}
			log.Printf("Generating template config file %s", absPath)
			if err = newDefaultConfigFile(absPath); err == nil {
				isNewConfig = true
			}
		}
		return
	case filepath.IsAbs(file):
		absPath = file
	default:
		absPath = filepath.Join(osutil.CamliConfigDir(), file)
	}
	_, err = os.Stat(absPath)
	return
}

type defaultConfigFile struct {
	Listen             string        `json:"listen"`
	HTTPS              bool          `json:"https"`
	Auth               string        `json:"auth"`
	Identity           string        `json:"identity"`
	IdentitySecretRing string        `json:"identitySecretRing"`
	KVFile             string        `json:"kvIndexFile"`
	BlobPath           string        `json:"blobPath"`
	MySQL              string        `json:"mysql"`
	Mongo              string        `json:"mongo"`
	Postgres           string        `json:"postgres"`
	SQLite             string        `json:"sqlite"`
	S3                 string        `json:"s3"`
	ReplicateTo        []interface{} `json:"replicateTo"`
	Publish            struct{}      `json:"publish"`
}

func newDefaultConfigFile(path string) error {
	conf := defaultConfigFile{
		Listen:      ":3179",
		HTTPS:       false,
		Auth:        "localhost",
		ReplicateTo: make([]interface{}, 0),
	}
	blobDir := osutil.CamliBlobRoot()
	if err := os.MkdirAll(blobDir, 0700); err != nil {
		return fmt.Errorf("Could not create default blobs directory: %v", err)
	}
	conf.BlobPath = blobDir
	if sqlite.CompiledIn() {
		conf.SQLite = filepath.Join(osutil.CamliVarDir(), "camli-index.db")
		if fi, err := os.Stat(conf.SQLite); os.IsNotExist(err) || (fi != nil && fi.Size() == 0) {
			if err := initSQLiteDB(conf.SQLite); err != nil {
				log.Printf("Error initializing DB %s: %v", conf.SQLite, err)
			}
		}
	} else {
		conf.KVFile = filepath.Join(osutil.CamliVarDir(), "camli-index.kvdb")
	}

	var keyId string
	secRing := osutil.IdentitySecretRing()
	_, err := os.Stat(secRing)
	switch {
	case err == nil:
		keyId, err = jsonsign.KeyIdFromRing(secRing)
		log.Printf("Re-using identity with keyId %q found in file %s", keyId, secRing)
	case os.IsNotExist(err):
		keyId, err = jsonsign.GenerateNewSecRing(secRing)
		log.Printf("Generated new identity with keyId %q in file %s", keyId, secRing)
	}
	if err != nil {
		return fmt.Errorf("Secret ring: %v", err)
	}
	conf.Identity = keyId
	conf.IdentitySecretRing = secRing

	confData, err := json.MarshalIndent(conf, "", "    ")
	if err != nil {
		return fmt.Errorf("Could not json encode config file : %v", err)
	}

	if err := ioutil.WriteFile(path, confData, 0600); err != nil {
		return fmt.Errorf("Could not create or write default server config: %v", err)
	}

	return nil
}

func initSQLiteDB(path string) error {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, tableSql := range sqlite.SQLCreateTables() {
		if _, err := db.Exec(tableSql); err != nil {
			return err
		}
	}
	if sqlite.IsWALCapable() {
		if _, err := db.Exec(sqlite.EnableWAL()); err != nil {
			return err
		}
	} else {
		log.Print("WARNING: An SQLite indexer without Write Ahead Logging will most likely fail. See http://camlistore.org/issues/114\n")
	}
	_, err = db.Exec(fmt.Sprintf(`REPLACE INTO meta VALUES ('version', '%d')`, sqlite.SchemaVersion()))
	return err
}

func setupTLS(ws *webserver.Server, config *serverconfig.Config, listen string) {
	cert, key := config.OptionalString("TLSCertFile", ""), config.OptionalString("TLSKeyFile", "")
	if !config.OptionalBool("https", true) {
		return
	}
	if (cert != "") != (key != "") {
		exitf("TLSCertFile and TLSKeyFile must both be either present or absent")
	}

	if cert == defCert && key == defKey {
		_, err1 := os.Stat(cert)
		_, err2 := os.Stat(key)
		if err1 != nil || err2 != nil {
			if os.IsNotExist(err1) || os.IsNotExist(err2) {
				if err := genSelfTLS(listen); err != nil {
					exitf("Could not generate self-signed TLS cert: %q", err)
				}
			} else {
				exitf("Could not stat cert or key: %q, %q", err1, err2)
			}
		}
	}
	if cert == "" && key == "" {
		err := genSelfTLS(listen)
		if err != nil {
			exitf("Could not generate self signed creds: %q", err)
		}
		cert = defCert
		key = defKey
	}
	data, err := ioutil.ReadFile(cert)
	if err != nil {
		exitf("Failed to read pem certificate: %s", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		exitf("Failed to decode pem certificate")
	}
	certif, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		exitf("Failed to parse certificate: %v", err)
	}
	sig := misc.SHA1Prefix(certif.Raw)
	log.Printf("TLS enabled, with certificate fingerprint: %v", sig)
	ws.SetTLS(cert, key)
}

func handleSignals(shutdownc <-chan io.Closer) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)
	signal.Notify(c, syscall.SIGINT)
	for {
		sig := <-c
		sysSig, ok := sig.(syscall.Signal)
		if !ok {
			log.Fatal("Not a unix signal")
		}
		switch sysSig {
		case syscall.SIGHUP:
			log.Print("SIGHUP: restarting camli")
			err := osutil.RestartProcess()
			if err != nil {
				log.Fatal("Failed to restart: " + err.Error())
			}
		case syscall.SIGINT:
			log.Print("Got SIGTERM: shutting down")
			donec := make(chan bool)
			go func() {
				cl := <-shutdownc
				if err := cl.Close(); err != nil {
					exitf("Error shutting down: %v", err)
				}
				donec <- true
			}()
			select {
			case <-donec:
				log.Printf("Shut down.")
				os.Exit(0)
			case <-time.After(2 * time.Second):
				exitf("Timeout shutting down. Exiting uncleanly.")
			}
		default:
			log.Fatal("Received another signal, should not happen.")
		}
	}
}

// listenAndBaseURL finds the configured, default, or inferred listen address
// and base URL from the command-line flags and provided config.
func listenAndBaseURL(config *serverconfig.Config) (listen, baseURL string) {
	baseURL = config.OptionalString("baseURL", "")
	listen = *listenFlag
	listenConfig := config.OptionalString("listen", "")
	// command-line takes priority over config
	if listen == "" {
		listen = listenConfig
		if listen == "" {
			exitf("\"listen\" needs to be specified either in the config or on the command line")
		}
	}
	return
}

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Fprintf(os.Stderr, "camlistored version: %s\n", buildinfo.Version())
		return
	}

	shutdownc := make(chan io.Closer, 1) // receives io.Closer to cleanly shut down
	go handleSignals(shutdownc)

	fileName, isNewConfig, err := findConfigFile(*flagConfigFile)
	if err != nil {
		exitf("Error finding config file %q: %v", fileName, err)
	}
	log.Printf("Using config file %s", fileName)
	config, err := serverconfig.Load(fileName)
	if err != nil {
		exitf("Could not load server config: %v", err)
	}

	ws := webserver.New()
	listen, baseURL := listenAndBaseURL(config)

	setupTLS(ws, config, listen)
	shutdownCloser, err := config.InstallHandlers(ws, baseURL, nil)
	if err != nil {
		exitf("Error parsing config: %v", err)
	}
	shutdownc <- shutdownCloser

	err = ws.Listen(listen)
	if err != nil {
		exitf("Listen: %v", err)
	}

	urlToOpen := ws.ListenURL()
	if baseURL != "" {
		urlToOpen = baseURL
	}
	if !isNewConfig {
		// user may like to configure the server at the initial startup,
		// open UI if this is not the first run with a new config file.
		urlToOpen += config.UIPath
	}

	log.Printf("Available on %s", urlToOpen)
	if *flagOpenBrowser {
		go osutil.OpenURL(urlToOpen)
	}

	go ws.Serve()
	if flagPollParent {
		osutil.DieOnParentDeath()
	}
	select {}
}
