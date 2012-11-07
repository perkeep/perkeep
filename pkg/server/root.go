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
	"fmt"
	"net/http"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
)

// RootHandler handles serving the about/splash page.
type RootHandler struct {
	// Don't advertise anything to non-authenticated clients.
	Stealth bool

	ui *UIHandler // or nil, if none configured
}

func init() {
	blobserver.RegisterHandlerConstructor("root", newRootFromConfig)
}

func newRootFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	root := &RootHandler{}
	root.Stealth = conf.OptionalBool("stealth", false)
	if err = conf.Validate(); err != nil {
		return
	}

	if _, h, err := ld.FindHandlerByType("ui"); err == nil {
		root.ui = h.(*UIHandler)
	}

	return root, nil
}

func (rh *RootHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// TODO(bradfitz): discovery should work without a 'ui' handler registered.
	// It should be part of the root handler, not part of the UI handler.
	if rh.ui != nil && wantsDiscovery(req) {
		if auth.IsAuthorized(req) {
			rh.ui.serveDiscovery(rw, req)
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
