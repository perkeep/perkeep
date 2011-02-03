// Copyright 2010 Brad Fitzpatrick <brad@danga.com>
//
// See LICENSE.

package main

import (
	"camli/auth"
	"camli/httputil"
	"camli/webserver"
	"camli/blobserver"
	"camli/blobserver/handlers"
	"flag"
	"fmt"
	"http"
	"log"
	"strings"
	"os"
)

var flagStorageRoot *string = flag.String("root", "/tmp/camliroot", "Root directory to store files")
var flagRequestLog *bool = flag.Bool("reqlog", false, "Log incoming requests")

var storage blobserver.Storage

const camliPrefix = "/camli/"
const partitionPrefix = "/partition-"

var InvalidCamliPath = os.NewError("Invalid Camlistore request path")

func parseCamliPath(path string) (partition, action string, err os.Error) {
	camIdx := strings.Index(path, camliPrefix)
	if camIdx == -1 {
		err = InvalidCamliPath
		return
	}
	action = path[camIdx+len(camliPrefix):]
	if camIdx == 0 {
		return
	}
	if !strings.HasPrefix(path, partitionPrefix) {
		err = InvalidCamliPath
		return
	}
	partition = path[len(partitionPrefix):camIdx]
	if !isValidPartitionName(partition) {
		err = InvalidCamliPath
		return
	}
	return
}

func pickPartitionHandlerMaybe(req *http.Request) (handler http.HandlerFunc, intercept bool) {
	if !strings.HasPrefix(req.URL.Path, partitionPrefix) {
		intercept = false
		return
	}
	return http.HandlerFunc(handleCamli), true
}

func unsupportedHandler(conn http.ResponseWriter, req *http.Request) {
        httputil.BadRequestError(conn, "Unsupported camlistore path or method.")
}

func handleCamli(conn http.ResponseWriter, req *http.Request) {
	partition, action, err := parseCamliPath(req.URL.Path)
	if err != nil {
		log.Printf("Invalid request for method %q, path %q", 
			req.Method, req.URL.Path)
		unsupportedHandler(conn, req)
		return
	}

	handler := unsupportedHandler
	if *flagRequestLog {
		log.Printf("method %q; partition %q; action %q", req.Method, partition, action)
	}
	switch req.Method {
	case "GET":
		switch action {
		case "enumerate-blobs":
			handler = auth.RequireAuth(createEnumerateHandler(storage, partition))
		default:
			handler = createGetHandler(storage)
		}
	case "POST":
		switch action {
		case "preupload":
			handler = auth.RequireAuth(handlePreUpload)
		case "upload":
			handler = auth.RequireAuth(handleMultiPartUpload)
		case "remove":
			// Currently only allows removing from a non-main partition.
			handler = auth.RequireAuth(handlers.CreateRemoveHandler(storage, partition))

	        // Not part of the spec:
		case "testform": // debug only
			handler = handleTestForm
		case "form": // debug only
			handler = handleCamliForm
		}
	case "PUT": // no longer part of spec
		handler = auth.RequireAuth(handlePut)
	}
	handler(conn, req)
}

func handleRoot(conn http.ResponseWriter, req *http.Request) {
	fmt.Fprintf(conn, "This is camlistored, a Camlistore storage daemon.\n")
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

	storage = newDiskStorage(*flagStorageRoot)

	ws := webserver.New()
	ws.RegisterPreMux(webserver.HandlerPicker(pickPartitionHandlerMaybe))
	ws.HandleFunc("/", handleRoot)
	ws.HandleFunc("/camli/", handleCamli)
	ws.Handle("/js/", http.FileServer("../../clients/js", "/js/"))
	ws.Serve()
}
