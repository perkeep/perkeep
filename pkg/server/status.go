/*
Copyright 2013 The Camlistore Authors.

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
	"net/http"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/buildinfo"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonconfig"
)

// StatusHandler publishes server status information.
type StatusHandler struct {
}

func init() {
	blobserver.RegisterHandlerConstructor("status", newStatusFromConfig)
}

func newStatusFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	return &StatusHandler{}, nil
}

func (sh *StatusHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	suffix := httputil.PathSuffix(req)
	if req.Method != "GET" {
		http.Error(rw, "Illegal URL.", http.StatusMethodNotAllowed)
		return
	}
	if suffix == "status.json" {
		sh.serveStatus(rw, req)
		return
	}
	http.Error(rw, "Illegal URL.", 404)
}

type statusResponse struct {
	Version string `json:"version"`
}

func (sh *StatusHandler) serveStatus(rw http.ResponseWriter, req *http.Request) {
	res := &statusResponse{
		Version: buildinfo.Version(),
	}

	httputil.ReturnJSON(rw, res)
}
