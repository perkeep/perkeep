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

package server

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/jsonsign/signhandler"
	uistatic "camlistore.org/server/camlistored/ui"
)

var (
	staticFilePattern = regexp.MustCompile(`^([a-zA-Z0-9\-\_]+\.(html|js|css|png|jpg|gif))$`)
	identOrDotPattern = regexp.MustCompile(`^[a-zA-Z\_]+(\.[a-zA-Z\_]+)*$`)

	// Download URL suffix:
	//   $1: blobref (checked in download handler)
	//   $2: optional "/filename" to be sent as recommended download name,
	//       if sane looking
	downloadPattern = regexp.MustCompile(`^download/([^/]+)(/.*)?$`)

	thumbnailPattern = regexp.MustCompile(`^thumbnail/([^/]+)(/.*)?$`)
	treePattern      = regexp.MustCompile(`^tree/([^/]+)(/.*)?$`)
	closurePattern   = regexp.MustCompile(`^closure/(([^/]+)(/.*)?)$`)
)

var uiFiles = uistatic.Files

// UIHandler handles serving the UI and discovery JSON.
type UIHandler struct {
	// JSONSignRoot is the optional path or full URL to the JSON
	// Signing helper. Only used by the UI and thus necessary if
	// UI is true.
	// TODO(bradfitz): also move this up to the root handler,
	// if we start having clients (like phones) that we want to upload
	// but don't trust to have private signing keys?
	JSONSignRoot string

	PublishRoots map[string]*PublishHandler

	prefix string // of the UI handler itself
	root   *RootHandler
	sigh   *signhandler.Handler // or nil

	Cache blobserver.Storage // or nil
	sc    ScaledImage        // cache for scaled images, optional

	// camliRoot optionally specifies the path to root of Camlistore's
	// source. If empty, the UI files must be compiled in to the
	// binary (with go run make.go).  This comes from the "camliRoot"
	// ui handler config option.
	// TODO: not yet implemented.
	camliRoot string

	// closureHandler serves the Closure JS files.
	closureHandler http.Handler
}

func init() {
	blobserver.RegisterHandlerConstructor("ui", uiFromConfig)
}

func uiFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	ui := &UIHandler{
		prefix:       ld.MyPrefix(),
		JSONSignRoot: conf.OptionalString("jsonSignRoot", ""),
		camliRoot:    conf.OptionalString("camliRoot", ""),
	}
	closureRoot := conf.OptionalString("closureRoot", "")
	pubRoots := conf.OptionalList("publishRoots")
	cachePrefix := conf.OptionalString("cache", "")
	scType := conf.OptionalString("scaledImage", "")
	if err = conf.Validate(); err != nil {
		return
	}

	if ui.JSONSignRoot != "" {
		h, _ := ld.GetHandler(ui.JSONSignRoot)
		if sigh, ok := h.(*signhandler.Handler); ok {
			ui.sigh = sigh
		}
	}

	ui.PublishRoots = make(map[string]*PublishHandler)
	for _, pubRoot := range pubRoots {
		h, err := ld.GetHandler(pubRoot)
		if err != nil {
			return nil, fmt.Errorf("UI handler's publishRoots references invalid %q", pubRoot)
		}
		pubh, ok := h.(*PublishHandler)
		if !ok {
			return nil, fmt.Errorf("UI handler's publishRoots references invalid %q; not a PublishHandler", pubRoot)
		}
		ui.PublishRoots[pubRoot] = pubh
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
			err = fmt.Errorf("UI handler's %q references %q of type %q; expected type %q", key, v, ct, htype)
		}
	}
	checkType("searchRoot", "search")
	checkType("jsonSignRoot", "jsonsign")
	if err != nil {
		return
	}

	if cachePrefix != "" {
		bs, err := ld.GetStorage(cachePrefix)
		if err != nil {
			return nil, fmt.Errorf("UI handler's cache of %q error: %v", cachePrefix, err)
		}
		ui.Cache = bs
		switch scType {
		case "lrucache":
			ui.sc = NewScaledImageLRU()
		default:
			return nil, fmt.Errorf("unsupported ui handler's scType: %q ", scType)
		}
	}

	ui.closureHandler, err = ui.makeClosureHandler(closureRoot)
	if err != nil {
		return nil, fmt.Errorf(`Invalid "closureRoot" value of %q: %v"`, closureRoot, err)
	}

	rootPrefix, _, err := ld.FindHandlerByType("root")
	if err != nil {
		return nil, errors.New("No root handler configured, which is necessary for the ui handler")
	}
	if h, err := ld.GetHandler(rootPrefix); err == nil {
		ui.root = h.(*RootHandler)
		ui.root.registerUIHandler(ui)
	} else {
		return nil, errors.New("failed to find the 'root' handler")
	}

	return ui, nil
}

