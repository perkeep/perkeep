/*
Copyright 2012 The Perkeep Authors.

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

	"go4.org/jsonconfig"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blobserver"
)

// SetupHandler handles serving the wizard setup page.
type SetupHandler struct {
	config jsonconfig.Obj
}

func init() {
	blobserver.RegisterHandlerConstructor("setup", newSetupFromConfig)
}

func newSetupFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	wizard := &SetupHandler{config: conf}
	return wizard, nil
}

func (sh *SetupHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if !auth.IsLocalhost(req) {
		fmt.Fprintf(rw,
			"<html><body>Setup only allowed from localhost"+
				"<p><a href='/'>Back</a></p>"+
				"</body></html>\n")
		return
	}
	http.Redirect(rw, req, "https://perkeep.org/doc/server-config", http.StatusMovedPermanently)
	return

	// TODO: this file and the code in wizard-html.go is outdated. Anyone interested enough
	// can take care of updating it as something nicer which would fit better with the
	// react UI. But in the meantime we don't link to it anymore.

	// if req.Method == "POST" {
	// 	err := req.ParseMultipartForm(10e6)
	// 	if err != nil {
	// 		httputil.ServeError(rw, req, err)
	// 		return
	// 	}
	// 	if len(req.Form) > 0 {
	// 		handleSetupChange(rw, req)
	// 	}
	// 	return
	// }

	// sendWizard(rw, req, false)
}
