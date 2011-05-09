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

package main

import (
	"fmt"
	"http"
	"os"

	"camli/jsonconfig"
)

// RootHandler handles serving the about/splash page.
type RootHandler struct {
	// Don't advertise anything to non-authenticated clients.
	Stealth    bool

	// Show a setup link?
	// TODO: figure out details of when/how this will work
	OfferSetup bool
}

func (hl *handlerLoader) createRootHandler(conf jsonconfig.Obj) (h http.Handler, err os.Error) {
	root := &RootHandler{}
	root.Stealth = conf.OptionalBool("stealth", false)
	if err = conf.Validate(); err != nil {
		return
	}
	return root, nil
}

func (rh *RootHandler) ServeHTTP(conn http.ResponseWriter, req *http.Request) {
	if rh.Stealth {
		return
	}
	configLink := ""
	if rh.OfferSetup {
		configLink = "<p>If you're coming from localhost, hit <a href='/setup'>/setup</a>.</p>"
	}
	fmt.Fprintf(conn,
		"<html><body>This is camlistored, a "+
			"<a href='http://camlistore.org'>Camlistore</a> server."+
			"%s</body></html>\n",configLink)
}
