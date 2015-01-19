/*
Copyright 2015 The Camlistore Authors

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

// This program is a wrapper around the gce deploy handler, to help with debugging it.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"camlistore.org/pkg/deploy/gce"
	"camlistore.org/pkg/httputil"
)

var (
	host    = flag.String("host", "localhost:8080", "listening port and hostname")
	debug   = flag.Bool("debug", false, "various tweaks to help with debugging. Do not actually create an instance.")
	cert    = flag.String("cert", "", "HTTS certificate")
	key     = flag.String("key", "", "HTTS key")
	logfile = flag.String("logfile", "", "where to log. otherwise goes to stderr")
	piggy   = flag.String("piggy", "", "path to the piggy gif for the progress animation")
)

func main() {
	flag.Parse()

	gce.DevHandler = *debug
	gceh, err := gce.NewDeployHandler(*host, "/launch/")
	if err != nil {
		log.Fatal(err)
	}
	if *logfile != "" {
		f, err := os.Create(*logfile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close() // lazy. no matter, just an example.
		gceh.(*gce.DeployHandler).SetLogger(log.New(f, "GCEDEPLOYER", log.LstdFlags))
	}

	http.HandleFunc("/static/piggy.gif", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, *piggy)
	})
	http.Handle("/launch/", &httputil.PrefixHandler{"/launch/", gceh})
	if *debug {
		if err := http.ListenAndServe(*host, nil); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := http.ListenAndServeTLS(*host, *cert, *key, nil); err != nil {
		log.Fatal(err)
	}
}
