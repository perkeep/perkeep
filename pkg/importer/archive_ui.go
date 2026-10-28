/*
Copyright 2026 The Perkeep Authors

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

package importer

import (
	"bytes"
	_ "embed"
	"net/http"
	"time"
)

//go:embed archive.js
var archiveScript []byte

// archiveUploadData describes a browser client of the existing file-upload and
// archive APIs. URLs are same-origin paths, including configured handler prefixes;
// no archive bytes pass through the importer HTML handlers.
type archiveUploadData struct {
	Title        string
	DiscoveryURL string
	ArchiveURL   string
	ScriptURL    string
	AccountBase  string
	AccountRef   string
	FileRef      string
	AccountPage  bool
}

func (im *importer) archiveUpload(ia *importerAcct) *archiveUploadData {
	if _, ok := im.impl.(ArchiveImporter); !ok {
		return nil
	}
	base := im.URL()
	data := &archiveUploadData{
		Title:        im.Title(),
		DiscoveryURL: im.host.discoveryRoot + "?camli.mode=config",
		ArchiveURL:   base + "/archive",
		ScriptURL:    base + "/archive.js",
		AccountBase:  base + "/",
	}
	if ia != nil {
		data.AccountPage = true
		data.AccountRef = ia.acct.PermanodeRef().String()
		data.FileRef = ia.acct.Attr(AcctAttrArchiveInput)
	}
	return data
}

func (h *Host) serveArchiveScript(w http.ResponseWriter, r *http.Request, im *importer) {
	if _, ok := im.impl.(ArchiveImporter); !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "archive.js", time.Time{}, bytes.NewReader(archiveScript))
}
