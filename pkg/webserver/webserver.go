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

// Package webserver implements a superset wrapper of http.Server.
//
// Among other things, it can throttle its connections, inherit its
// listening socket from a file descriptor in the environment, and
// log all activity.
package webserver

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/third_party/github.com/bradfitz/runsit/listen"

	"go4.org/net/throttle"
	"go4.org/wkfs"
	"golang.org/x/net/http2"
)

type Server struct {
	mux      *http.ServeMux
	listener net.Listener
	verbose  bool // log HTTP requests and response codes

	Logger *log.Logger // or nil.

	// H2Server is the HTTP/2 server config.
	H2Server http2.Server

	enableTLS               bool
	tlsCertFile, tlsKeyFile string

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

func (s *Server) printf(format string, v ...interface{}) {
	if s.Logger != nil {
		s.Logger.Printf(format, v...)
		return
	}
	log.Printf(format, v...)
}

func (s *Server) fatalf(format string, v ...interface{}) {
	if s.Logger != nil {
		s.Logger.Fatalf(format, v...)
		return
	}
	log.Fatalf(format, v...)
}

func (s *Server) SetTLS(certFile, keyFile string) {
	s.enableTLS = true
	s.tlsCertFile = certFile
	s.tlsKeyFile = keyFile
}

func (s *Server) ListenURL() string {
	scheme := "http"
	if s.enableTLS {
		scheme = "https"
	}
	if s.listener != nil {
		if taddr, ok := s.listener.Addr().(*net.TCPAddr); ok {
			if taddr.IP.IsUnspecified() {
				return fmt.Sprintf("%s://localhost:%d", scheme, taddr.Port)
			}
			return fmt.Sprintf("%s://%s", scheme, s.listener.Addr())
		}
	}
	return ""
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
func (s *Server) Listen(addr string) error {
	if s.listener != nil {
		return nil
	}

	doLog := os.Getenv("TESTING_PORT_WRITE_FD") == "" // Don't make noise during unit tests
	if addr == "" {
		return fmt.Errorf("<host>:<port> needs to be provided to start listening")
	}

	var err error
	s.listener, err = listen.Listen(addr)
	if err != nil {
		return fmt.Errorf("Failed to listen on %s: %v", addr, err)
	}
	base := s.ListenURL()
	if doLog {
		s.printf("Starting to listen on %s\n", base)
	}

	if s.enableTLS {
		config := &tls.Config{
			Rand:       rand.Reader,
			Time:       time.Now,
			NextProtos: []string{http2.NextProtoTLS, "http/1.1"},
		}
		config.Certificates = make([]tls.Certificate, 1)

		config.Certificates[0], err = loadX509KeyPair(s.tlsCertFile, s.tlsKeyFile)
		if err != nil {
			return fmt.Errorf("Failed to load TLS cert: %v", err)
		}
		s.listener = tls.NewListener(s.listener, config)
	}

	if doLog && strings.HasSuffix(base, ":0") {
		s.printf("Now listening on %s\n", s.ListenURL())
	}

	return nil
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
// TODO: write back the port number that we randomly selected?
// For now just writes back a single byte.
func runTestHarnessIntegration(listener net.Listener) {
	writePipe, err := pipeFromEnvFd("TESTING_PORT_WRITE_FD")
	if err != nil {
		return
	}
	readPipe, _ := pipeFromEnvFd("TESTING_CONTROL_READ_FD")

	if writePipe != nil {
		writePipe.Write([]byte(listener.Addr().String() + "\n"))
	}

	if readPipe != nil {
		bufr := bufio.NewReader(readPipe)
		for {
			line, err := bufr.ReadString('\n')
			if err == io.EOF || line == "EXIT\n" {
				os.Exit(0)
			}
			return
		}
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
