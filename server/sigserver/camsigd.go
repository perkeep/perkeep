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

// The sigserver is a stand-alone JSON signing and verification server.
//
// TODO(bradfitz): as of 2012-01-10 this is very old and superceded by
// the general server and pkg/serverconfig.  We should just make it
// possible to configure a signing-only server with
// serverconfig/genconfig.go.  I think we basically already can. Then
// we can delete this.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/webserver"
)

var accessPassword string

var flagPubKeyDir = flag.String("pubkey-dir", "test/pubkey-blobs",
	"Temporary development hack; directory to dig-xxxx.camli public keys.")

// TODO: for now, the only implementation of the blobref.Fetcher
// interface for fetching public keys is the "local, from disk"
// implementation used for testing.  In reality we'd want to be able
// to fetch these from blobservers.
var pubKeyFetcher = blob.NewSimpleDirectoryFetcher(*flagPubKeyDir)

func handleRoot(conn http.ResponseWriter, req *http.Request) {
	fmt.Fprintf(conn, "camsigd")
}

func handleCamliSig(conn http.ResponseWriter, req *http.Request) {
	handler := func(conn http.ResponseWriter, req *http.Request) {
		httputil.BadRequestError(conn, "Unsupported path or method.")
	}

	switch req.Method {
	case "POST":
		switch req.URL.Path {
		case "/camli/sig/sign":
			handler = auth.RequireAuth(handleSign, auth.OpSign)
		case "/camli/sig/verify":
			handler = handleVerify
		}
	}
	handler(conn, req)
}

func main() {
	flag.Parse()

	mode, err := auth.FromEnv()
	if err != nil {
		log.Fatal(err)
	}
	auth.SetMode(mode)

	ws := webserver.New()
	ws.HandleFunc("/", handleRoot)
	ws.HandleFunc("/camli/sig/", handleCamliSig)
	ws.Serve()
}