// makeClosureHandler returns a handler to serve Closure files.
// root is either:
// 1) empty: use the Closure files compiled in to the binar (if available), else redirect to the Internet.
// 2) a URL prefix: base of Closure to redirect to
// 3) a path on disk to serve files from
func (ui *UIHandler) makeClosureHandler(root string) (http.Handler, error) {
	// In development mode, serve the Closure files from disk directly.
	if root == "" {
		// TODO: see if they're compiled in, and serve from that.

		// But for now, assume a redirector to their current location
		// on the web.
		return closureBaseURL, nil
	}
	if strings.HasPrefix(root, "http") {
		return closureRedirector(root), nil
	}
	fi, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, errors.New("not a directory")
	}
	_, err = os.Stat(filepath.Join(root, "closure", "goog", "base.js"))
	if err != nil {
		return nil, fmt.Errorf("directory doesn't contain closure/goog/base.js; wrong directory?")
	}
	return http.FileServer(http.Dir(filepath.Join(root, "closure"))), nil
}

const closureBaseURL closureRedirector = "https://closure-library.googlecode.com/git"

// closureRedirector is a hack to redirect requests for Closure's million *.js files
// to https://closure-library.googlecode.com/git.
// TODO: this doesn't work when offline. We need to run genjsdeps over all of the Camlistore
// UI to figure out which Closure *.js files to fileembed and generate zembed. Then this
// type can be deleted.
type closureRedirector string

func (base closureRedirector) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	newURL := string(base) + "/" + path.Clean(httputil.PathSuffix(req))
	http.Redirect(rw, req, newURL, http.StatusTemporaryRedirect)
}

func camliMode(req *http.Request) string {
	return req.URL.Query().Get("camli.mode")
}

func wantsDiscovery(req *http.Request) bool {
	return req.Method == "GET" &&
		(req.Header.Get("Accept") == "text/x-camli-configuration" ||
			camliMode(req) == "config")
}

func wantsUploadHelper(req *http.Request) bool {
	return req.Method == "POST" && camliMode(req) == "uploadhelper"
}

func wantsRecentPermanodes(req *http.Request) bool {
	return req.Method == "GET" && req.FormValue("mode") == "thumbnails"
}

func wantsPermanode(req *http.Request) bool {
	return req.Method == "GET" && blobref.Parse(req.FormValue("p")) != nil
}

func wantsBlobInfo(req *http.Request) bool {
	return req.Method == "GET" && blobref.Parse(req.FormValue("b")) != nil
}

func wantsFileTreePage(req *http.Request) bool {
	return req.Method == "GET" && blobref.Parse(req.FormValue("d")) != nil
}

func wantsClosure(req *http.Request) bool {
	if req.Method == "GET" {
		suffix := httputil.PathSuffix(req)
		return closurePattern.MatchString(suffix)
	}
	return false
}

func (ui *UIHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	suffix := httputil.PathSuffix(req)

	rw.Header().Set("Vary", "Accept")
	switch {
	case wantsDiscovery(req):
		ui.root.serveDiscovery(rw, req)
	case wantsUploadHelper(req):
		ui.serveUploadHelper(rw, req)
	case strings.HasPrefix(suffix, "download/"):
		ui.serveDownload(rw, req)
	case strings.HasPrefix(suffix, "thumbnail/"):
		ui.serveThumbnail(rw, req)
	case strings.HasPrefix(suffix, "tree/"):
		ui.serveFileTree(rw, req)
	case wantsClosure(req):
		ui.serveClosure(rw, req)
	default:
		file := ""
		if m := staticFilePattern.FindStringSubmatch(suffix); m != nil {
			file = m[1]
		} else {
			switch {
			case wantsPermanode(req):
				file = "permanode.html"
			case wantsBlobInfo(req):
				file = "blobinfo.html"
			case wantsFileTreePage(req):
				file = "filetree.html"
			case req.URL.Path == httputil.PathBase(req):
				file = "index.html"
			default:
				http.Error(rw, "Illegal URL.", http.StatusNotFound)
				return
			}
		}
		if file == "deps.js" {
			envVar := uiFiles.OverrideEnv
			if envVar != "" && os.Getenv(envVar) != "" {
				serveDepsJS(rw, req)
				return
			}
		}
		serveStaticFile(rw, req, uiFiles, file)
	}
}

