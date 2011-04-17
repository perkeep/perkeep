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
	"json"
	"os"
	"path/filepath"
	"regexp"

	"camli/jsonconfig"
)

var staticFilePattern = regexp.MustCompile(`/static/([a-zA-Z0-9\-\_]+\.(html|js|css|png|jpg|gif))$`)
var identPattern = regexp.MustCompile(`^[a-zA-Z\_]+$`)

// UIHandler handles serving the UI and discovery JSON.
type UIHandler struct {
	// URL prefixes (path or full URL) to the primary blob and
	// search root.  Only used by the UI and thus necessary if UI
	// is true.
	BlobRoot     string
	SearchRoot   string
	JSONSignRoot string

	FilesDir string
}

func defaultFilesDir() string {
	dir, _ := filepath.Split(os.Args[0])
	return filepath.Join(dir, "ui")
}

func createUIHandler(conf jsonconfig.Obj) (h http.Handler, err os.Error) {
	ui := &UIHandler{}
	ui.BlobRoot = conf.OptionalString("blobRoot", "")
	ui.SearchRoot = conf.OptionalString("searchRoot", "")
	ui.JSONSignRoot = conf.OptionalString("jsonSignRoot", "")
	ui.FilesDir = conf.OptionalString("staticFiles", defaultFilesDir())
	if err = conf.Validate(); err != nil {
		return
	}
	fi, sterr := os.Stat(ui.FilesDir)
	if sterr != nil || !fi.IsDirectory() {
		err = fmt.Errorf("UI handler's \"staticFiles\" of %q is invalid", ui.FilesDir)
		return
	}
	return ui, nil
}

func wantsDiscovery(req *http.Request) bool {
	return req.Method == "GET" &&
		(req.Header.Get("Accept") == "text/x-camli-configuration" ||
			req.FormValue("camli.mode") == "config")
}

func (ui *UIHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Vary", "Accept")
	switch {
	case wantsDiscovery(req):
		ui.serveDiscovery(rw, req)
	default:
		file := ""
		if m := staticFilePattern.FindStringSubmatch(req.URL.Path); m != nil {
			file = m[1]
		} else {
			file = "index.html"
		}
		http.ServeFile(rw, req, filepath.Join(ui.FilesDir, file))
	}
}

func (ui *UIHandler) serveDiscovery(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Content-Type", "text/javascript")
	inCb := false
	if cb := req.FormValue("cb"); identPattern.MatchString(cb) {
		fmt.Fprintf(rw, "%s(", cb)
		inCb = true
	}
	bytes, _ := json.Marshal(map[string]interface{}{
                "blobRoot":   ui.BlobRoot,
                "searchRoot": ui.SearchRoot,
                "jsonSignRoot": ui.JSONSignRoot,
        })
	rw.Write(bytes)
	if inCb {
		rw.Write([]byte{')'})
	}
}
