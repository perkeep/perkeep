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

package webserver

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"flag"
	"http"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

var Listen = flag.String("listen", "0.0.0.0:2856", "host:port to listen on, or :0 to auto-select")

type HandlerPicker func(req *http.Request) (http.HandlerFunc, bool)

type Server struct {
	premux   []HandlerPicker
	mux      *http.ServeMux
	listener net.Listener

	enableTLS               bool
	tlsCertFile, tlsKeyFile string
}

func New() *Server {
	return &Server{
		premux: make([]HandlerPicker, 0),
		mux:    http.NewServeMux(),
	}
}

func (s *Server) SetTLS(certFile, keyFile string) {
	s.enableTLS = true
	s.tlsCertFile = certFile
	s.tlsKeyFile = keyFile
}

func (s *Server) BaseURL() string {
	scheme := "http"
	if s.enableTLS {
		scheme = "https"
	}
	if s.listener != nil {
		return scheme + "://" + s.listener.Addr().String()
	}
	if strings.HasPrefix(*Listen, ":") {
		return scheme + "://localhost" + *Listen
	}
	return scheme + "://" + strings.Replace(*Listen, "0.0.0.0:", "localhost:", 1)
}

// Register conditional handler-picker functions which get run before
// HandleFunc or Handle.  The HandlerPicker should return false if
// it's not interested in a request.
func (s *Server) RegisterPreMux(hp HandlerPicker) {
	s.premux = append(s.premux, hp)
}

func (s *Server) HandleFunc(pattern string, fn func(http.ResponseWriter, *http.Request)) {
	s.mux.HandleFunc(pattern, fn)
}

func (s *Server) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

func (s *Server) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	for _, hp := range s.premux {
		handler, ok := hp(req)
		if ok {
			handler(rw, req)
			return
		}
	}
	s.mux.ServeHTTP(rw, req)
}

func (s *Server) Listen() os.Error {
	if s.listener != nil {
		return nil
	}

	doLog := os.Getenv("TESTING_PORT_WRITE_FD") == "" // Don't make noise during unit tests
	base := s.BaseURL()
	if doLog {
		log.Printf("Starting to listen on %s\n", base)
	}

	var err os.Error
	s.listener, err = net.Listen("tcp", *Listen)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", *Listen, err)
	}

	if s.enableTLS {
		config := &tls.Config{
			Rand:       rand.Reader,
			Time:       time.Seconds,
			NextProtos: []string{"http/1.1"},
		}
		config.Certificates = make([]tls.Certificate, 1)
		config.Certificates[0], err = tls.LoadX509KeyPair(s.tlsCertFile, s.tlsKeyFile)
		if err != nil {
			log.Fatalf("Failed to load TLS cert: %v", err)
		}
		s.listener = tls.NewListener(s.listener, config)
	}

	if doLog && strings.HasSuffix(base, ":0") {
		log.Printf("Now listening on %s\n", s.BaseURL())
	}

	return nil
}

func (s *Server) Serve() {
	if err := s.Listen(); err != nil {
		log.Fatalf("Listen error: %v", err)
	}
	go runTestHarnessIntegration(s.listener)
	err := http.Serve(s.listener, s)
	if err != nil {
		log.Printf("Error in http server: %v\n", err)
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
			if err == os.EOF || line == "EXIT\n" {
				os.Exit(0)
			}
			return
		}
	}
}
