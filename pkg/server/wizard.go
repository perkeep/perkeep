/*
Copyright 2012 The Camlistore Authors.

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
	"log"
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

func printWizard(i interface{}) (s string) {
	switch ei := i.(type) {
	case []string:
		for _, v := range ei {
			s += printWizard(v) + ","
		}
		s = strings.TrimRight(s, ",")
	case []interface{}:
		for _, v := range ei {
			s += printWizard(v) + ","
		}
		s = strings.TrimRight(s, ",")
	default:
		return fmt.Sprintf("%v", i)
	}
	return s
}

// Flatten all published entities as lists and move them at the root 
// of the conf, to have them displayed individually by the template
func flattenPublish(config jsonconfig.Obj) error {
	gallery := []string{}
	blog := []string{}
	config["gallery"] = gallery
	config["blog"] = blog
	published, ok := config["publish"]
	if !ok {
		delete(config, "publish")
		return nil
	}
	pubObj, ok := published.(map[string]interface{})
	if !ok {
		return fmt.Errorf("Was expecting a map[string]interface{} for \"publish\", got %T", published)
	}
	for k, v := range pubObj {
		pub, ok := v.(map[string]interface{})
		if !ok {
			return fmt.Errorf("Was expecting a map[string]interface{} for %s, got %T", k, pub)
		}
		template, rootPermanode, style := "", "", ""
		for pk, pv := range pub {
			val, ok := pv.(string)
			if !ok {
				return fmt.Errorf("Was expecting a string for %s, got %T", pk, pv)
			}
			switch pk {
			case "template":
				template = val
			case "rootPermanode":
				rootPermanode = val
			case "style":
				style = val
			default:
				return fmt.Errorf("Unknown key %q in %s", pk, k)
			}
		}
		if template == "" || rootPermanode == "" {
			return fmt.Errorf("missing \"template\" key or \"rootPermanode\" key in %s", k)
		}
		obj := []string{k, rootPermanode, style}
		config[template] = obj
	}

	delete(config, "publish")
	return nil
}

func sendWizard(rw http.ResponseWriter, req *http.Request, hasChanged bool) {
	config, err := jsonconfig.ReadFile(osutil.UserServerConfigPath())
	if err != nil {
		httputil.ServerError(rw, req, err)
		return
	}

	err = flattenPublish(config)
	if err != nil {
		httputil.ServerError(rw, req, err)
		return
	}

	funcMap := template.FuncMap{
		"printWizard":    printWizard,
		"inputIsGallery": func(inputName string) bool { return inputName == "gallery" },
	}

	body := `<form id="WizardForm" action="setup" method="post" enctype="multipart/form-data">`
	body += `{{range $k,$v := .}}{{printf "%v" $k}} <input type="text" size="30" name ="{{printf "%v" $k}}" value="{{printWizard $v}}" {{if inputIsGallery $k}}placeholder="/pics/,sha1-xxxx,pics.css"{{end}}><br />{{end}}`
	body += `<input type="submit" form="WizardForm" value="Save"></form>`

	if hasChanged {
		body += `<p> Configuration succesfully rewritten </p>`
	}

	tmpl, err := template.New("wizard").Funcs(funcMap).Parse(topWizard + body + bottomWizard)
	if err != nil {
		httputil.ServerError(rw, req, err)
		return
	}
	err = tmpl.Execute(rw, config)
	if err != nil {
		httputil.ServerError(rw, req, err)
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

func handleSetupChange(rw http.ResponseWriter, req *http.Request) {
	hilevelConf, err := jsonconfig.ReadFile(osutil.UserServerConfigPath())
	if err != nil {
		httputil.ServerError(rw, req, err)
		return
	}

	hasChanged := false
	var el interface{}
	publish := jsonconfig.Obj{}
	for k, v := range req.Form {
		if _, ok := hilevelConf[k]; !ok {
			if k != "gallery" && k != "blog" {
				continue
			}
		}

		switch k {
		case "https":
			b, err := strconv.ParseBool(v[0])
			if err != nil {
				httputil.ServerError(rw, req, fmt.Errorf("https field expects a boolean value"))
			}
			el = b
		case "replicateTo":
			// TODO(mpl): figure out why it is always seen as different from the conf
			el = []interface{}{}
			if len(v[0]) > 0 {
				els := []string{}
				vals := strings.Split(v[0], ",")
				els = append(els, vals...)
				el = els
			}
		// TODO(mpl): "handler,rootPermanode[,style]" for each published entity for now.
		// we will need something more readable later probably
		case "gallery", "blog":
			if len(v[0]) > 0 {
				pub := strings.Split(v[0], ",")
				if len(pub) < 2 || len(pub) > 3 {
					// no need to fail loudly for now as we'll probably change this format
					continue
				}
				handler := jsonconfig.Obj{}
				handler["template"] = k
				handler["rootPermanode"] = pub[1]
				if len(pub) > 2 {
					handler["style"] = pub[2]
				}
				publish[pub[0]] = handler
			}
			continue
		default:
			el = v[0]
		}
		if reflect.DeepEqual(hilevelConf[k], el) {
			continue
		}
		hasChanged = true
		hilevelConf[k] = el
	}
	// "publish" wasn't checked yet
	if !reflect.DeepEqual(hilevelConf["publish"], publish) {
		hilevelConf["publish"] = publish
		hasChanged = true
	}

	if hasChanged {
		err = rewriteConfig(&hilevelConf, osutil.UserServerConfigPath())
		if err != nil {
			httputil.ServerError(rw, req, err)
			return
		}
	}
	sendWizard(rw, req, hasChanged)
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
		err := req.ParseMultipartForm(10e6)
		if err != nil {
			httputil.ServerError(rw, req, err)
			return
		}
		if len(req.Form) > 0 {
			handleSetupChange(rw, req)
			return
		}
		if strings.Contains(req.URL.Path, "restartCamli") {
			err = osutil.RestartProcess()
			if err != nil {
				log.Fatal("Failed to restart: " + err.Error())
			}
		}
	}

	sendWizard(rw, req, false)
}
