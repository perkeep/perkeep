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
	"io"
	"log"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"time"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/buildinfo"
	"camlistore.org/pkg/env"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/server/app"
	"camlistore.org/pkg/types/camtypes"
	"go4.org/jsonconfig"
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
	if suffix == "restart" {
		sh.serveRestart(rw, req)
		return
	}
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
		http.Error(rw, "Illegal status path.", http.StatusNotFound)
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
		if len(a) == 0 {
			io.WriteString(rw, p)
		} else {
			fmt.Fprintf(rw, p, a...)
		}
	}
	f("<html><head><title>camlistored status</title></head>")
	f("<body>")

	f("<h1>camlistored status</h1>")

	f("<h2>Versions</h2><ul>")
	var envStr string
	if env.OnGCE() {
		envStr = " (on GCE)"
	}
	f("<li><b>Camlistore</b>: %s%s</li>", html.EscapeString(buildinfo.Version()), envStr)
	f("<li><b>Go</b>: %s/%s %s, cgo=%v</li>", runtime.GOOS, runtime.GOARCH, runtime.Version(), cgoEnabled)
	f("<li><b>djpeg</b>: %s", html.EscapeString(buildinfo.DjpegStatus()))
	f("</ul>")

	f("<h2>Logs</h2><ul>")
	f("  <li><a href='/debug/config'>/debug/config</a> - server config</li>\n")
	if env.OnGCE() {
		f("  <li><a href='/debug/logs/camlistored'>camlistored logs on Google Cloud Logging</a></li>\n")
		f("  <li><a href='/debug/logs/system'>system logs from Google Compute Engine</a></li>\n")
	}
	f("</ul>")

	f("<h2>Admin</h2>")
	f("<form method='post' action='restart' onsubmit='return confirm(\"Really restart now?\")'><button>restart server</button></form>")

	f("<h2>Handlers</h2>")
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

func (sh *StatusHandler) serveRestart(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "POST" {
		http.Error(rw, "POST to restart", http.StatusMethodNotAllowed)
		return
	}

	_, handlers := sh.handlerFinder.AllHandlers()
	for _, h := range handlers {
		ah, ok := h.(*app.Handler)
		if !ok {
			continue
		}
		log.Printf("Sending SIGINT to %s", ah.ProgramName())
		err := ah.Quit()
		if err != nil {
			msg := fmt.Sprintf("Not restarting: couldn't interrupt app %s: %v", ah.ProgramName(), err)
			log.Printf(msg)
			http.Error(rw, msg, http.StatusInternalServerError)
			return
		}
	}

	log.Println("Restarting camlistored")
	rw.Header().Set("Connection", "close")
	http.Redirect(rw, req, sh.prefix, http.StatusFound)
	if f, ok := rw.(http.Flusher); ok {
		f.Flush()
	}
	osutil.RestartProcess()
}

var cgoEnabled bool
