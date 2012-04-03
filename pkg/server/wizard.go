/*
Copyright 2012 Google Inc.

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
	"html/template"
	"net/http"
	"os"
	"reflect"
	"strconv"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/osutil"
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

func sendWizard(req *http.Request, rw http.ResponseWriter, hasChanged bool) {
	config, err := jsonconfig.ReadFile(osutil.UserServerConfigPath())
	if err != nil {
		httputil.ServerError(rw, err)
		return
	}

	body := `<form id="WizardForm" action="setup" method="post" enctype="multipart/form-data">`
	body += `{{range $k,$v := .}}{{printf "%v" $k}} <input type="text" size="30" name ="{{printf "%v" $k}}" value="{{printf "%v" $v}}"><br />{{end}}`
	body += `<input type="submit" form="WizardForm" value="Save"></form>`

	if hasChanged {
		body += `<p> Configuration succesfully rewritten </p>`
	}

	tmpl, err := template.New("wizard").Parse(topWizard + body + bottomWizard)
	if err != nil {
		httputil.ServerError(rw, err)
		return
	}
	err = tmpl.Execute(rw, config)
	if err != nil {
		httputil.ServerError(rw, err)
		return
	}
}

func rewriteConfig(config *jsonconfig.Obj, configfile string) error {
	b, err := json.MarshalIndent(*config, "", "	")
	if err != nil {
		return err
	}
	s := string(b)
	f, err := os.Create(configfile)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(s)
	return err
}

func handleSetupChange(req *http.Request, rw http.ResponseWriter) {
	err := req.ParseMultipartForm(10e6)
	if err != nil {
		httputil.ServerError(rw, err)
		return
	}
	hilevelConf, err := jsonconfig.ReadFile(osutil.UserServerConfigPath())
	if err != nil {
		httputil.ServerError(rw, err)
		return
	}

	hasChanged := false
	for k, v := range req.Form {
		if _, ok := hilevelConf[k]; !ok {
			continue
		}

		// TODO(mpl): this only works for single elements (so it actually fails for
		// replicateTo already because it's supposed to be an array).
		// so the question is:
		// Do we allow the high level conf file to get more complicated than that?
		// i.e, do we allow some fields to be arrays, maps, etc? it looks like we need
		// that at least for replicateTo, which is an empty array for now. So, we could
		// 1) only allow fields to be simple elements (bools, ints, or strings), but that 
		// limits the user's possibilities with that wizard
		// 2) or allow input fields to be valid json syntax so the user can input arrays
		// and such. But then it's not such a userfriendly wizard anymore.
		// 3) lose the genericity and expect the type depending on the key. i.e, I know
		// I'm supposed to get an array for replicateTo, so I know I'm supposed to get a
		// comma, or space, or whatever separated list of elements in that field.
		// 3) something else altogether?
		var el interface{}
		if b, err := strconv.ParseBool(v[0]); err == nil {
			el = b
		} else {
			if i, err := strconv.ParseInt(v[0], 0, 32); err == nil {
				el = i
			} else {
				el = v[0]
			}
		}
		if reflect.DeepEqual(hilevelConf[k], el) {
			continue
		}
		hasChanged = true
		hilevelConf[k] = el
	}

	if hasChanged {
		err = rewriteConfig(&hilevelConf, osutil.UserServerConfigPath())
		if err != nil {
			httputil.ServerError(rw, err)
			return
		}
	}
	sendWizard(req, rw, hasChanged)
	return
}

func (sh *SetupHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	// TODO(mpl): do the auth checking. see the localtcp story
	if req.Method == "POST" {
		handleSetupChange(req, rw)
		return
	}

	sendWizard(req, rw, false)
}
