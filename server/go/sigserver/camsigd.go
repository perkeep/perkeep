package main

import (
	"camli/auth"
	"camli/httputil"
	"camli/webserver"
	"flag"
	"fmt"
	"http"
	"os"
)

var gpgPath *string = flag.String("gpg-path", "/usr/bin/gpg", "Path to the gpg binary.")

var flagRing *string = flag.String("keyring", "./test/test-keyring.gpg",
	"GnuPG public keyring file to use.")

var flagSecretRing *string = flag.String("secret-keyring", "./test/test-secring.gpg",
	"GnuPG secret keyring file to use.")

var accessPassword string

func handleRoot(conn http.ResponseWriter, req *http.Request) {
	fmt.Fprintf(conn, "camsigd")
}

func handleCamliSig(conn http.ResponseWriter, req *http.Request) {
	handler := func (conn http.ResponseWriter, req *http.Request) {
		httputil.BadRequestError(conn, "Unsupported path or method.")
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

func main() {
	flag.Parse()

	auth.AccessPassword = os.Getenv("CAMLI_PASSWORD")
	if len(auth.AccessPassword) == 0 {
		fmt.Fprintf(os.Stderr,
			"No CAMLI_PASSWORD environment variable set.\n")
		os.Exit(1)
	}

	ws := webserver.New()
	ws.HandleFunc("/", handleRoot)
	ws.HandleFunc("/camli/sig/", handleCamliSig)
	ws.Serve()
}
