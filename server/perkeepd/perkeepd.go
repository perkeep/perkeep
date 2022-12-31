/*
Copyright 2011 The Perkeep Authors

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

// The perkeepd binary is the Perkeep server.
package main // import "perkeep.org/server/perkeepd"

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"perkeep.org/internal/geocode"
	"perkeep.org/internal/httputil"
	"perkeep.org/internal/netutil"
	"perkeep.org/internal/osutil"
	"perkeep.org/internal/osutil/gce"
	"perkeep.org/pkg/buildinfo"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/serverinit"
	"perkeep.org/pkg/webserver"

	// VM environments:
	// for init side-effects + LogWriter

	// Storage options:
	_ "perkeep.org/pkg/blobserver/azure"
	"perkeep.org/pkg/blobserver/blobpacked"
	_ "perkeep.org/pkg/blobserver/cond"
	_ "perkeep.org/pkg/blobserver/diskpacked"
	_ "perkeep.org/pkg/blobserver/encrypt"
	_ "perkeep.org/pkg/blobserver/google/cloudstorage"
	_ "perkeep.org/pkg/blobserver/google/drive"
	_ "perkeep.org/pkg/blobserver/localdisk"
	_ "perkeep.org/pkg/blobserver/mongo"
	_ "perkeep.org/pkg/blobserver/overlay"
	_ "perkeep.org/pkg/blobserver/proxycache"
	_ "perkeep.org/pkg/blobserver/remote"
	_ "perkeep.org/pkg/blobserver/replica"
	_ "perkeep.org/pkg/blobserver/s3"
	_ "perkeep.org/pkg/blobserver/shard"
	_ "perkeep.org/pkg/blobserver/union"

	// Indexers: (also present themselves as storage targets)
	// KeyValue implementations:
	_ "perkeep.org/pkg/sorted/kvfile"
	_ "perkeep.org/pkg/sorted/leveldb"
	_ "perkeep.org/pkg/sorted/mongo"
	_ "perkeep.org/pkg/sorted/mysql"
	_ "perkeep.org/pkg/sorted/postgres"

	// Handlers:
	_ "perkeep.org/pkg/search"
	_ "perkeep.org/pkg/server" // UI, publish, etc

	// Importers:
	_ "perkeep.org/pkg/importer/allimporters"

	// Licence:
	_ "perkeep.org/pkg/camlegal"

	"go4.org/legal"
	"go4.org/wkfs"

	"golang.org/x/crypto/acme/autocert"
)

var (
	flagVersion    = flag.Bool("version", false, "show version")
	flagHelp       = flag.Bool("help", false, "show usage")
	flagLegal      = flag.Bool("legal", false, "show licenses")
	flagConfigFile = flag.String("configfile", "",
		"Config file to use, relative to the Perkeep configuration directory root. "+
			"If blank, the default is used or auto-generated. "+
			"If it starts with 'http:' or 'https:', it is fetched from the network.")
	flagListen      = flag.String("listen", "", "host:port to listen on, or :0 to auto-select. If blank, the value in the config will be used instead.")
	flagOpenBrowser = flag.Bool("openbrowser", true, "Launches the UI on startup")
	flagReindex     = flag.Bool("reindex", false, "Reindex all blobs on startup")
	flagRecovery    = flag.Int("recovery", 0, "Recovery mode: it corresponds for now to the recovery modes of the blobpacked package. Which means: 0 does nothing, 1 rebuilds the blobpacked index without erasing it, and 2 wipes the blobpacked index before rebuilding it.")
	flagSyslog      = flag.Bool("syslog", false, "Log everything only to syslog. It is an error to use this flag on windows.")
	flagKeepGoing   = flag.Bool("keep-going", false, "Continue after reindex or blobpacked recovery errors")
	flagPollParent  bool
)

func init() {
	if debug, _ := strconv.ParseBool(os.Getenv("CAMLI_MORE_FLAGS")); debug {
		flag.BoolVar(&flagPollParent, "pollparent", false, "Perkeepd regularly polls its parent process to detect if it has been orphaned, and terminates in that case. Mainly useful for tests.")
	}
}

func exitf(pattern string, args ...interface{}) {
	if !strings.HasSuffix(pattern, "\n") {
		pattern = pattern + "\n"
	}
	fmt.Fprintf(os.Stderr, pattern, args...)
	os.Exit(1)
}

func slurpURL(urls string, limit int64) ([]byte, error) {
	res, err := http.Get(urls)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return ioutil.ReadAll(io.LimitReader(res.Body, limit))
}

// loadConfig returns the server's parsed config file, locating it using the provided arg.
//
// The arg may be of the form:
//   - empty, to mean automatic (will write a default high-level config if
//     no cloud config is available)
//   - a filepath absolute or relative to the user's configuration directory,
//   - a URL
func loadConfig(arg string) (*serverinit.Config, error) {
	if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
		contents, err := slurpURL(arg, 256<<10)
		if err != nil {
			return nil, err
		}
		return serverinit.Load(contents)
	}
	var absPath string
	switch {
	case arg == "":
		absPath = osutil.UserServerConfigPath()
		_, err := wkfs.Stat(absPath)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		conf, err := serverinit.DefaultEnvConfig()
		if err != nil || conf != nil {
			return conf, err
		}
		configDir, err := osutil.PerkeepConfigDir()
		if err != nil {
			return nil, err
		}
		if err := wkfs.MkdirAll(configDir, 0700); err != nil {
			return nil, err
		}
		log.Printf("Generating template config file %s", absPath)
		if err := serverinit.WriteDefaultConfigFile(absPath); err != nil {
			return nil, err
		}
	case filepath.IsAbs(arg):
		absPath = arg
	default:
		configDir, err := osutil.PerkeepConfigDir()
		if err != nil {
			return nil, err
		}
		absPath = filepath.Join(configDir, arg)
	}
	return serverinit.LoadFile(absPath)
}

// If cert/key files are specified, and found, use them.
// If cert/key files are specified, not found, and the default values, generate
// them (self-signed CA used as a cert), and use them.
// If cert/key files are not specified, use Let's Encrypt.
func setupTLS(ws *webserver.Server, config *serverinit.Config, hostname string) {
	cert, key := config.HTTPSCert(), config.HTTPSKey()
	if !config.HTTPS() {
		return
	}
	if (cert != "") != (key != "") {
		exitf("httpsCert and httpsKey must both be either present or absent")
	}

	defCert := osutil.DefaultTLSCert()
	defKey := osutil.DefaultTLSKey()
	const hint = "You must add this certificate's fingerprint to your client's trusted certs list to use it. Like so:\n\"trustedCerts\": [\"%s\"],"
	if cert == defCert && key == defKey {
		_, err1 := wkfs.Stat(cert)
		_, err2 := wkfs.Stat(key)
		if err1 != nil || err2 != nil {
			if os.IsNotExist(err1) || os.IsNotExist(err2) {
				sig, err := httputil.GenSelfTLSFiles(hostname, defCert, defKey)
				if err != nil {
					exitf("Could not generate self-signed TLS cert: %q", err)
				}
				log.Printf(hint, sig)
			} else {
				exitf("Could not stat cert or key: %q, %q", err1, err2)
			}
		}
	}
	if cert == "" && key == "" {
		// Use Let's Encrypt if no files are specified, and we have a usable hostname.
		if netutil.IsFQDN(hostname) {
			m := autocert.Manager{
				Prompt:     autocert.AcceptTOS,
				HostPolicy: autocert.HostWhitelist(hostname),
				Cache:      autocert.DirCache(osutil.DefaultLetsEncryptCache()),
			}
			ws.SetTLS(webserver.TLSSetup{
				CertManager: m.GetCertificate,
			})
			log.Printf("Using Let's Encrypt tls-alpn-01 for %v", hostname)
			return
		}
		// Otherwise generate new certificates
		sig, err := httputil.GenSelfTLSFiles(hostname, defCert, defKey)
		if err != nil {
			exitf("Could not generate self signed creds: %q", err)
		}
		log.Printf(hint, sig)
		cert = defCert
		key = defKey
	}
	data, err := wkfs.ReadFile(cert)
	if err != nil {
		exitf("Failed to read pem certificate: %s", err)
	}
	sig, err := httputil.CertFingerprint(data)
	if err != nil {
		exitf("certificate error: %v", err)
	}
	log.Printf("TLS enabled, with SHA-256 certificate fingerprint: %v", sig)
	ws.SetTLS(webserver.TLSSetup{
		CertFile: cert,
		KeyFile:  key,
	})
}

func handleSignals(shutdownc <-chan io.Closer) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	for {
		sig := <-c
		sysSig, ok := sig.(syscall.Signal)
		if !ok {
			log.Fatal("Not a unix signal")
		}
		switch sysSig {
		case syscall.SIGHUP:
			log.Printf(`Got "%v" signal: restarting camli`, sig)
			err := osutil.RestartProcess()
			if err != nil {
				log.Fatal("Failed to restart: " + err.Error())
			}
		case syscall.SIGINT, syscall.SIGTERM:
			log.Printf(`Got "%v" signal: shutting down`, sig)
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

// listen discovers the listen address, base URL, and hostname that the ws is
// going to use, sets up the TLS configuration, and starts listening.
//
// If camliNetIP is configured, it also prepares for the GPG
// challenge, to register/acquire a name in the camlistore.net domain.
func listen(ws *webserver.Server, config *serverinit.Config) (baseURL string, err error) {
	camliNetIP := config.CamliNetIP()
	if camliNetIP != "" {
		return listenForCamliNet(ws, config)
	}

	baseURL = config.BaseURL()

	// Prefer the --listen flag value. Otherwise use the config value.
	listen := *flagListen
	if listen == "" {
		listen = config.ListenAddr()
	}
	if listen == "" {
		exitf("\"listen\" needs to be specified either in the config or on the command line")
	}

	hostname, err := certHostname(listen, baseURL)
	if err != nil {
		return "", fmt.Errorf("Bad baseURL or listen address: %v", err)
	}
	setupTLS(ws, config, hostname)

	err = ws.Listen(listen)
	if err != nil {
		return "", fmt.Errorf("Listen: %v", err)
	}
	if baseURL == "" {
		baseURL = ws.ListenURL()
	}
	return baseURL, nil
}

// certHostname figures out the name to use for the TLS certificates, using baseURL
// and falling back to the listen address if baseURL is empty or invalid.
func certHostname(listen, baseURL string) (string, error) {
	hostPort, err := netutil.HostPort(baseURL)
	if err != nil {
		hostPort = listen
	}
	hostname, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return "", fmt.Errorf("failed to find hostname for cert from address %q: %v", hostPort, err)
	}
	return hostname, nil
}

func setBlobpackedRecovery() {
	if *flagRecovery == 0 && env.OnGCE() {
		*flagRecovery = gce.BlobpackedRecoveryValue()
	}
	if blobpacked.RecoveryMode(*flagRecovery) > blobpacked.NoRecovery {
		blobpacked.SetRecovery(blobpacked.RecoveryMode(*flagRecovery))
	}
}

// checkGeoKey returns nil if we have a Google Geocoding API key file stored
// in the config dir. Otherwise it returns instruction about it as the error.
func checkGeoKey() error {
	if _, err := geocode.GetAPIKey(); err == nil {
		return nil
	}
	keyPath, err := geocode.GetAPIKeyPath()
	if err != nil {
		return fmt.Errorf("error getting Geocoding API key path: %v", err)
	}
	if env.OnGCE() {
		keyPath = strings.TrimPrefix(keyPath, "/gcs/")
		return fmt.Errorf("using OpenStreetMap for location related requests. To use the Google Geocoding API, create a key (see https://developers.google.com/maps/documentation/geocoding/get-api-key ) and save it in your VM's configuration bucket as: %v", keyPath)
	}
	return fmt.Errorf("using OpenStreetMap for location related requests. To use the Google Geocoding API, create a key (see https://developers.google.com/maps/documentation/geocoding/get-api-key ) and save it in Perkeep's configuration directory as: %v", keyPath)
}

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Fprintf(os.Stderr, "perkeepd version: %s\nGo version: %s (%s/%s)\n",
			buildinfo.Summary(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}
	if *flagHelp {
		flag.Usage()
		os.Exit(0)
	}
	if *flagLegal {
		for _, l := range legal.Licenses() {
			fmt.Fprintln(os.Stderr, l)
		}
		return
	}
	setBlobpackedRecovery()

	// In case we're running in a Docker container with no
	// filesystem from which to load the root CAs, this
	// conditionally installs a static set if necessary. We do
	// this before we load the config file, which might come from
	// an https URL. And also before setting up the logging,
	// as it uses an http Client.
	httputil.InstallCerts()

	logCloser := setupLogging()
	defer func() {
		if err := logCloser.Close(); err != nil {
			log.SetOutput(os.Stderr)
			log.Printf("Error closing logger: %v", err)
		}
	}()

	log.Printf("Starting perkeepd version %s; Go %s (%s/%s)", buildinfo.Summary(), runtime.Version(),
		runtime.GOOS, runtime.GOARCH)

	shutdownc := make(chan io.Closer, 1) // receives io.Closer to cleanly shut down
	go handleSignals(shutdownc)

	config, err := loadConfig(*flagConfigFile)
	if err != nil {
		exitf("Error loading config file: %v", err)
	}

	ws := webserver.New()
	baseURL, err := listen(ws, config)
	if err != nil {
		exitf("Error starting webserver: %v", err)
	}

	challengeClient, err := registerDNSChallengeHandler(ws, config)
	if err != nil {
		exitf("Error registering challenge client with Perkeep muxer: %v", err)
	}

	config.SetReindex(*flagReindex)
	config.SetKeepGoing(*flagKeepGoing)

	// Finally, install the handlers. This also does the final config validation.
	shutdownCloser, err := config.InstallHandlers(ws, baseURL)
	if err != nil {
		exitf("Error parsing config: %v", err)
	}
	shutdownc <- shutdownCloser

	go ws.Serve()

	if challengeClient != nil {
		if err := requestHostName(challengeClient); err != nil {
			exitf("Could not register on camlistore.net: %v", err)
		}
	}
	if env.OnGCE() {
		gce.FixUserDataForPerkeepRename()
	}

	if err := checkGeoKey(); err != nil {
		log.Printf("perkeepd: %v", err)
	}

	urlToOpen := baseURL + config.UIPath()

	if *flagOpenBrowser {
		go osutil.OpenURL(urlToOpen)
	}

	if flagPollParent {
		osutil.DieOnParentDeath()
	}

	ctx := context.Background()
	if err := config.UploadPublicKey(ctx); err != nil {
		exitf("Error uploading public key on startup: %v", err)
	}

	if err := config.StartApps(); err != nil {
		exitf("StartApps: %v", err)
	}

	for appName, appURL := range config.AppURL() {
		addr, err := netutil.HostPort(appURL)
		if err != nil {
			log.Printf("Could not get app %v address: %v", appName, err)
			continue
		}
		if err := netutil.AwaitReachable(addr, 5*time.Second); err != nil {
			log.Printf("Could not reach app %v: %v", appName, err)
		}
	}
	log.Printf("server: available at %s", urlToOpen)

	select {}
}
