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

package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/user"
	"sync"
	"time"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/search"
)

// RootHandler handles serving the about/splash page.
type RootHandler struct {
	// Stealth determines whether we hide from non-authenticated
	// clients.
	Stealth bool

	OwnerName string // for display purposes only

	// URL prefixes (path or full URL) to the primary blob and
	// search root.
	BlobRoot   string
	SearchRoot string

	Storage blobserver.Storage // of BlobRoot, or nil

	searchInitOnce sync.Once // runs searchInit, which populates searchHandler
	searchInit     func()
	searchHandler  *search.Handler // of SearchRoot, or nil

	ui *UIHandler // or nil, if none configured
}

func (rh *RootHandler) SearchHandler() (h *search.Handler, ok bool) {
	rh.searchInitOnce.Do(rh.searchInit)
	return rh.searchHandler, rh.searchHandler != nil
}

func init() {
	blobserver.RegisterHandlerConstructor("root", newRootFromConfig)
}

func newRootFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	u, err := user.Current()
	if err != nil {
		return
	}
	root := &RootHandler{
		BlobRoot:   conf.OptionalString("blobRoot", ""),
		SearchRoot: conf.OptionalString("searchRoot", ""),
		OwnerName:  conf.OptionalString("ownerName", u.Name),
	}
	root.Stealth = conf.OptionalBool("stealth", false)
	if err = conf.Validate(); err != nil {
		return
	}

	if root.BlobRoot != "" {
		bs, err := ld.GetStorage(root.BlobRoot)
		if err != nil {
			return nil, fmt.Errorf("Root handler's blobRoot of %q error: %v", root.BlobRoot, err)
		}
		root.Storage = bs
	}

	root.searchInit = func() {}
	if root.SearchRoot != "" {
		prefix := root.SearchRoot
		if t := ld.GetHandlerType(prefix); t != "search" {
			if t == "" {
				return nil, fmt.Errorf("root handler's searchRoot of %q is invalid and doesn't refer to a declared handler", prefix)
			}
			return nil, fmt.Errorf("root handler's searchRoot of %q is of type %q, not %q", prefix, t, "search")
		}
		root.searchInit = func() {
			h, err := ld.GetHandler(prefix)
			if err != nil {
				log.Fatalf("Error fetching SearchRoot at %q: %v", prefix, err)
			}
			root.searchHandler = h.(*search.Handler)
			root.searchInit = nil
		}
	}

	return root, nil
}

func (rh *RootHandler) registerUIHandler(h *UIHandler) {
	rh.ui = h
}

func (rh *RootHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if wantsDiscovery(req) {
		// TODO(mpl): an OpDiscovery would be more to the point,
		//  but OpGet is similar/good enough for now.
		if auth.Allowed(req, auth.OpGet) {
			rh.serveDiscovery(rw, req)
			return
		}
		if !rh.Stealth {
			http.Error(rw, "Unauthorized", http.StatusUnauthorized)
		}
		return
	}

	if rh.Stealth {
		return
	}

	configLink := ""
	if auth.IsLocalhost(req) {
		configLink = "<p>If you're coming from localhost, hit <a href='/setup'>/setup</a>.</p>"
	}
	fmt.Fprintf(rw, "<html><body>This is camlistored, a "+
		"<a href='http://camlistore.org'>Camlistore</a> server."+
		"%s</body></html>\n", configLink)
}

func (rh *RootHandler) serveDiscovery(rw http.ResponseWriter, req *http.Request) {
	m := map[string]interface{}{
		"blobRoot":   rh.BlobRoot,
		"searchRoot": rh.SearchRoot,
		"ownerName":  rh.OwnerName,
	}
	if gener, ok := rh.Storage.(blobserver.Generationer); ok {
		initTime, gen, err := gener.StorageGeneration()
		if err != nil {
			m["storageGenerationError"] = err.Error()
		} else {
			m["storageInitTime"] = initTime.UTC().Format(time.RFC3339)
			m["storageGeneration"] = gen
		}
	}
	if rh.ui != nil {
		rh.ui.populateDiscoveryMap(m)
	}
	discoveryHelper(rw, req, m)
}

func discoveryHelper(rw http.ResponseWriter, req *http.Request, m map[string]interface{}) {
	rw.Header().Set("Content-Type", "text/javascript")
	if cb := req.FormValue("cb"); identPattern.MatchString(cb) {
		fmt.Fprintf(rw, "%s(", cb)
		defer rw.Write([]byte(");\n"))
	} else if v := req.FormValue("var"); identOrDotPattern.MatchString(v) {
		fmt.Fprintf(rw, "%s = ", v)
		defer rw.Write([]byte(";\n"))
	}
	bytes, _ := json.MarshalIndent(m, "", "  ")
	rw.Write(bytes)
}
