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
	"net/http"

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

	// URL prefixes (path or full URL) to the primary blob and
	// search root.
	BlobRoot   string
	SearchRoot string

	Storage blobserver.Storage // of BlobRoot, or nil
	Search  *search.Handler    // of SearchRoot, or nil

	ui *UIHandler // or nil, if none configured
}

func init() {
	blobserver.RegisterHandlerConstructor("root", newRootFromConfig)
}

func newRootFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	root := &RootHandler{
		BlobRoot:   conf.OptionalString("blobRoot", ""),
		SearchRoot: conf.OptionalString("searchRoot", ""),
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

	if root.SearchRoot != "" {
		h, _ := ld.GetHandler(root.SearchRoot)
		root.Search = h.(*search.Handler)
	}

	return root, nil
}

func (rh *RootHandler) registerUIHandler(h *UIHandler) {
	rh.ui = h
}

func (rh *RootHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if wantsDiscovery(req) {
		if auth.IsAuthorized(req) {
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
	if auth.LocalhostAuthorized(req) {
		configLink = "<p>If you're coming from localhost, hit <a href='/setup'>/setup</a>.</p>"
	}
	fmt.Fprintf(rw, "<html><body>This is camlistored, a "+
		"<a href='http://camlistore.org'>Camlistore</a> server."+
		"%s</body></html>\n", configLink)
}

func (rh *RootHandler) serveDiscovery(rw http.ResponseWriter, req *http.Request) {
	m := map[string]interface{}{
		"blobRoot":        rh.BlobRoot,
		"searchRoot":      rh.SearchRoot,
	}
	if rh.ui != nil {
		rh.ui.populateDiscoveryMap(m)
	}
	discoveryHelper(rw, req, m)
}

func discoveryHelper(rw http.ResponseWriter, req *http.Request, m map[string]interface{}) {
	rw.Header().Set("Content-Type", "text/javascript")
	inCb := false
	if cb := req.FormValue("cb"); identPattern.MatchString(cb) {
		fmt.Fprintf(rw, "%s(", cb)
		inCb = true
	}
	bytes, _ := json.MarshalIndent(m, "", "  ")
	rw.Write(bytes)
	if inCb {
		rw.Write([]byte{')'})
	}
}
