package main

import (
	"auth"
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
		}
	}
	handler(conn, req)
}

// Signals the test harness that we've started listening.
// TODO: write back the port number that we randomly selected?
// For now just writes back a single byte.
func signalTestHarness(listener net.Listener) {
	log.Printf("Listening on %v", listener.Addr().String())
	fdStr := os.Getenv("TESTING_LISTENER_UP_WRITER_PIPE")
	if fdStr == "" {
		return
	}
	fd, err := strconv.Atoi(fdStr)
	if err != nil {
		log.Exitf("Bogus test harness fd '%s': %v", fdStr, err)
	}
	file := os.NewFile(fd, "signalpipe")
	file.Write([]byte(listener.Addr().String()))
	file.Write([]byte{'\n'})
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
	fmt.Printf("Starting to listen on http://%v/\n", *listen)

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Exitf("Failed to listen on %s: %v", *listen, err)
	}
	signalTestHarness(listener)
	err = http.Serve(listener, mux)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Error in http server: %v\n", err)
		os.Exit(1)
	}
}
