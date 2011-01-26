// Copyright 2010 Brad Fitzpatrick <brad@danga.com>
//
// See LICENSE.

package main

import (
	"camli/auth"
	"camli/httputil"
	"camli/webserver"
	"camli/blobref"
	"flag"
	"fmt"
	"http"
	"log"
	"os"
)

var flagStorageRoot *string = flag.String("root", "/tmp/camliroot", "Root directory to store files")
var flagRequestLog *bool = flag.Bool("reqlog", false, "Log incoming requests")
var stealthMode *bool = flag.Bool("stealth", true, "Run in stealth mode.")

var blobFetcher blobref.Fetcher

func handleCamli(conn http.ResponseWriter, req *http.Request) {
	handler := func (conn http.ResponseWriter, req *http.Request) {
		httputil.BadRequestError(conn,
			fmt.Sprintf("Unsupported path (%s) or method (%s).",
			req.URL.Path, req.Method))
	}
	if *flagRequestLog {
		log.Printf("%s %s", req.Method, req.RawURL)
	}
	switch req.Method {
	case "GET":
		switch req.URL.Path {
		case "/camli/enumerate-blobs":
			handler = auth.RequireAuth(handleEnumerateBlobs)
		default:
			handler = auth.RequireAuth(createGetHandler(blobFetcher))
		}
	case "POST":
		switch req.URL.Path {
		case "/camli/preupload":
			handler = auth.RequireAuth(handlePreUpload)
		case "/camli/upload":
			handler = auth.RequireAuth(handleMultiPartUpload)
		case "/camli/testform": // debug only
			handler = handleTestForm
		case "/camli/form": // debug only
			handler = handleCamliForm
		}
	case "PUT": // no longer part of spec
		handler = auth.RequireAuth(handlePut)
	}
	handler(conn, req)
}

func handleRoot(conn http.ResponseWriter, req *http.Request) {
	if *stealthMode {
		fmt.Fprintf(conn, "Hi.\n")
	} else {
		fmt.Fprintf(conn, "This is camlistored, a Camlistore storage daemon.\n");
		fmt.Fprintf(conn, "<p><a href=/js>js interface</a>");
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


	{
		fi, err := os.Stat(*flagStorageRoot)
		if err != nil || !fi.IsDirectory() {
			fmt.Fprintf(os.Stderr,
				"Storage root '%s' doesn't exist or is not a directory.\n",
				*flagStorageRoot)
			os.Exit(1)
		}
	}

	blobFetcher = newDiskStorage(*flagStorageRoot)

	ws := webserver.New()
	ws.HandleFunc("/", handleRoot)
	ws.HandleFunc("/camli/", handleCamli)
	ws.Handle("/js/", http.FileServer("../../clients/js", "/js/"))
	ws.Serve()
}
