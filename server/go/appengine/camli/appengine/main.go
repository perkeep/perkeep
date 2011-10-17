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
	"http"
	"sync"

	"appengine"

	"camli/blobserver"
	"camli/serverconfig"
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
	li.mu.Lock()
	if !li.ready {
		li.ready = realInit(w, r)
	}
	li.mu.Unlock()
	if li.ready {
		li.mux.ServeHTTP(w, r)
	}
}

var root = new(lazyInit)

func init() {
	blobserver.RegisterStorageConstructor("appengine", blobserver.StorageConstructor(newFromConfig))
	http.Handle("/", root)
}

func realInit(w http.ResponseWriter, r *http.Request) bool {
	ctx := appengine.NewContext(r)

	errf := func(format string, args ...interface{}) bool {
		ctx.Errorf("In init: "+format, args...)
		http.Error(w, fmt.Sprintf(format, args...), 500)
		return false
	}

	config, err := serverconfig.Load("./config.json")
	if err != nil {
		return errf("Could not load server config: %v", err)
	}

	// Update the config to use the URL path derived from the first App Engine request.
	// TODO(bslatkin): Support hostnames that aren't x.appspot.com
	// TODO(bslatkin): Support the HTTPS scheme
	baseURL := fmt.Sprintf("http://%s/", r.Header.Get("X-Appengine-Default-Version-Hostname"))
	ctx.Infof("baseurl = %q", baseURL)
	config.Obj["baseURL"] = baseURL

	root.mux = http.NewServeMux()
	err = config.InstallHandlers(root.mux, baseURL)
	if err != nil {
		return errf("Error installing handlers: %v", err)
	}

	return true
}
