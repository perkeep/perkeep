/*
Copyright 2011 The Perkeep Authors

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
	"sort"
	"sync"

	"perkeep.org/internal/httputil"
	"perkeep.org/internal/images"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/buildinfo"
	"perkeep.org/pkg/jsonsign/signhandler"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/types/camtypes"

	"go4.org/jsonconfig"
	"go4.org/types"
)

// RootHandler handles serving the about/splash page.
type RootHandler struct {
	// Stealth determines whether we hide from non-authenticated
	// clients.
	Stealth bool

	OwnerName string // for display purposes only.
	Username  string // default user for mobile setup.

	// URL prefixes (path or full URL) to the primary blob and
	// search root.
	BlobRoot     string
	SearchRoot   string
	helpRoot     string
	importerRoot string
	statusRoot   string
	Prefix       string // root handler's prefix
	shareRoot    string // share handler's prefix, if any.

	// JSONSignRoot is the optional path or full URL to the JSON
	// Signing helper.
	JSONSignRoot string

	Storage blobserver.Storage // of BlobRoot, or nil

	searchInitOnce sync.Once // runs searchInit, which populates searchHandler
	searchInit     func()
	searchHandler  *search.Handler // of SearchRoot, or nil
	hasLegacySHA1  bool            // whether the index has SHA1 blobs. requires searchHandler.

	ui   *UIHandler           // or nil, if none configured
	sigh *signhandler.Handler // or nil, if none configured
	sync []*SyncHandler       // list of configured sync handlers, for discovery.
}

func (rh *RootHandler) SearchHandler() (h *search.Handler, ok bool) {
	rh.searchInitOnce.Do(rh.searchInit)
	return rh.searchHandler, rh.searchHandler != nil
}

func init() {
	blobserver.RegisterHandlerConstructor("root", newRootFromConfig)
}

func newRootFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	checkType := func(key string, htype string) {
		v := conf.OptionalString(key, "")
		if v == "" {
			return
		}
		ct := ld.GetHandlerType(v)
		if ct == "" {
			err = fmt.Errorf("root handler's %q references non-existent %q", key, v)
		} else if ct != htype {
			err = fmt.Errorf("root handler's %q references %q of type %q; expected type %q", key, v, ct, htype)
		}
	}
	checkType("searchRoot", "search")
	checkType("jsonSignRoot", "jsonsign")
	if err != nil {
		return
	}
	username, _ := getUserName()
	root := &RootHandler{
		BlobRoot:     conf.OptionalString("blobRoot", ""),
		SearchRoot:   conf.OptionalString("searchRoot", ""),
		JSONSignRoot: conf.OptionalString("jsonSignRoot", ""),
		OwnerName:    conf.OptionalString("ownerName", username),
		Username:     osutil.Username(),
		Prefix:       ld.MyPrefix(),
	}
	root.Stealth = conf.OptionalBool("stealth", false)
	root.statusRoot = conf.OptionalString("statusRoot", "")
	root.helpRoot = conf.OptionalString("helpRoot", "")
	root.shareRoot = conf.OptionalString("shareRoot", "")
	if err = conf.Validate(); err != nil {
		return
	}

	if root.BlobRoot != "" {
		bs, err := ld.GetStorage(root.BlobRoot)
		if err != nil {
			return nil, fmt.Errorf("Root handler's blobRoot of %q error: %v", root.BlobRoot, err)
		}
		root.Storage = bs
	}

	if root.JSONSignRoot != "" {
		h, _ := ld.GetHandler(root.JSONSignRoot)
		if sigh, ok := h.(*signhandler.Handler); ok {
			root.sigh = sigh
		}
	}

	root.searchInit = func() {}
	if root.SearchRoot != "" {
		prefix := root.SearchRoot
		if t := ld.GetHandlerType(prefix); t != "search" {
			if t == "" {
				return nil, fmt.Errorf("root handler's searchRoot of %q is invalid and doesn't refer to a declared handler", prefix)
			}
			return nil, fmt.Errorf("root handler's searchRoot of %q is of type %q, not %q", prefix, t, "search")
		}
		root.searchInit = func() {
			h, err := ld.GetHandler(prefix)
			if err != nil {
				log.Fatalf("Error fetching SearchRoot at %q: %v", prefix, err)
			}
			root.searchHandler = h.(*search.Handler)
			// the result from root.searchHandler.HasLegacySHA1() is determined on index
			// startup, and never changes during the server's lifetime, so we might as well
			// cache it here too.
			root.hasLegacySHA1 = root.searchHandler.HasLegacySHA1()
			root.searchInit = nil
		}
	}

	if pfx, _, _ := ld.FindHandlerByType("importer"); err == nil {
		root.importerRoot = pfx
	}

	return root, nil
}

func (rh *RootHandler) registerUIHandler(h *UIHandler) {
	rh.ui = h
}

func (rh *RootHandler) registerSyncHandler(h *SyncHandler) {
	rh.sync = append(rh.sync, h)
	sort.Sort(byFromTo(rh.sync))
}

func (rh *RootHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if wantsDiscovery(r) {
		if auth.Allowed(r, auth.OpDiscovery) {
			rh.serveDiscovery(w, r)
			return
		}
		if !rh.Stealth {
			auth.SendUnauthorized(w, r)
		}
		return
	}

	if rh.Stealth {
		return
	}
	if r.RequestURI == "/" && rh.ui != nil {
		http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
		return
	}
	switch r.URL.Path {
	case "/favicon.ico":
		ServeStaticFile(w, r, &Files, "favicon.ico")
		return
	case "/mobile-setup":
		http.Redirect(w, r, "/ui/mobile.html", http.StatusFound)
		return
	case "/":
		break
	default:
		http.NotFound(w, r)
		return
	}

	f := func(p string, a ...any) {
		fmt.Fprintf(w, p, a...)
	}
	f("<html><body><p>This is perkeepd (%s), a "+
		"<a href='http://perkeep.org'>Perkeep</a> server.</p>",
		html.EscapeString(buildinfo.Summary()))
	if rh.ui != nil {
		f("<p>To manage your content, access the <a href='%s'>%s</a>.</p>", rh.ui.prefix, rh.ui.prefix)
	}
	if rh.statusRoot != "" {
		f("<p>To view status, see <a href='%s'>%s</a>.</p>", rh.statusRoot, rh.statusRoot)
	}
	if rh.helpRoot != "" {
		f("<p>To view more information on accessing the server, see <a href='%s'>%s</a>.</p>", rh.helpRoot, rh.helpRoot)
	}
	fmt.Fprintf(w, "</body></html>")
}

type byFromTo []*SyncHandler

func (b byFromTo) Len() int      { return len(b) }
func (b byFromTo) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b byFromTo) Less(i, j int) bool {
	if b[i].fromName < b[j].fromName {
		return true
	}
	return b[i].fromName == b[j].fromName && b[i].toName < b[j].toName
}

func (rh *RootHandler) serveDiscovery(rw http.ResponseWriter, req *http.Request) {
	d := &camtypes.Discovery{
		BlobRoot:     rh.BlobRoot,
		JSONSignRoot: rh.JSONSignRoot,
		HelpRoot:     rh.helpRoot,
		ImporterRoot: rh.importerRoot,
		SearchRoot:   rh.SearchRoot,
		ShareRoot:    rh.shareRoot,
		StatusRoot:   rh.statusRoot,
		OwnerName:    rh.OwnerName,
		UserName:     rh.Username,
		AuthToken:    auth.DiscoveryToken(),
		ThumbVersion: images.ThumbnailVersion(),
	}
	if gener, ok := rh.Storage.(blobserver.Generationer); ok {
		initTime, gen, err := gener.StorageGeneration()
		if err != nil {
			d.StorageGenerationError = err.Error()
		} else {
			d.StorageInitTime = types.Time3339(initTime)
			d.StorageGeneration = gen
		}
	} else {
		log.Printf("Storage type %T is not a blobserver.Generationer; not sending storageGeneration", rh.Storage)
	}
	if rh.ui != nil {
		d.UIDiscovery = rh.ui.discovery()
	}
	if rh.sigh != nil {
		d.Signing = rh.sigh.Discovery(rh.JSONSignRoot)
	}
	if len(rh.sync) > 0 {
		syncHandlers := make([]camtypes.SyncHandlerDiscovery, 0, len(rh.sync))
		for _, sh := range rh.sync {
			syncHandlers = append(syncHandlers, sh.discovery())
		}
		d.SyncHandlers = syncHandlers
	}
	d.HasLegacySHA1Index = rh.hasLegacySHA1
	discoveryHelper(rw, req, d)
}

func discoveryHelper(rw http.ResponseWriter, req *http.Request, dr *camtypes.Discovery) {
	rw.Header().Set("Content-Type", "text/javascript")
	if cb := req.FormValue("cb"); identOrDotPattern.MatchString(cb) {
		fmt.Fprintf(rw, "%s(", cb)
		defer rw.Write([]byte(");\n"))
	} else if v := req.FormValue("var"); identOrDotPattern.MatchString(v) {
		fmt.Fprintf(rw, "%s = ", v)
		defer rw.Write([]byte(";\n"))
	}
	bytes, err := json.MarshalIndent(dr, "", "  ")
	if err != nil {
		httputil.ServeJSONError(rw, httputil.ServerError("encoding discovery information: "+err.Error()))
		return
	}
	rw.Write(bytes)
}
