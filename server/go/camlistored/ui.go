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
	"bytes"
	"fmt"
	"http"
	"io"
	"json"
	"log"
	"os"
	"path/filepath"
	"regexp"

	"camli/blobserver"
	"camli/jsonconfig"
)

var _ = log.Printf

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

func init() {
	blobserver.RegisterHandlerConstructor("ui", newUiFromConfig)
}

func newUiFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err os.Error) {
	ui := &UIHandler{}
	ui.BlobRoot = conf.OptionalString("blobRoot", "")
	ui.SearchRoot = conf.OptionalString("searchRoot", "")
	ui.JSONSignRoot = conf.OptionalString("jsonSignRoot", "")
	ui.FilesDir = conf.OptionalString("staticFiles", defaultFilesDir())
	if err = conf.Validate(); err != nil {
		return
	}

	checkType := func(key string, htype string) {
		v := conf.OptionalString(key, "")
		if v == "" {
			return
		}
		ct := ld.GetHandlerType(v)
		if ct == "" {
			err = fmt.Errorf("UI handler's %q references non-existant %q", key, v)
		} else if ct != htype {
			err = fmt.Errorf("UI handler's %q references %q of type %q; expected type %q", key, v,
				ct, htype)
		}
	}
	checkType("searchRoot", "search")
	checkType("jsonSignRoot", "jsonsign")
	if err != nil {
		return
	}

	if ui.BlobRoot != "" {
		_, err := ld.GetStorage(ui.BlobRoot)
		if err != nil {
			return nil, fmt.Errorf("UI handler's blobRoot of %q error: %v", ui.BlobRoot, err)
		}
	}

	fi, sterr := os.Stat(ui.FilesDir)
	if sterr != nil || !fi.IsDirectory() {
		err = fmt.Errorf("UI handler's \"staticFiles\" of %q is invalid", ui.FilesDir)
		return
	}
	return ui, nil
}

func camliMode(req *http.Request) string {
	// TODO-GO: this is too hard to get at the GET Query args on a
	// POST request.
	m, err := http.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return ""
	}
	if mode, ok := m["camli.mode"]; ok && len(mode) > 0 {
		return mode[0]
	}
	return ""
}

func wantsDiscovery(req *http.Request) bool {
	return req.Method == "GET" &&
		(req.Header.Get("Accept") == "text/x-camli-configuration" ||
		camliMode(req) == "config")
}

func wantsUploadHelper(req *http.Request) bool {
	return req.Method == "POST" && camliMode(req) == "uploadhelper"
}

func (ui *UIHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Vary", "Accept")
	switch {
	case wantsDiscovery(req):
		ui.serveDiscovery(rw, req)
	case wantsUploadHelper(req):
		ui.serveUploadHelper(rw, req)
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
                "uploadHelper": "?camli.mode=uploadhelper", // hack; remove with better javascript
        })
	rw.Write(bytes)
	if inCb {
		rw.Write([]byte{')'})
	}
}

func (ui *UIHandler) serveUploadHelper(rw http.ResponseWriter, req *http.Request) {
	var buf bytes.Buffer
	defer io.Copy(rw, &buf)

	fmt.Fprintf(&buf, "<pre>\n")

	mr, err := req.MultipartReader()
	if err != nil {
		fmt.Fprintf(&buf, "multipart reader: %v", err)
		return
	}

	for {
		part, err := mr.NextPart()
		if err == os.EOF {
			break
		}
		if err != nil {
			buf.Reset()
			http.Error(rw, "Multipart error: " + err.String(), 500)
			break
		}
		fmt.Fprintf(&buf, "filename=%q, formname=%q\n", part.FileName(), part.FormName())
	}
}
