/*
Copyright 2014 The Camlistore Authors.

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

// The hello application serves as an example on how to make stand-alone
// server applications, interacting with a Camlistore server.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"

	"camlistore.org/pkg/app"
	"camlistore.org/pkg/buildinfo"
	"camlistore.org/pkg/webserver"
)

var (
	flagVersion = flag.Bool("version", false, "show version")
)

// config is used to unmarshal the application configuration JSON
// that we get from Camlistore when we request it at $CAMLI_APP_CONFIG_URL.
type config struct {
	Word string `json:"word,omitempty"` // Argument printed after "Hello " in the helloHandler response.
}

func appConfig() *config {
	configURL := os.Getenv("CAMLI_APP_CONFIG_URL")
	if configURL == "" {
		log.Fatalf("Hello application needs a CAMLI_APP_CONFIG_URL env var")
	}
	cl, err := app.Client()
	if err != nil {
		log.Fatalf("could not get a client to fetch extra config: %v", err)
	}
	conf := &config{}
	if err := cl.GetJSON(configURL, conf); err != nil {
		log.Fatalf("could not get app config at %v: %v", configURL, err)
	}
	return conf
}

type helloHandler struct {
	who string // who to say hello to.
}

func (h *helloHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	rw.WriteHeader(200)
	fmt.Fprintf(rw, "Hello %s\n", h.who)
}

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Fprintf(os.Stderr, "hello version: %s\nGo version: %s (%s/%s)\n",
			buildinfo.Version(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}

	log.Printf("Starting hello version %s; Go %s (%s/%s)", buildinfo.Version(), runtime.Version(),
		runtime.GOOS, runtime.GOARCH)

	listenAddr, err := app.ListenAddress()
	if err != nil {
		log.Fatalf("Listen address: %v", err)
	}
	conf := appConfig()
	ws := webserver.New()
	ws.Handle("/", &helloHandler{who: conf.Word})
	// TODO(mpl): handle status requests too. Camlistore will send an auth
	// token in the extra config that should be used as the "password" for
	// subsequent status requests.
	if err := ws.Listen(listenAddr); err != nil {
		log.Fatalf("Listen: %v", err)
	}

	ws.Serve()
}
