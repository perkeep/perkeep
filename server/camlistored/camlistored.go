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
package main // import "camlistore.org/server/camlistored"

import (
	"crypto/tls"
	"errors"
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

	"camlistore.org/pkg/buildinfo"
	"camlistore.org/pkg/env"
	"camlistore.org/pkg/gpgchallenge"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/netutil"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/serverinit"
	"camlistore.org/pkg/webserver"

	// VM environments:
	"camlistore.org/pkg/osutil/gce" // for init side-effects + LogWriter

	// Storage options:
	_ "camlistore.org/pkg/blobserver/b2"
	"camlistore.org/pkg/blobserver/blobpacked"
	_ "camlistore.org/pkg/blobserver/cond"
	_ "camlistore.org/pkg/blobserver/diskpacked"
	_ "camlistore.org/pkg/blobserver/encrypt"
	_ "camlistore.org/pkg/blobserver/google/cloudstorage"
	_ "camlistore.org/pkg/blobserver/google/drive"
	_ "camlistore.org/pkg/blobserver/localdisk"
	_ "camlistore.org/pkg/blobserver/mongo"
	_ "camlistore.org/pkg/blobserver/proxycache"
	_ "camlistore.org/pkg/blobserver/remote"
	_ "camlistore.org/pkg/blobserver/replica"
	_ "camlistore.org/pkg/blobserver/s3"
	_ "camlistore.org/pkg/blobserver/shard"
	// Indexers: (also present themselves as storage targets)
	// KeyValue implementations:
	_ "camlistore.org/pkg/sorted/kvfile"
	_ "camlistore.org/pkg/sorted/leveldb"
	_ "camlistore.org/pkg/sorted/mongo"
	_ "camlistore.org/pkg/sorted/mysql"
	_ "camlistore.org/pkg/sorted/postgres"
	"camlistore.org/pkg/sorted/sqlite" // for sqlite.CompiledIn()

	// Handlers:
	_ "camlistore.org/pkg/search"
	_ "camlistore.org/pkg/server" // UI, publish, etc

	// Importers:
	_ "camlistore.org/pkg/importer/allimporters"

	// Licence:
	_ "camlistore.org/pkg/camlegal"

	"go4.org/legal"
	"go4.org/types"
	"go4.org/wkfs"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/logging"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

var (
	flagVersion    = flag.Bool("version", false, "show version")
	flagHelp       = flag.Bool("help", false, "show usage")
	flagLegal      = flag.Bool("legal", false, "show licenses")
	flagConfigFile = flag.String("configfile", "",
		"Config file to use, relative to the Camlistore configuration directory root. "+
			"If blank, the default is used or auto-generated. "+
			"If it starts with 'http:' or 'https:', it is fetched from the network.")
	flagListen      = flag.String("listen", "", "host:port to listen on, or :0 to auto-select. If blank, the value in the config will be used instead.")
	flagOpenBrowser = flag.Bool("openbrowser", true, "Launches the UI on startup")
	flagReindex     = flag.Bool("reindex", false, "Reindex all blobs on startup")
	flagRecovery    = flag.Bool("recovery", false, "Recovery mode: rebuild the blobpacked meta index. The tasks performed by the recovery mode might change in the future.")
	flagSyslog      = flag.Bool("syslog", false, "Log everything only to syslog. It is an error to use this flag on windows.")
	flagPollParent  bool
)

// For getting a name in camlistore.net
const (
	camliNetDNS    = "camnetdns.camlistore.org"
	camliNetDomain = "camlistore.net"
)

var camliNetHostName string // <keyId>.camlistore.net

// For logging on Google Cloud Logging when not running on Google Compute Engine
// (for debugging).
var (
	flagGCEProjectID string
	flagGCELogName   string
	flagGCEJWTFile   string
)

func init() {
	if debug, _ := strconv.ParseBool(os.Getenv("CAMLI_DEBUG")); debug {
		flag.BoolVar(&flagPollParent, "pollparent", false, "Camlistored regularly polls its parent process to detect if it has been orphaned, and terminates in that case. Mainly useful for tests.")
		flag.StringVar(&flagGCEProjectID, "gce_project_id", "", "GCE project ID; required by --gce_log_name.")
		flag.StringVar(&flagGCELogName, "gce_log_name", "", "log all messages to that log name on Google Cloud Logging as well.")
		flag.StringVar(&flagGCEJWTFile, "gce_jwt_file", "", "Filename to the GCE Service Account's JWT (JSON) config file; required by --gce_log_name.")
	}
}

func exitf(pattern string, args ...interface{}) {
	if !strings.HasSuffix(pattern, "\n") {
		pattern = pattern + "\n"
	}
	fmt.Fprintf(os.Stderr, pattern, args...)
	osExit(1)
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
// - empty, to mean automatic (will write a default high-level config if
//   no cloud config is available)
// - a filepath absolute or relative to the user's configuration directory,
// - a URL
func loadConfig(arg string) (conf *serverinit.Config, isNewConfig bool, err error) {
	if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
		contents, err := slurpURL(arg, 256<<10)
		if err != nil {
			return nil, false, err
		}
		conf, err = serverinit.Load(contents)
		return conf, false, err
	}
	var absPath string
	switch {
	case arg == "":
		absPath = osutil.UserServerConfigPath()
		_, err = wkfs.Stat(absPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return
			}
			conf, err = serverinit.DefaultEnvConfig()
			if err != nil || conf != nil {
				return
			}
			err = wkfs.MkdirAll(osutil.CamliConfigDir(), 0700)
			if err != nil {
				return
			}
			log.Printf("Generating template config file %s", absPath)
			if err = serverinit.WriteDefaultConfigFile(absPath, sqlite.CompiledIn()); err == nil {
				isNewConfig = true
			}
		}
	case filepath.IsAbs(arg):
		absPath = arg
	default:
		absPath = filepath.Join(osutil.CamliConfigDir(), arg)
	}
	conf, err = serverinit.LoadFile(absPath)
	return
}

