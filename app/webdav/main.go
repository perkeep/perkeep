/*
Copyright 2025 The Perkeep Authors.

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

// The webdav application provides a WebDAV server for accessing Perkeep content.
// It is designed for use with mobile photo sync apps like PhotoSync.
package main // import "perkeep.org/app/webdav"

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/app"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/buildinfo"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/webserver"

	"golang.org/x/net/webdav"
)

var (
	flagVersion = flag.Bool("version", false, "show version")
)

// config is used to unmarshal the application configuration JSON
// that we get from Perkeep when we request it at $CAMLI_APP_CONFIG_URL.
type config struct {
	// RootName is the value of the camliRoot attribute to search for.
	// The app will find the permanode with camliRoot=<RootName>.
	// In dev mode, this permanode is auto-created by the server.
	RootName string `json:"camliRoot,omitempty"`
}

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Fprintf(os.Stderr, "webdav version: %s\nGo version: %s (%s/%s)\n",
			buildinfo.Summary(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}

	log.Printf("Starting webdav version %s; Go %s (%s/%s)", buildinfo.Summary(), runtime.Version(),
		runtime.GOOS, runtime.GOARCH)

	// Exit when parent process (perkeepd) dies.
	osutil.DieOnParentDeath()

	listenAddr, err := app.ListenAddress()
	if err != nil {
		log.Fatalf("Listen address: %v", err)
	}

	cl, err := app.Client()
	if err != nil {
		log.Fatalf("Could not get Perkeep client: %v", err)
	}

	server, err := app.APIHost()
	if err != nil {
		log.Fatalf("Could not get API host: %v", err)
	}
	signer := app.NewSigner(cl, server)

	conf := loadConfig(cl)
	rootRef, err := findRootPermanode(context.Background(), cl, conf)
	if err != nil {
		log.Fatalf("Could not find root permanode: %v", err)
	}
	log.Printf("Using root permanode: %v", rootRef)

	fs := newPerkeepFS(cl, signer, rootRef)

	// Get the route prefix (e.g., "/webdav/") set by perkeepd when launching the app.
	prefix := app.RoutePrefix()
	if !strings.HasSuffix(prefix, "/") {
		prefix = prefix + "/"
	}
	log.Printf("Using route prefix: %s", prefix)

	davHandler := &webdav.Handler{
		Prefix:     prefix,
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				log.Printf("WebDAV %s %s: %v", r.Method, r.URL.Path, err)
			} else {
				log.Printf("WebDAV %s %s", r.Method, r.URL.Path)
			}
		},
	}

	ws := webserver.New()
	ws.Handle("/", davHandler)

	if err := ws.Listen(listenAddr); err != nil {
		log.Fatalf("Listen: %v", err)
	}

	log.Printf("WebDAV server listening on %s", listenAddr)
	ws.Serve()
}

func loadConfig(cl *client.Client) *config {
	configURL := os.Getenv("CAMLI_APP_CONFIG_URL")
	if configURL == "" {
		log.Printf("No CAMLI_APP_CONFIG_URL set, using defaults")
		return &config{}
	}

	conf := &config{}
	if err := cl.GetJSON(context.Background(), configURL, conf); err != nil {
		log.Printf("Warning: could not get app config at %v: %v", configURL, err)
		return &config{}
	}
	return conf
}

func findRootPermanode(ctx context.Context, cl *client.Client, conf *config) (blob.Ref, error) {
	if conf.RootName == "" {
		return blob.Ref{}, fmt.Errorf(`camliRoot not configured.

Add "camliRoot" to your webdav app's appConfig in server config:
  "appConfig": {
    "camliRoot": "webdav-root"
  }

In dev mode, the server will auto-create this permanode.`)
	}

	result, err := cl.Query(ctx, &search.SearchQuery{
		Limit: 1,
		Constraint: &search.Constraint{
			Permanode: &search.PermanodeConstraint{
				Attr:  "camliRoot",
				Value: conf.RootName,
			},
		},
	})
	if err != nil {
		return blob.Ref{}, fmt.Errorf("failed to search for camliRoot %q: %v", conf.RootName, err)
	}

	if len(result.Blobs) == 0 || !result.Blobs[0].Blob.Valid() {
		return blob.Ref{}, fmt.Errorf("no permanode found with camliRoot=%q. In dev mode, restart the server to auto-create it.", conf.RootName)
	}

	return result.Blobs[0].Blob, nil
}
