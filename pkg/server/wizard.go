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
	"fmt"
	"html/template"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"

	"camlistore.org/pkg/auth"
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

func jsonPrint(i interface{}) (s string) {
	switch ei := i.(type) {
	case []interface{}:
		for _, v := range ei {
			s += jsonPrint(v) + ","
		}
		s = strings.TrimRight(s, ",")
	default:
		return fmt.Sprintf("%v", i)
	}
	return s
}

func sendWizard(req *http.Request, rw http.ResponseWriter, hasChanged bool) {
	config, err := jsonconfig.ReadFile(osutil.UserServerConfigPath())
	if err != nil {
		httputil.ServerError(rw, err)
		return
	}

	funcMap := template.FuncMap{
		"jsonPrint": jsonPrint,
	}

	body := `<form id="WizardForm" action="setup" method="post" enctype="multipart/form-data">`
	body += `{{range $k,$v := .}}{{printf "%v" $k}} <input type="text" size="30" name ="{{printf "%v" $k}}" value="{{jsonPrint $v}}"><br />{{end}}`
	body += `<input type="submit" form="WizardForm" value="Save"></form>`

	if hasChanged {
		body += `<p> Configuration succesfully rewritten </p>`
	}

	tmpl, err := template.New("wizard").Funcs(funcMap).Parse(topWizard + body + bottomWizard)
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
	var el interface{}
	for k, v := range req.Form {
		if _, ok := hilevelConf[k]; !ok {
			continue
		}

		switch k {
		case "TLS":
			b, err := strconv.ParseBool(v[0])
			if err != nil {
				httputil.ServerError(rw, fmt.Errorf("TLS field expects a boolean value"))
			}
			el = b
		case "replicateTo":
			els := []string{}
			if len(v[0]) > 0 {
				vals := strings.Split(v[0], ",")
				els = append(els, vals...)
			}
			el = els
		default:
			el = v[0]
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
	if !auth.LocalhostAuthorized(req) {
		fmt.Fprintf(rw,
			"<html><body>Setup only allowed from localhost"+
				"<p><a href='/'>Back</a></p>"+
				"</body></html>\n")
		return
	}
	if req.Method == "POST" {
		handleSetupChange(req, rw)
		return
	}

	sendWizard(req, rw, false)
}
