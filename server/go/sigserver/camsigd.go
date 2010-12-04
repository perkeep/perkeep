package main

import (
	"bufio"
	"camli/auth"
	"camli/http_util"
	"flag"
	"fmt"
	"http"
	"log"
	"net"
	"os"
	"strconv"
)

var gpgPath *string = flag.String("gpg-path", "/usr/bin/gpg", "Path to the gpg binary.")

var flagRing *string = flag.String("keyring", "./test/test-keyring.gpg",
	"GnuPG public keyring file to use.")

var flagSecretRing *string = flag.String("secret-keyring", "./test/test-secring.gpg",
	"GnuPG secret keyring file to use.")

var listen *string = flag.String("listen", "0.0.0.0:2856", "host:port to listen on")

var accessPassword string

func handleRoot(conn http.ResponseWriter, req *http.Request) {
	fmt.Fprintf(conn, "camsigd")
}

func handleCamliSig(conn http.ResponseWriter, req *http.Request) {
	handler := func (conn http.ResponseWriter, req *http.Request) {
		http_util.BadRequestError(conn, "Unsupported path or method.")
	}

	switch req.Method {
	case "POST":
		switch req.URL.Path {
		case "/camli/sig/sign":
			handler = auth.RequireAuth(handleSign)
		case "/camli/sig/verify":
			handler = handleVerify
		}
	}
	handler(conn, req)
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

func main() {
	flag.Parse()

	auth.AccessPassword = os.Getenv("CAMLI_PASSWORD")
	if len(auth.AccessPassword) == 0 {
		fmt.Fprintf(os.Stderr,
			"No CAMLI_PASSWORD environment variable set.\n")
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/camli/sig/", handleCamliSig)

	if *listen != ":0" {  // be quiet for unit tests
		log.Printf("Starting to listen on http://%v/\n", *listen)
	}

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Exitf("Failed to listen on %s: %v", *listen, err)
	}
	go runTestHarnessIntegration(listener)
	err = http.Serve(listener, mux)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Error in http server: %v\n", err)
		os.Exit(1)
	}
}