func serveStaticFile(rw http.ResponseWriter, req *http.Request, root http.FileSystem, file string) {
	f, err := root.Open("/" + file)
	if err != nil {
		http.NotFound(rw, req)
		log.Printf("Failed to open file %q from uiFiles: %v", file, err)
		return
	}
	defer f.Close()
	var modTime time.Time
	if fi, err := f.Stat(); err == nil {
		modTime = fi.ModTime()
	}
	http.ServeContent(rw, req, file, modTime, f)
}

func (ui *UIHandler) populateDiscoveryMap(m map[string]interface{}) {
	pubRoots := map[string]interface{}{}
	for key, pubh := range ui.PublishRoots {
		m := map[string]interface{}{
			"name":   pubh.RootName,
			"prefix": []string{key},
			// TODO: include gpg key id
		}
		if sh, ok := ui.root.SearchHandler(); ok {
			pn, err := sh.Index().PermanodeOfSignerAttrValue(sh.Owner(), "camliRoot", pubh.RootName)
			if err == nil {
				m["currentPermanode"] = pn.String()
			}
		}
		pubRoots[pubh.RootName] = m
	}

	uiDisco := map[string]interface{}{
		"jsonSignRoot":    ui.JSONSignRoot,
		"uploadHelper":    ui.prefix + "?camli.mode=uploadhelper", // hack; remove with better javascript
		"downloadHelper":  path.Join(ui.prefix, "download") + "/",
		"directoryHelper": path.Join(ui.prefix, "tree") + "/",
		"publishRoots":    pubRoots,
	}
	if ui.sigh != nil {
		uiDisco["signing"] = ui.sigh.DiscoveryMap(ui.JSONSignRoot)
	}
	for k, v := range uiDisco {
		if _, ok := m[k]; ok {
			log.Fatalf("Duplicate discovery key %q", k)
		}
		m[k] = v
	}
}

func (ui *UIHandler) serveDownload(rw http.ResponseWriter, req *http.Request) {
	if ui.root.Storage == nil {
		http.Error(rw, "No BlobRoot configured", 500)
		return
	}

	suffix := httputil.PathSuffix(req)
	m := downloadPattern.FindStringSubmatch(suffix)
	if m == nil {
		httputil.ErrorRouting(rw, req)
		return
	}

	fbr := blobref.Parse(m[1])
	if fbr == nil {
		http.Error(rw, "Invalid blobref", 400)
		return
	}

	dh := &DownloadHandler{
		Fetcher: ui.root.Storage,
		Cache:   ui.Cache,
	}
	dh.ServeHTTP(rw, req, fbr)
}

func (ui *UIHandler) serveThumbnail(rw http.ResponseWriter, req *http.Request) {
	if ui.root.Storage == nil {
		http.Error(rw, "No BlobRoot configured", 500)
		return
	}

	suffix := httputil.PathSuffix(req)
	m := thumbnailPattern.FindStringSubmatch(suffix)
	if m == nil {
		httputil.ErrorRouting(rw, req)
		return
	}

	query := req.URL.Query()
	width, err := strconv.Atoi(query.Get("mw"))
	if err != nil {
		http.Error(rw, "Invalid specified max width 'mw'", 500)
		return
	}
	height, err := strconv.Atoi(query.Get("mh"))
	if err != nil {
		http.Error(rw, "Invalid specified height 'mh'", 500)
		return
	}

	blobref := blobref.Parse(m[1])
	if blobref == nil {
		http.Error(rw, "Invalid blobref", 400)
		return
	}

	th := &ImageHandler{
		Fetcher:   ui.root.Storage,
		Cache:     ui.Cache,
		MaxWidth:  width,
		MaxHeight: height,
		sc:        ui.sc,
	}
	th.ServeHTTP(rw, req, blobref)
}

