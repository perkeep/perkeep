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
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"time"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/buildinfo"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/types/camtypes"
)

// StatusHandler publishes server status information.
type StatusHandler struct {
	prefix        string
	handlerFinder blobserver.FindHandlerByTyper
}

func init() {
	blobserver.RegisterHandlerConstructor("status", newStatusFromConfig)
}

var _ blobserver.HandlerIniter = (*StatusHandler)(nil)

func newStatusFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	if err := conf.Validate(); err != nil {
		return nil, err
	}
	return &StatusHandler{
		prefix:        ld.MyPrefix(),
		handlerFinder: ld,
	}, nil
}

func (sh *StatusHandler) InitHandler(hl blobserver.FindHandlerByTyper) error {
	_, h, err := hl.FindHandlerByType("search")
	if err == blobserver.ErrHandlerTypeNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	go func() {
		var lastSend *status
		for {
			cur := sh.currentStatus()
			if reflect.DeepEqual(cur, lastSend) {
				// TODO: something better. get notified on interesting events.
				time.Sleep(10 * time.Second)
				continue
			}
			lastSend = cur
			js, _ := json.MarshalIndent(cur, "", "  ")
			h.(*search.Handler).SendStatusUpdate(js)
		}
	}()
	return nil
}

func (sh *StatusHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	suffix := httputil.PathSuffix(req)
	if !httputil.IsGet(req) {
		http.Error(rw, "Illegal status method.", http.StatusMethodNotAllowed)
		return
	}
	switch suffix {
	case "status.json":
		sh.serveStatusJSON(rw, req)
	case "":
		sh.serveStatusHTML(rw, req)
	default:
		http.Error(rw, "Illegal status path.", 404)
	}
}

type status struct {
	Version      string                   `json:"version"`
	Errors       []camtypes.StatusError   `json:"errors,omitempty"`
	Sync         map[string]syncStatus    `json:"sync"`
	Storage      map[string]storageStatus `json:"storage"`
	importerRoot string
	rootPrefix   string

	ImporterAccounts interface{} `json:"importerAccounts"`
}

func (st *status) addError(msg, url string) {
	st.Errors = append(st.Errors, camtypes.StatusError{
		Error: msg,
		URL:   url,
	})
}

func (st *status) isHandler(pfx string) bool {
	if pfx == st.importerRoot {
		return true
	}
	if _, ok := st.Sync[pfx]; ok {
		return true
	}
	if _, ok := st.Storage[pfx]; ok {
		return true
	}
	return false
}

type storageStatus struct {
	Primary     bool        `json:"primary,omitempty"`
	IsIndex     bool        `json:"isIndex,omitempty"`
	Type        string      `json:"type"`
	ApproxBlobs int         `json:"approxBlobs,omitempty"`
	ApproxBytes int         `json:"approxBytes,omitempty"`
	ImplStatus  interface{} `json:"implStatus,omitempty"`
}

func (sh *StatusHandler) currentStatus() *status {
	res := &status{
		Version: buildinfo.Version(),
		Storage: make(map[string]storageStatus),
		Sync:    make(map[string]syncStatus),
	}
	if v := os.Getenv("CAMLI_FAKE_STATUS_ERROR"); v != "" {
		res.addError(v, "/status/#fakeerror")
	}
	_, hi, err := sh.handlerFinder.FindHandlerByType("root")
	if err != nil {
		res.addError(fmt.Sprintf("Error finding root handler: %v", err), "")
		return res
	}
	rh := hi.(*RootHandler)
	res.rootPrefix = rh.Prefix

	if pfx, h, err := sh.handlerFinder.FindHandlerByType("importer"); err == nil {
		res.importerRoot = pfx
		as := h.(interface {
			AccountsStatus() (interface{}, []camtypes.StatusError)
		})
		var errs []camtypes.StatusError
		res.ImporterAccounts, errs = as.AccountsStatus()
		res.Errors = append(res.Errors, errs...)
	}

	types, handlers := sh.handlerFinder.AllHandlers()

	// Sync
	for pfx, h := range handlers {
		sh, ok := h.(*SyncHandler)
		if !ok {
			continue
		}
		res.Sync[pfx] = sh.currentStatus()
	}

	// Storage
	for pfx, typ := range types {
		if !strings.HasPrefix(typ, "storage-") {
			continue
		}
		h := handlers[pfx]
		_, isIndex := h.(*index.Index)
		res.Storage[pfx] = storageStatus{
			Type:    strings.TrimPrefix(typ, "storage-"),
			Primary: pfx == rh.BlobRoot,
			IsIndex: isIndex,
		}
	}

	return res
}

func (sh *StatusHandler) serveStatusJSON(rw http.ResponseWriter, req *http.Request) {
	httputil.ReturnJSON(rw, sh.currentStatus())
}

var quotedPrefix = regexp.MustCompile(`[;"]/(\S+?/)[&"]`)

func (sh *StatusHandler) serveStatusHTML(rw http.ResponseWriter, req *http.Request) {
	st := sh.currentStatus()
	f := func(p string, a ...interface{}) {
		fmt.Fprintf(rw, p, a...)
	}
	f("<html><head><title>Status</title></head>")
	f("<body><h2>Status</h2>")
	f("<p>As JSON: <a href='status.json'>status.json</a>; and the <a href='%s?camli.mode=config'>discovery JSON</a>.</p>", st.rootPrefix)
	f("<p>Not yet pretty HTML UI:</p>")
	js, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		log.Printf("JSON marshal error: %v", err)
	}
	jsh := html.EscapeString(string(js))
	jsh = quotedPrefix.ReplaceAllStringFunc(jsh, func(in string) string {
		pfx := in[1 : len(in)-1]
		if st.isHandler(pfx) {
			return fmt.Sprintf("%s<a href='%s'>%s</a>%s", in[:1], pfx, pfx, in[len(in)-1:])
		}
		return in
	})
	f("<pre>%s</pre>", jsh)
}
