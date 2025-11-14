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

// Package webserver implements a superset wrapper of http.Server.
//
// Among other things, it can throttle its connections, inherit its
// listening socket from a file descriptor in the environment, and
// log all activity.
package webserver // import "perkeep.org/pkg/webserver"

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go4.org/net/throttle"
	"go4.org/wkfs"
	"golang.org/x/net/http2"
	"perkeep.org/pkg/webserver/listen"
	"tailscale.com/tsnet"
	"tailscale.com/util/ctxkey"
)

const alpnProto = "acme-tls/1" // from golang.org/x/crypto/acme.ALPNProto

// TailscaleCtxKey is the context key for whether the server is running in Tailscale tsnet mode.
var TailscaleCtxKey = ctxkey.New("tailscale", false)

type Server struct {
	mux       *http.ServeMux
	listener  net.Listener
	listenURL string // optional forced value for ListenURL, if set (used by Tailscale)
	verbose   bool   // log HTTP requests and response codes

	Logger *log.Logger // or nil.

	// H2Server is the HTTP/2 server config.
	H2Server http2.Server

	// enableTLS sets the Server up for listening to HTTPS connections.
	enableTLS bool
	// tlsCertFile (tlsKeyFile) is the path to the HTTPS certificate (key) file.
	tlsCertFile, tlsKeyFile string
	// certManager is set as GetCertificate in the tls.Config of the listener. But tlsCertFile takes precedence.
	certManager func(*tls.ClientHelloInfo) (*tls.Certificate, error)

	// tsnetServer is non-nil when running in Tailscale tsnet mode.
	tsnetServer *tsnet.Server

	mu   sync.Mutex
	reqs int64
}

func New() *Server {
	verbose, _ := strconv.ParseBool(os.Getenv("CAMLI_HTTP_DEBUG"))
	return &Server{
		mux:     http.NewServeMux(),
		verbose: verbose,
	}
}

func (s *Server) printf(format string, v ...any) {
	if s.Logger != nil {
		s.Logger.Printf(format, v...)
		return
	}
	log.Printf(format, v...)
}

func (s *Server) fatalf(format string, v ...any) {
	if s.Logger != nil {
		s.Logger.Fatalf(format, v...)
		return
	}
	log.Fatalf(format, v...)
}

// TLSSetup specifies how the server gets its TLS certificate.
type TLSSetup struct {
	// Certfile is the path to the TLS certificate file. It takes precedence over CertManager.
	CertFile string
	// KeyFile is the path to the TLS key file.
	KeyFile string
	// CertManager is the tls.GetCertificate of the tls Config. But CertFile takes precedence.
	CertManager func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error)
}

func (s *Server) SetTLS(setup TLSSetup) {
	s.enableTLS = true
	s.certManager = setup.CertManager
	s.tlsCertFile = setup.CertFile
	s.tlsKeyFile = setup.KeyFile
}

// ListenURL returns the base URL of the server, including its scheme and
// authority, but without a trailing slash or any path.
func (s *Server) ListenURL() string {
	if s.listenURL != "" {
		return s.listenURL
	}
	if s.listener == nil {
		return ""
	}
	taddr, ok := s.listener.Addr().(*net.TCPAddr)
	if !ok {
		return ""
	}
	scheme := "http"
	if s.enableTLS {
		scheme = "https"
	}
	if taddr.IP.IsUnspecified() {
		return fmt.Sprintf("%s://localhost:%d", scheme, taddr.Port)
	}
	return fmt.Sprintf("%s://%s", scheme, s.listener.Addr())
}

func (s *Server) HandleFunc(pattern string, fn func(http.ResponseWriter, *http.Request)) {
	s.mux.HandleFunc(pattern, fn)
}

func (s *Server) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

func (s *Server) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	var n int64
	if s.verbose {
		s.mu.Lock()
		s.reqs++
		n = s.reqs
		s.mu.Unlock()
		s.printf("Request #%d: %s %s (from %s) ...", n, req.Method, req.RequestURI, req.RemoteAddr)
		rw = &trackResponseWriter{ResponseWriter: rw}
	}
	s.mux.ServeHTTP(rw, req)
	if s.verbose {
		tw := rw.(*trackResponseWriter)
		s.printf("Request #%d: %s %s = code %d, %d bytes", n, req.Method, req.RequestURI, tw.code, tw.resSize)
	}
}

type trackResponseWriter struct {
	http.ResponseWriter
	code    int
	resSize int64
}

func (tw *trackResponseWriter) WriteHeader(code int) {
	tw.code = code
	tw.ResponseWriter.WriteHeader(code)
}

func (tw *trackResponseWriter) Write(p []byte) (int, error) {
	if tw.code == 0 {
		tw.code = 200
	}
	tw.resSize += int64(len(p))
	return tw.ResponseWriter.Write(p)
}

