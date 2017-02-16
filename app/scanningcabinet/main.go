/*
Copyright 2017 The Camlistore Authors

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

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"

	"camlistore.org/pkg/app"
	"camlistore.org/pkg/buildinfo"
	"camlistore.org/pkg/webserver"
)

var (
	flagVersion = flag.Bool("version", false, "show version")
)

var (
	logger = log.New(os.Stderr, "SCANNING CABINET: ", log.LstdFlags)
	logf   = logger.Printf
)

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Fprintf(os.Stderr, "WARNING: THIS APP IS STILL EXPERIMENTAL, AND EVEN ITS DATA SCHEMA MIGHT CHANGE. DO NOT USE IN PRODUCTION.")
		fmt.Fprintf(os.Stderr, "scanningcabinet version: %s\nGo version: %s (%s/%s)\n",
			buildinfo.Version(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}

	logf("WARNING: THIS APP IS STILL EXPERIMENTAL, AND EVEN ITS DATA SCHEMA MIGHT CHANGE. DO NOT USE IN PRODUCTION.")
	logf("Starting scanning cabinet version %s; Go %s (%s/%s)", buildinfo.Version(), runtime.Version(),
		runtime.GOOS, runtime.GOARCH)

	listenAddr, err := app.ListenAddress()
	if err != nil {
		logger.Fatalf("Listen address: %v", err)
	}

	h, err := newHandler()
	if err != nil {
		logger.Fatalf("newHandler: %v", err)
	}

	ws := webserver.New()
	ws.Logger = logger
	ws.Handle("/", h)
	if h.httpsCert != "" {
		ws.SetTLS(webserver.TLSSetup{
			CertFile: h.httpsCert,
			KeyFile:  h.httpsKey,
		})
	}
	if err := ws.Listen(listenAddr); err != nil {
		logger.Fatalf("Listen: %v", err)
	}
	ws.Serve()
}
