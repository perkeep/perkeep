/*
Copyright 2015 The Camlistore Authors.

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
	"strconv"
	"sync"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/types/clientconfig"
)

const helpHTML string = `<html>
		<head>
			<title>Help</title>
		</head>
		<body>
			<h2>Help</h2>

			<h3>Web User Interface</h3>
			<p><a href='https://camlistore.googlesource.com/camlistore/+/master/doc/search-ui.txt'>Search bar predicates.</a></p>

			<h3>Client Configuration</h3>
			<p>You will need to use the following <a href='http://camlistore.org/docs/client-config'>client configuration</a> in order to access this server using the Camlistore command line tools.</p>
			<pre>{{ . }}</pre>

			<h3>Anything Else?</h3>
			<p>See the Camlistore <a href='http://camlistore.org/docs/'>online documentation</a> and <a href='http://camlistore.org/community/'>community contacts</a>.</p>
		</body>
	</html>`

// HelpHandler publishes information related to accessing the server
type HelpHandler struct {
	clientConfig *clientconfig.Config // generated from serverConfig
	serverConfig jsonconfig.Obj       // low-level config
	goTemplate   *template.Template   // for rendering
}

// setServerConfigOnce guards operation within SetServerConfig
var setServerConfigOnce sync.Once

// SetServerConfig enables the handler to receive the server config
// before InitHandler, which generates a client config from the server config, is called.
func (hh *HelpHandler) SetServerConfig(config jsonconfig.Obj) {
	setServerConfigOnce.Do(func() { hh.serverConfig = config })
}

func init() {
	blobserver.RegisterHandlerConstructor("help", newHelpFromConfig)
}

func (hh *HelpHandler) InitHandler(hl blobserver.FindHandlerByTyper) error {
	if hh.serverConfig == nil {
		return fmt.Errorf("HelpHandler's serverConfig must be set before calling its InitHandler")
	}

	clientConfig, err := clientconfig.GenerateClientConfig(hh.serverConfig)
	if err != nil {
		return fmt.Errorf("error generating client config: %v", err)
	}
	hh.clientConfig = clientConfig

	tmpl, err := template.New("help").Parse(helpHTML)
	if err != nil {
		return fmt.Errorf("error creating template: %v", err)
	}
	hh.goTemplate = tmpl

	return nil
}

func newHelpFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	return &HelpHandler{}, nil
}

func (hh *HelpHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	suffix := httputil.PathSuffix(req)
	if !httputil.IsGet(req) {
		http.Error(rw, "Illegal help method.", http.StatusMethodNotAllowed)
		return
	}
	switch suffix {
	case "":
		if clientConfig := req.FormValue("clientConfig"); clientConfig != "" {
			if clientConfigOnly, err := strconv.ParseBool(clientConfig); err == nil && clientConfigOnly {
				httputil.ReturnJSON(rw, hh.clientConfig)
				return
			}
		}
		hh.serveHelpHTML(rw, req)
	default:
		http.Error(rw, "Illegal help path.", http.StatusNotFound)
	}
}

func (hh *HelpHandler) serveHelpHTML(rw http.ResponseWriter, req *http.Request) {
	jsonBytes, err := json.MarshalIndent(hh.clientConfig, "", "  ")
	if err != nil {
		httputil.ServeError(rw, req, fmt.Errorf("could not serialize client config JSON: %v", err))
		return
	}

	hh.goTemplate.Execute(rw, string(jsonBytes))
}