// If cert/key files are specified, and found, use them.
// If cert/key files are specified, not found, and the default values, generate
// them (self-signed CA used as a cert), and use them.
// If cert/key files are not specified, use Let's Encrypt.
func setupTLS(ws *webserver.Server, config *serverinit.Config, hostname string) {
	cert, key := config.OptionalString("httpsCert", ""), config.OptionalString("httpsKey", "")
	if !config.OptionalBool("https", true) {
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
			log.Printf("TLS enabled, with Let's Encrypt for %v", hostname)
			ws.SetTLS(webserver.TLSSetup{
				CertManager: m.GetCertificate,
			})
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

var osExit = os.Exit // testing hook

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
				osExit(0)
			case <-time.After(2 * time.Second):
				exitf("Timeout shutting down. Exiting uncleanly.")
			}
		default:
			log.Fatal("Received another signal, should not happen.")
		}
	}
}

// listenForCamliNet prepares the TLS listener for both the GPG challenge, and
// for Let's Encrypt. It then starts listening and returns the baseURL derived from
// the hostname we should obtain from the GPG challenge.
func listenForCamliNet(ws *webserver.Server, config *serverinit.Config) (baseURL string, err error) {
	camliNetIP := config.OptionalString("camliNetIP", "")
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
	_, keyId, err := keyRingAndId(config)
	if err != nil {
		return "", fmt.Errorf("could not get keyId for camliNet hostname: %v", err)
	}
	camliNetHostName = strings.ToLower(keyId + "." + camliNetDomain)
	m := autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(camliNetHostName),
		Cache:      autocert.DirCache(osutil.DefaultLetsEncryptCache()),
	}
	getCertificate := func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if hello.ServerName == challengeHostname {
			return &gpgchallengeCert, nil
		}
		return m.GetCertificate(hello)
	}
	log.Printf("TLS enabled, with Let's Encrypt for %v", camliNetHostName)
	ws.SetTLS(webserver.TLSSetup{
		CertManager: getCertificate,
	})
	// Since we're not going through setupTLS, we need to consume manually the 3 below
	config.OptionalString("httpsCert", "")
	config.OptionalString("httpsKey", "")
	config.OptionalBool("https", true)

	err = ws.Listen(fmt.Sprintf(":%d", gpgchallenge.ClientChallengedPort))
	if err != nil {
		return "", fmt.Errorf("Listen: %v", err)
	}
	return fmt.Sprintf("https://%s", camliNetHostName), nil
}

