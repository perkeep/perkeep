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
	"flag"
	"http"
	"log"
	"net"
	"os"
	"strconv"
)

var Listen *string = flag.String("listen", "0.0.0.0:2856", "host:port to listen on, or :0 to auto-select")

type HandlerPicker func(req *http.Request) (http.HandlerFunc, bool)

type Server struct {
	premux []HandlerPicker
	mux  *http.ServeMux
}

func New() *Server {
	return &Server{
	premux: make([]HandlerPicker, 0),
	mux: http.NewServeMux(),
	}
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

func (s *Server) Serve() {
	if os.Getenv("TESTING_PORT_WRITE_FD") == "" {  // Don't make noise during unit tests
		log.Printf("Starting to listen on http://%v/\n", *Listen)
	}

	listener, err := net.Listen("tcp", *Listen)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", *Listen, err)
	}
	go runTestHarnessIntegration(listener)
	err = http.Serve(listener, s)
	if err != nil {
		log.Printf("Error in http server: %v\n", err)
		os.Exit(1)
	}
}


func pipeFromEnvFd(env string) *os.File {
	fdStr := os.Getenv(env)
	if fdStr == "" {
		return nil
	}
	fd, err := strconv.Atoi(fdStr)
	if err != nil {
		log.Fatalf("Bogus test harness fd '%s': %v", fdStr, err)
	}
	return os.NewFile(fd, "testingpipe-" + env)
}

// Signals the test harness that we've started listening.
// TODO: write back the port number that we randomly selected?
// For now just writes back a single byte.
func runTestHarnessIntegration(listener net.Listener) {
	writePipe := pipeFromEnvFd("TESTING_PORT_WRITE_FD")
	readPipe := pipeFromEnvFd("TESTING_CONTROL_READ_FD")

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
