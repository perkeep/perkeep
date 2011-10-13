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

	"camli/blobserver"
	"camli/serverconfig"
)

var mux = http.NewServeMux()

// lazyInit is our root handler for App Engine. We don't have an App Engine
// context until the first request and we need that context to figure out
// our serving URL. So we use this to defer setting up our environment until
// the first request.
type lazyInit struct {
	mux http.Handler
	once sync.Once
}

func (li *lazyInit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	li.once.Do(func() {
		realInit(r)
	})

	li.mux.ServeHTTP(w, r)
}

var root = &lazyInit{mux: mux}

func init() {
	http.Handle("/", root)
}

func exitFailure(pattern string, args ...interface{}) {
	panic(fmt.Sprintf(pattern, args...))
}

func realInit(r *http.Request) {
	blobserver.RegisterStorageConstructor("appengine", blobserver.StorageConstructor(newFromConfig))

	config, err := serverconfig.Load("./config.json")
	if err != nil {
		exitFailure("Could not load server config: %v", err)
	}

	// Update the config to use the URL path derived from the first App Engine request.
	// TODO(bslatkin): Support hostnames that aren't x.appspot.com
	// TODO(bslatkin): Support the HTTPS scheme
	config.Obj["baseURL"] = fmt.Sprintf("http://%s/", r.Header.Get("X-Appengine-Default-Version-Hostname"))

	baseURL := ""
	err = config.InstallHandlers(mux, baseURL)
	if err != nil {
		exitFailure("Error parsing config: %v", err)
	}
}
