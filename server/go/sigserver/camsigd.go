package main

import (
	"camli/auth"
	"camli/blobref"
	"camli/httputil"
	"camli/webserver"
	"flag"
	"fmt"
	"http"
	"os"
)

var accessPassword string

var flagPubKeyDir *string = flag.String("pubkey-dir", "test/pubkey-blobs",
	"Temporary development hack; directory to dig-xxxx.camli public keys.")

// TODO: for now, the only implementation of the blobref.Fetcher
// interface for fetching public keys is the "local, from disk"
// implementation used for testing.  In reality we'd want to be able
// to fetch these from blobservers.
var pubKeyFetcher = blobref.NewSimpleDirectoryFetcher(*flagPubKeyDir)

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