// Listen starts listening on the given host:port addr.
//
// If the "host" part is "tailscale", it goes into Tailscale tsnet mode, and the
// "port" is instead an optional state directory path or a bare name for the
// instance name.
func (s *Server) Listen(addr string) error {
	if s.listener != nil {
		return nil
	}

	if addr == "" {
		return fmt.Errorf("<host>:<port> needs to be provided to start listening")
	}

	preColon, _, _ := strings.Cut(addr, ":")
	isTailscale := preColon == "tailscale"

	var err error
	if isTailscale {
		s.listener, err = s.listenTailscale(addr, s.enableTLS)
	} else {
		s.listener, err = listen.Listen(addr)
	}
	if err != nil {
		return fmt.Errorf("Failed to listen on %s: %v", addr, err)
	}
	base := s.ListenURL()
	s.printf("Starting to listen on %s\n", base)

	if s.enableTLS {
		if s.tsnetServer != nil {
			lc, err := s.tsnetServer.LocalClient()
			if err != nil {
				return err
			}
			s.SetTLS(TLSSetup{
				CertManager: lc.GetCertificate,
			})
		}
		doEnableTLS := func() error {
			config := &tls.Config{
				Rand:       rand.Reader,
				Time:       time.Now,
				NextProtos: []string{http2.NextProtoTLS, "http/1.1"},
				MinVersion: tls.VersionTLS12,
			}
			if s.tlsCertFile == "" && s.certManager != nil {
				config.GetCertificate = s.certManager
				// TODO(mpl): see if we can instead use
				// https://godoc.org/golang.org/x/crypto/acme/autocert#Manager.TLSConfig
				config.NextProtos = append(config.NextProtos, alpnProto)
				s.listener = tls.NewListener(s.listener, config)
				return nil
			}

			config.Certificates = make([]tls.Certificate, 1)
			config.Certificates[0], err = loadX509KeyPair(s.tlsCertFile, s.tlsKeyFile)
			if err != nil {
				return fmt.Errorf("Failed to load TLS cert: %v", err)
			}
			s.listener = tls.NewListener(s.listener, config)
			return nil
		}
		if err := doEnableTLS(); err != nil {
			return err
		}
	}

	return nil
}

func (s *Server) listenTailscale(addr string, withTLS bool) (net.Listener, error) {
	preColon, postColon, _ := strings.Cut(addr, ":")
	if preColon != "tailscale" {
		panic("caller error")
	}
	var dir string
	name := "perkeep"
	if postColon != "" {
		// Make sure they didn't think it was a port number.
		if _, err := strconv.Atoi(postColon); err == nil {
			return nil, fmt.Errorf("invalid %q Tailscale listen address; the part after the colon should be a name or directory, not a port number", addr)
		}
		if strings.Contains(postColon, string(os.PathSeparator)) {
			dir = postColon
		} else {
			name = postColon
		}
	}
	if dir == "" {
		confDir, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("failed to find user config dir: %v", err)
		}
		dir = filepath.Join(confDir, "tsnet-"+name)
	}
	if fi, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("error creating Tailscale state directory: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("error checking Tailscale state directory: %w", err)
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("Tailscale state directory %q (from listen arg %q) is not a directory", dir, addr)
	}
	ts := &tsnet.Server{
		Dir:      dir, // or empty for automatic
		Hostname: name,
	}
	s.printf("Tailscale tsnet starting for name %q in directory %q ...", name, dir)
	if err := ts.Start(); err != nil {
		return nil, err
	}
	s.printf("Tailscale started; waiting Up...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	st, err := ts.Up(ctx)
	if err != nil {
		return nil, err
	}
	dnsName := strings.TrimSuffix(st.Self.DNSName, ".")
	s.printf("Tailscale up; state=%v, self=%v (%q)", st.BackendState, dnsName, st.Self.TailscaleIPs)
	if withTLS {
		if len(st.CertDomains) == 0 {
			return nil, fmt.Errorf("HTTPS is not enabled for Tailnet %q", st.CurrentTailnet.Name)
		}
	}
	s.tsnetServer = ts
	if withTLS {
		s.listenURL = "https://" + dnsName
		return ts.Listen("tcp", ":443")
	}
	s.listenURL = "http://" + dnsName
	return ts.Listen("tcp", ":80")
}

func (s *Server) throttleListener() net.Listener {
	kBps, _ := strconv.Atoi(os.Getenv("DEV_THROTTLE_KBPS"))
	ms, _ := strconv.Atoi(os.Getenv("DEV_THROTTLE_LATENCY_MS"))
	if kBps == 0 && ms == 0 {
		return s.listener
	}
	rate := throttle.Rate{
		KBps:    kBps,
		Latency: time.Duration(ms) * time.Millisecond,
	}
	return &throttle.Listener{
		Listener: s.listener,
		Down:     rate,
		Up:       rate, // TODO: separate rates?
	}
}

func (s *Server) Serve() {
	if err := s.Listen(""); err != nil {
		s.fatalf("Listen error: %v", err)
	}
	go runTestHarnessIntegration(s.listener)

	srv := &http.Server{
		Handler: s,
		BaseContext: func(ln net.Listener) context.Context {
			ctx := context.Background()
			ctx = TailscaleCtxKey.WithValue(ctx, s.tsnetServer != nil)
			return ctx
		},
	}
	// TODO: allow configuring src.ErrorLog (and plumb through to
	// Google Cloud Logging when run on GCE, eventually)

	// Setup the NPN NextProto map for HTTP/2 support:
	http2.ConfigureServer(srv, &s.H2Server)

	err := srv.Serve(s.throttleListener())
	if err != nil {
		s.printf("Error in http server: %v\n", err)
		os.Exit(1)
	}
}

// Signals the test harness that we've started listening.
// Writes back the address that we randomly selected.
func runTestHarnessIntegration(listener net.Listener) {
	addr := os.Getenv("CAMLI_SET_BASE_URL_AND_SEND_ADDR_TO")
	if addr == "" {
		return
	}
	c, err := net.Dial("tcp", addr)
	if err == nil {
		fmt.Fprintf(c, "%s\n", listener.Addr())
		c.Close()
	}
}

// loadX509KeyPair is a copy of tls.LoadX509KeyPair but using wkfs.
func loadX509KeyPair(certFile, keyFile string) (cert tls.Certificate, err error) {
	certPEMBlock, err := wkfs.ReadFile(certFile)
	if err != nil {
		return
	}
	keyPEMBlock, err := wkfs.ReadFile(keyFile)
	if err != nil {
		return
	}
	return tls.X509KeyPair(certPEMBlock, keyPEMBlock)
}
