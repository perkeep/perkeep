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

type Server struct {
	mux  *http.ServeMux
}

func New() *Server {
	return &Server{mux: http.NewServeMux()}
}

func (s *Server) HandleFunc(pattern string, fn func(http.ResponseWriter, *http.Request)) {
	s.mux.HandleFunc(pattern, fn)
}

func (s *Server) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

func (s *Server) Serve() {
	if os.Getenv("TESTING_PORT_WRITE_FD") == "" {  // Don't make noise during unit tests
		log.Printf("Starting to listen on http://%v/\n", *Listen)
	}

	listener, err := net.Listen("tcp", *Listen)
	if err != nil {
		log.Exitf("Failed to listen on %s: %v", *Listen, err)
	}
	go runTestHarnessIntegration(listener)
	err = http.Serve(listener, s.mux)
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
		log.Exitf("Bogus test harness fd '%s': %v", fdStr, err)
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