func (ui *UIHandler) serveFileTree(rw http.ResponseWriter, req *http.Request) {
	if ui.root.Storage == nil {
		http.Error(rw, "No BlobRoot configured", 500)
		return
	}

	suffix := httputil.PathSuffix(req)
	m := treePattern.FindStringSubmatch(suffix)
	if m == nil {
		httputil.ErrorRouting(rw, req)
		return
	}

	blobref := blobref.Parse(m[1])
	if blobref == nil {
		http.Error(rw, "Invalid blobref", 400)
		return
	}

	fth := &FileTreeHandler{
		Fetcher: ui.root.Storage,
		file:    blobref,
	}
	fth.ServeHTTP(rw, req)
}

func (ui *UIHandler) serveClosure(rw http.ResponseWriter, req *http.Request) {
	suffix := httputil.PathSuffix(req)
	if ui.closureHandler == nil {
		log.Printf("%v not served: closure handler is nil", suffix)
		http.NotFound(rw, req)
		return
	}
	m := closurePattern.FindStringSubmatch(suffix)
	if m == nil {
		httputil.ErrorRouting(rw, req)
		return
	}
	req.URL.Path = "/" + m[1]
	ui.closureHandler.ServeHTTP(rw, req)
}

// serveDepsJS serves an auto-generated Closure deps.js file.
func serveDepsJS(rw http.ResponseWriter, req *http.Request) {
	envVar := uiFiles.OverrideEnv
	if envVar == "" {
		log.Printf("No uiFiles.OverrideEnv set; can't generate deps.js")
		http.NotFound(rw, req)
		return
	}
	dir := os.Getenv(envVar)
	if dir == "" {
		log.Printf("The %q environment variable is not set; can't generate deps.js", envVar)
		http.NotFound(rw, req)
		return
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		log.Printf("The %q environment variable points to non-directory %s; can't generate deps.js", envVar, dir)
		http.NotFound(rw, req)
		return
	}
	var buf bytes.Buffer
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !strings.HasSuffix(path, ".js") {
			return nil
		}
		suffix := path[len(dir)+1:]
		prov, req, err := parseProvidesRequires(info, path)
		if err != nil {
			return err
		}
		if len(prov) > 0 {
			fmt.Fprintf(&buf, "goog.addDependency(%q, %v, %v);\n", "../../"+suffix, jsList(prov), jsList(req))
		}
		return nil
	})
	if err != nil {
		log.Printf("Error walking %d generating deps.js: %v", dir, err)
		http.Error(rw, "Server error", 500)
		return
	}
	rw.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	io.Copy(rw, &buf)
}

var provReqRx = regexp.MustCompile(`^goog\.(provide|require)\(['"]([\w\.]+)['"]\)`)

type depCacheItem struct {
	modTime            time.Time
	provides, requires []string
}

var (
	depCacheMu sync.Mutex
	depCache   = map[string]depCacheItem{}
)

func parseProvidesRequires(fi os.FileInfo, path string) (provides, requires []string, err error) {
	mt := fi.ModTime()
	depCacheMu.Lock()
	defer depCacheMu.Unlock()
	if ci := depCache[path]; ci.modTime.Equal(mt) {
		return ci.provides, ci.requires, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	br := bufio.NewReader(f)
	for {
		l, err := br.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		if !strings.HasPrefix(l, "goog.") {
			continue
		}
		m := provReqRx.FindStringSubmatch(l)
		if m != nil {
			if m[1] == "provide" {
				provides = append(provides, m[2])
			} else {
				requires = append(requires, m[2])
			}
		}
	}
	depCache[path] = depCacheItem{provides: provides, requires: requires, modTime: mt}
	return provides, requires, nil
}

// jsList prints a list of strings as JavaScript list.
type jsList []string

func (s jsList) String() string {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, v := range s {
		if i > 0 {
			buf.WriteString(", ")
		}
		fmt.Fprintf(&buf, "%q", v)
	}
	buf.WriteByte(']')
	return buf.String()
}
