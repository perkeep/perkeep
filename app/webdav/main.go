/*
Copyright 2022 The Perkeep Authors.

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

package main // import "perkeep.org/app/webdav"

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"time"

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

var (
	logger = log.New(os.Stderr, "WEBDAV: ", log.LstdFlags)
)

type config struct {
	CamliRoot string
	Prefix    string
}

func appConfig() (*config, error) {
	configURL := os.Getenv("CAMLI_APP_CONFIG_URL")
	if configURL == "" {
		return nil, fmt.Errorf("WebDAV application needs a CAMLI_APP_CONFIG_URL env var")
	}
	conf := &config{}

	prefix := os.Getenv("CAMLI_APP_ROUTE_PREFIX")
	if prefix == "" {
		logger.Fatalf("WebDAV application needs a CAMLI_APP_ROUTE_PREFIX env var")
	}
	conf.Prefix = prefix

	cl, err := app.Client()
	if err != nil {
		return nil, fmt.Errorf("could not get a client to fetch extra config: %v", err)
	}

	pause := time.Second
	giveupTime := time.Now().Add(time.Hour)
	for {
		err := cl.GetJSON(context.TODO(), configURL, conf)
		if err == nil {
			break
		}
		if time.Now().After(giveupTime) {
			logger.Fatalf("giving up on starting: could not get app config at %v: %v", configURL, err)
		}
		logger.Printf("could not get app config at %v: %v. Will retry in a while.", configURL, err)
		time.Sleep(pause)
		pause *= 2
	}
	return conf, nil
}

type webdavHandler struct {
	webdav.Handler
}

func newWebdavHandler(conf *config) (*webdavHandler, error) {
	client, err := app.Client()
	if err != nil {
		return nil, fmt.Errorf("unable to create app client: %w", err)
	}
	camliRoot, err := camliRootQuery(client, conf.CamliRoot)
	if err != nil {
		return nil, fmt.Errorf("unable to look up camli root: %w", err)
	}
	webdavFs, err := newWebDavFS(client, camliRoot)
	if err != nil {
		return nil, fmt.Errorf("unable to create new webdav fs: %w", err)
	}

	return &webdavHandler{
		Handler: webdav.Handler{
			Prefix:     conf.Prefix,
			FileSystem: webdavFs,
			LockSystem: webdav.NewMemLS(),
		},
	}, nil
}

func camliRootQuery(client *client.Client, name string) (blob.Ref, error) {
	res, err := client.Query(context.TODO(), &search.SearchQuery{
		Limit: 1,
		Constraint: &search.Constraint{
			Permanode: &search.PermanodeConstraint{
				Attr:  "camliRoot",
				Value: name,
			},
		},
	})
	if err != nil {
		return blob.Ref{}, fmt.Errorf("unable to query camli root: %w", err)
	}
	if len(res.Blobs) == 0 {
		return blob.Ref{}, fmt.Errorf("no camliroot named '%s' was found", name)
	}
	return res.Blobs[0].Blob, nil
}

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Fprintf(os.Stderr, "WebDAV version: %s\nGo version: %s (%s/%s)\n",
			buildinfo.Summary(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return
	}

	logger.Printf("Starting WebDAV version %s; Go %s (%s/%s)", buildinfo.Summary(), runtime.Version(),
		runtime.GOOS, runtime.GOARCH)

	listenAddr, err := app.ListenAddress()
	if err != nil {
		logger.Fatalf("listen address: %v", err)
	}
	config, err := appConfig()
	if err != nil {
		logger.Fatalf("no app config: %v", err)
	}
	wdh, err := newWebdavHandler(config)
	if err != nil {
		logger.Fatalf("unable to create webdav handler: %v", err)
	}

	ws := webserver.New()
	ws.Logger = logger

	ws.Handle("/", wdh)
	if err := ws.Listen(listenAddr); err != nil {
		logger.Fatalf("Listen: %v", err)
	}

	ws.Serve()
}
