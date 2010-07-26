// Copyright 2010 Brad Fitzpatrick <brad@danga.com>
//
// See LICENSE.

package main

import (
	"flag"
	"fmt"
	"http"
	"os"
)

var listen *string = flag.String("listen", "0.0.0.0:3179", "host:port to listen on")
var flagStorageRoot *string = flag.String("root", "/tmp/camliroot", "Root directory to store files")
var stealthMode *bool = flag.Bool("stealth", true, "Run in stealth mode.")

func handleCamli(conn *http.Conn, req *http.Request) {
	handler := func (conn *http.Conn, req *http.Request) {
		badRequestError(conn, "Unsupported path or method.")
	}
	switch req.Method {
	case "GET":
		switch req.URL.Path {
		case "/camli/enumerate-blobs":
			handler = requireAuth(handleEnumerateBlobs)
		default:
			handler = requireAuth(handleGet)
		}
	case "POST":
		switch req.URL.Path {
		case "/camli/preupload":
			handler = requireAuth(handlePreUpload)
		case "/camli/upload":
			handler = requireAuth(handleMultiPartUpload)
		case "/camli/testform": // debug only
			handler = handleTestForm
		case "/camli/form": // debug only
			handler = handleCamliForm
		}
	case "PUT": // no longer part of spec
		handler = requireAuth(handlePut)
	}
	handler(conn, req)
}

func handleRoot(conn *http.Conn, req *http.Request) {
	if *stealthMode {
		fmt.Fprintf(conn, "Hi.\n")
	} else {
		fmt.Fprintf(conn, "This is camlistored, a Camlistore storage daemon.\n");
	}
}

func main() {
	flag.Parse()

	accessPassword = os.Getenv("CAMLI_PASSWORD")
	if len(accessPassword) == 0 {
		fmt.Fprintf(os.Stderr,
			"No CAMLI_PASSWORD environment variable set.\n")
		os.Exit(1)
	}

	{
		fi, err := os.Stat(*flagStorageRoot)
		if err != nil || !fi.IsDirectory() {
			fmt.Fprintf(os.Stderr,
				"Storage root '%s' doesn't exist or is not a directory.\n",
				*flagStorageRoot)
			os.Exit(1)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/camli/", handleCamli)

	fmt.Printf("Starting to listen on http://%v/\n", *listen)
	err := http.ListenAndServe(*listen, mux)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Error in http server: %v\n", err)
		os.Exit(1)
	}
}