// listen discovers the listen address, base URL, and hostname that the ws is
// going to use, sets up the TLS configuration, and starts listening.
// If camliNetIP, it also prepares for the GPG challenge, to register/acquire a
// name in the camlistore.net domain.
func listen(ws *webserver.Server, config *serverinit.Config) (baseURL string, err error) {
	camliNetIP := config.OptionalString("camliNetIP", "")
	if camliNetIP != "" {
		return listenForCamliNet(ws, config)
	}

	listen, baseURL := listenAndBaseURL(config)
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

func keyRingAndId(config *serverinit.Config) (keyRing, keyId string, err error) {
	prefixes := config.RequiredObject("prefixes")
	if len(prefixes) == 0 {
		return "", "", fmt.Errorf("no prefixes object in config")
	}
	sighelper := prefixes.OptionalObject("/sighelper/")
	if len(sighelper) == 0 {
		return "", "", fmt.Errorf("no sighelper object in prefixes")
	}
	handlerArgs := sighelper.OptionalObject("handlerArgs")
	if len(handlerArgs) == 0 {
		return "", "", fmt.Errorf("no handlerArgs object in sighelper")
	}
	keyId = handlerArgs.OptionalString("keyId", "")
	if keyId == "" {
		return "", "", fmt.Errorf("no keyId in sighelper")
	}
	keyRing = handlerArgs.OptionalString("secretRing", "")
	if keyRing == "" {
		return "", "", fmt.Errorf("no secretRing in sighelper")
	}
	return keyRing, keyId, nil
}

// muxChallengeHandler initializes the gpgchallenge Client, and registers its
// handler with Camlistore's muxer. The returned Client can then be used right
// after Camlistore starts serving HTTPS connections.
func muxChallengeHandler(ws *webserver.Server, config *serverinit.Config) (*gpgchallenge.Client, error) {
	camliNetIP := config.OptionalString("camliNetIP", "")
	if camliNetIP == "" {
		return nil, nil
	}
	if ip := net.ParseIP(camliNetIP); ip == nil {
		return nil, fmt.Errorf("camliNetIP value %q is not a valid IP address", camliNetIP)
	}

	keyRing, keyId, err := keyRingAndId(config)
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

// setInstanceHostname sets the "camlistore-hostname" metadata on the GCE
// instance where camlistored is running. The value set is the same as the one we
// register with the camlistore.net DNS, i.e. "<gpgKeyId>.camlistore.net", where
// <gpgKeyId> is Camlistore's keyId.
func setInstanceHostname() error {
	if !env.OnGCE() {
		return nil
	}

	hostname, err := metadata.InstanceAttributeValue("camlistore-hostname")
	if err != nil {
		if _, ok := err.(metadata.NotDefinedError); !ok {
			return fmt.Errorf("error getting existing camlistore-hostname: %v", err)
		}
	}
	if err == nil && hostname != "" {
		// we do not overwrite the existing value
		return nil
	}

	ctx := context.Background()

	hc, err := google.DefaultClient(ctx)
	if err != nil {
		return fmt.Errorf("error getting a default http client: %v", err)
	}
	s, err := compute.New(hc)
	if err != nil {
		return fmt.Errorf("error getting a compute service: %v", err)
	}
	cs := compute.NewInstancesService(s)
	projectID, err := metadata.ProjectID()
	if err != nil {
		return fmt.Errorf("error getting projectID: %v", err)
	}
	zone, err := metadata.Zone()
	if err != nil {
		return fmt.Errorf("error getting zone: %v", err)
	}
	instance, err := metadata.InstanceName()
	if err != nil {
		return fmt.Errorf("error getting instance name: %v", err)
	}

	inst, err := cs.Get(projectID, zone, instance).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("error getting instance: %v", err)
	}
	items := inst.Metadata.Items
	items = append(items, &compute.MetadataItems{
		Key:   "camlistore-hostname",
		Value: googleapi.String(camliNetHostName),
	})
	mdata := &compute.Metadata{
		Items:       items,
		Fingerprint: inst.Metadata.Fingerprint,
	}

	call := cs.SetMetadata(projectID, zone, instance, mdata).Context(ctx)
	op, err := call.Do()
	if err != nil {
		if !googleapi.IsNotModified(err) {
			return fmt.Errorf("error setting instance hostname: %v", err)
		}
		return nil
	}
	opName := op.Name
	for {
		// TODO(mpl): add a timeout maybe?
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		time.Sleep(500 * time.Millisecond)
		op, err := s.ZoneOperations.Get(projectID, zone, opName).Do()
		if err != nil {
			return fmt.Errorf("failed to get op %s: %v", opName, err)
		}
		switch op.Status {
		case "PENDING", "RUNNING":
			continue
		case "DONE":
			if op.Error != nil {
				for _, operr := range op.Error.Errors {
					log.Printf("operation error: %+v", operr)
				}
				return fmt.Errorf("operation error")
			}
			log.Printf(`Successfully set "camlistore-hostname" to "%v" on instance`, camliNetHostName)
			return nil
		default:
			return fmt.Errorf("unknown operation status %q: %+v", op.Status, op)
		}
	}
	return nil
}

// requestHostName performs the GPG challenge to register/obtain a name in the
// camlistore.net domain. The acquired name should be "<gpgKeyId>.camlistore.net",
// where <gpgKeyId> is Camlistore's keyId.
// It also starts a goroutine that will rerun the challenge every hour, to keep
// the camlistore.net DNS server up to date.
func requestHostName(cl *gpgchallenge.Client) error {
	if err := cl.Challenge(camliNetDNS); err != nil {
		return err
	}

	if err := setInstanceHostname(); err != nil {
		return fmt.Errorf("error setting instance camlistore-hostname: %v", err)
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

// listenAndBaseURL finds the configured, default, or inferred listen address
// and base URL from the command-line flags and provided config.
func listenAndBaseURL(config *serverinit.Config) (listen, baseURL string) {
	baseURL = config.OptionalString("baseURL", "")
	listen = *flagListen
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

func redirectFromHTTP(base string) {
	http.ListenAndServe(":80", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, base, http.StatusFound)
	}))
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

// TODO(mpl): maybe export gce.writer, and reuse it here. Later.

// gclWriter is an io.Writer, where each Write writes a log entry to Google
// Cloud Logging.
type gclWriter struct {
	severity logging.Severity
	logger   *logging.Logger
}

func (w gclWriter) Write(p []byte) (n int, err error) {
	w.logger.Log(logging.Entry{
		Severity: w.severity,
		Payload:  string(p),
	})
	return len(p), nil
}

// if a non-nil logging Client is returned, it should be closed before the
// program terminates to flush any buffered log entries.
func maybeSetupGoogleCloudLogging() io.Closer {
	if flagGCEProjectID == "" && flagGCELogName == "" && flagGCEJWTFile == "" {
		return types.NopCloser
	}
	if flagGCEProjectID == "" || flagGCELogName == "" || flagGCEJWTFile == "" {
		exitf("All of --gce_project_id, --gce_log_name, and --gce_jwt_file must be specified for logging on Google Cloud Logging.")
	}
	ctx := context.Background()
	logc, err := logging.NewClient(ctx,
		flagGCEProjectID, option.WithServiceAccountFile(flagGCEJWTFile))
	if err != nil {
		exitf("Error creating GCL client: %v", err)
	}
	if err := logc.Ping(ctx); err != nil {
		exitf("Google logging client not ready (ping failed): %v", err)
	}
	logw := gclWriter{
		severity: logging.Debug,
		logger:   logc.Logger(flagGCELogName),
	}
	log.SetOutput(io.MultiWriter(os.Stderr, logw))
	return logc
}

// setupLoggingSyslog is non-nil on Unix. If it returns a non-nil io.Closer log
// flush function, setupLogging returns that flush function.
var setupLoggingSyslog func() io.Closer

// setupLogging sets up logging and returns an io.Closer that flushes logs.
func setupLogging() io.Closer {
	if *flagSyslog && runtime.GOOS == "windows" {
		exitf("-syslog not available on windows")
	}
	if fn := setupLoggingSyslog; fn != nil {
		if flusher := fn(); flusher != nil {
			return flusher
		}
	}
	if env.OnGCE() {
		lw, err := gce.LogWriter()
		if err != nil {
			log.Fatalf("Error setting up logging: %v", err)
		}
		log.SetOutput(lw)
		return lw
	}
	return maybeSetupGoogleCloudLogging()
}

func checkRecovery() {
	if *flagRecovery {
		blobpacked.SetRecovery()
		return
	}
	if !env.OnGCE() {
		return
	}
	recovery, err := metadata.InstanceAttributeValue("camlistore-recovery")
	if err != nil {
		if _, ok := err.(metadata.NotDefinedError); !ok {
			log.Printf("error getting camlistore-recovery: %v", err)
		}
		return
	}
	if recovery == "" {
		return
	}
	doRecovery, err := strconv.ParseBool(recovery)
	if err != nil {
		log.Printf("invalid bool value for \"camlistore-recovery\": %v", err)
	}
	if doRecovery {
		blobpacked.SetRecovery()
	}
}

// main wraps Main so tests (which generate their own func main) can still run Main.
func main() {
	Main(nil, nil)
}

// Main sends on up when it's running, and shuts down when it receives from down.
func Main(up chan<- struct{}, down <-chan struct{}) {
	flag.Parse()

	if *flagVersion {
		fmt.Fprintf(os.Stderr, "camlistored version: %s\nGo version: %s (%s/%s)\n",
			buildinfo.Version(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
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
	checkRecovery()

	// In case we're running in a Docker container with no
	// filesytem from which to load the root CAs, this
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

	log.Printf("Starting camlistored version %s; Go %s (%s/%s)", buildinfo.Version(), runtime.Version(),
		runtime.GOOS, runtime.GOARCH)

	shutdownc := make(chan io.Closer, 1) // receives io.Closer to cleanly shut down
	go handleSignals(shutdownc)

	config, isNewConfig, err := loadConfig(*flagConfigFile)
	if err != nil {
		exitf("Error loading config file: %v", err)
	}

	ws := webserver.New()
	baseURL, err := listen(ws, config)
	if err != nil {
		exitf("Error starting webserver: %v", err)
	}

	shutdownCloser, err := config.InstallHandlers(ws, baseURL, *flagReindex, nil)
	if err != nil {
		exitf("Error parsing config: %v", err)
	}
	shutdownc <- shutdownCloser

	challengeClient, err := muxChallengeHandler(ws, config)
	if err != nil {
		exitf("Error registering challenge client with Camlistore muxer: %v", err)
	}

	go ws.Serve()

	if challengeClient != nil {
		// TODO(mpl): we should technically wait for the above ws.Serve
		// to be ready, otherwise we're racy. Should we care?
		if err := requestHostName(challengeClient); err != nil {
			exitf("Could not register on camlistore.net: %v", err)
		}
	}

	urlToOpen := baseURL
	if !isNewConfig {
		// user may like to configure the server at the initial startup,
		// open UI if this is not the first run with a new config file.
		urlToOpen += config.UIPath
	}

	if *flagOpenBrowser {
		go osutil.OpenURL(urlToOpen)
	}

	if flagPollParent {
		osutil.DieOnParentDeath()
	}

	if err := config.UploadPublicKey(); err != nil {
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
	log.Printf("Available on %s", urlToOpen)

	if env.OnGCE() && strings.HasPrefix(baseURL, "https://") {
		go redirectFromHTTP(baseURL)
	}

	// Block forever, except during tests.
	up <- struct{}{}
	<-down
	osExit(0)
}
