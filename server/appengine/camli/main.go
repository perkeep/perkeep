// +build appengine

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

package appengine

import (
	"fmt"
	"net/http"
	"sync"

	"appengine"

	"camlistore.org/pkg/blobserver" // storage interface definition
	_ "camlistore.org/pkg/blobserver/cond"
	_ "camlistore.org/pkg/blobserver/replica"
	_ "camlistore.org/pkg/blobserver/shard"
	_ "camlistore.org/pkg/server"   // handlers: UI, publish, thumbnailing, etc
	"camlistore.org/pkg/serverinit" // wiring up the world from a JSON description

	// TODO(bradfitz): uncomment these config setup
	// Both require an App Engine context to make HTTP requests too.
	//_ "camlistore.org/pkg/blobserver/remote"
	//_ "camlistore.org/pkg/blobserver/s3"
)

// lazyInit is our root handler for App Engine. We don't have an App Engine
// context until the first request and we need that context to figure out
// our serving URL. So we use this to defer setting up our environment until
// the first request.
type lazyInit struct {
	mu    sync.Mutex
	ready bool
	mux   *http.ServeMux
}

func (li *lazyInit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	ctxPool.HandlerBegin(c)
	defer ctxPool.HandlerEnd(c)

	li.mu.Lock()
	if !li.ready {
		li.ready = realInit(w, r)
	}
	li.mu.Unlock()
	if li.ready {
		li.mux.ServeHTTP(w, r)
	}
}

var ctxPool ContextPool

var root = new(lazyInit)

func init() {
	// TODO(bradfitz): rename some of this to be consistent
	blobserver.RegisterStorageConstructor("appengine", blobserver.StorageConstructor(newFromConfig))
	blobserver.RegisterStorageConstructor("aeindex", blobserver.StorageConstructor(indexFromConfig))
	http.Handle("/", root)
}

func realInit(w http.ResponseWriter, r *http.Request) bool {
	ctx := appengine.NewContext(r)

	errf := func(format string, args ...interface{}) bool {
		ctx.Errorf("In init: "+format, args...)
		http.Error(w, fmt.Sprintf(format, args...), 500)
		return false
	}

	config, err := serverinit.Load("./config.json")
	if err != nil {
		return errf("Could not load server config: %v", err)
	}

	// Update the config to use the URL path derived from the first App Engine request.
	// TODO(bslatkin): Support hostnames that aren't x.appspot.com
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	baseURL := fmt.Sprintf("%s://%s/", scheme, appengine.DefaultVersionHostname(ctx))
	ctx.Infof("baseurl = %q", baseURL)

	root.mux = http.NewServeMux()
	_, err = config.InstallHandlers(root.mux, baseURL, false, r)
	if err != nil {
		return errf("Error installing handlers: %v", err)
	}

	return true
}
