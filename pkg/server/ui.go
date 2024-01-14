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
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	closurestatic "perkeep.org/clients/web/embed/closure/lib"
	fontawesomestatic "perkeep.org/clients/web/embed/fontawesome"
	keepystatic "perkeep.org/clients/web/embed/keepy"
	leafletstatic "perkeep.org/clients/web/embed/leaflet"
	lessstatic "perkeep.org/clients/web/embed/less"
	opensansstatic "perkeep.org/clients/web/embed/opensans"
	reactstatic "perkeep.org/clients/web/embed/react"

	"go4.org/jsonconfig"
	"go4.org/syncutil"
	"perkeep.org/internal/closure"
	"perkeep.org/internal/httputil"
	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/cacher"
	"perkeep.org/pkg/constants"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/server/app"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/camtypes"
	uistatic "perkeep.org/server/perkeepd/ui"
	"rsc.io/qr"
)

var (
	staticFilePattern  = regexp.MustCompile(`^([a-zA-Z0-9\-\_\.]+\.(html|js|css|png|jpg|gif|svg))$`)
	identOrDotPattern  = regexp.MustCompile(`^[a-zA-Z\_]+(\.[a-zA-Z\_]+)*$`)
	thumbnailPattern   = regexp.MustCompile(`^thumbnail/([^/]+)(/.*)?$`)
	treePattern        = regexp.MustCompile(`^tree/([^/]+)(/.*)?$`)
	closurePattern     = regexp.MustCompile(`^(closure/([^/]+)(/.*)?)$`)
	lessPattern        = regexp.MustCompile(`^less/(.+)$`)
	reactPattern       = regexp.MustCompile(`^react/(.+)$`)
	leafletPattern     = regexp.MustCompile(`^leaflet/(.+)$`)
	fontawesomePattern = regexp.MustCompile(`^fontawesome/(.+)$`)
	openSansPattern    = regexp.MustCompile(`^opensans/(([^/]+)(/.*)?)$`)
	keepyPattern       = regexp.MustCompile(`^keepy/(.+)$`)

	disableThumbCache, _ = strconv.ParseBool(os.Getenv("CAMLI_DISABLE_THUMB_CACHE"))

	vendorEmbed = filepath.Join("clients", "web", "embed")
)

// UIHandler handles serving the UI and discovery JSON.
type UIHandler struct {
	publishRoots map[string]*publishRoot

	prefix        string // of the UI handler itself
	root          *RootHandler
	search        *search.Handler
	shareImporter *shareImporter // nil if no root.Storage

	// Cache optionally specifies a cache blob server, used for
	// caching image thumbnails and other emphemeral data.
	Cache blobserver.Storage // or nil

	// Limit peak RAM used by concurrent image thumbnail calls.
	resizeSem *syncutil.Sem
	thumbMeta *ThumbMeta // optional thumbnail key->blob.Ref cache

	// sourceRoot optionally specifies the path to root of Perkeep's
	// source. If empty, the UI files must be compiled in to the
	// binary (with go run make.go).  This comes from the "sourceRoot"
	// ui handler config option.
	sourceRoot string

	uiDir string // if sourceRoot != "", this is sourceRoot+"/server/perkeepd/ui"

	closureHandler         http.Handler
	fileLessHandler        http.Handler
	fileReactHandler       http.Handler
	fileLeafletHandler     http.Handler
	fileFontawesomeHandler http.Handler
	fileOpenSansHandler    http.Handler
	fileKeepyHandler       http.Handler

	// Embed Filesystems.
	// Some of them may point to the disk.
	uiFiles                fs.FS
	serverFiles            fs.FS
	lessStaticFiles        fs.FS
	reactStaticFiles       fs.FS
	leafletStaticFiles     fs.FS
	keepyStaticFiles       fs.FS
	fontawesomeStaticFiles fs.FS
	opensansStaticFiles    fs.FS
}

func init() {
	blobserver.RegisterHandlerConstructor("ui", uiFromConfig)
}

// newKVOrNil wraps sorted.NewKeyValue and adds the ability
// to pass a nil conf to get a (nil, nil) response.
func newKVOrNil(conf jsonconfig.Obj) (sorted.KeyValue, error) {
	if len(conf) == 0 {
		return nil, nil
	}
	return sorted.NewKeyValueMaybeWipe(conf)
}

func uiFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	ui := &UIHandler{
		prefix:     ld.MyPrefix(),
		sourceRoot: conf.OptionalString("sourceRoot", ""),
		resizeSem: syncutil.NewSem(int64(conf.OptionalInt("maxResizeBytes",
			constants.DefaultMaxResizeMem))),

		serverFiles:            Files,
		uiFiles:                uistatic.Files,
		lessStaticFiles:        lessstatic.Files,
		reactStaticFiles:       reactstatic.Files,
		leafletStaticFiles:     leafletstatic.Files,
		keepyStaticFiles:       keepystatic.Files,
		fontawesomeStaticFiles: fontawesomestatic.Files,
		opensansStaticFiles:    opensansstatic.Files,
	}
	cachePrefix := conf.OptionalString("cache", "")
	scaledImageConf := conf.OptionalObject("scaledImage")
	if err = conf.Validate(); err != nil {
		return
	}

	scaledImageKV, err := newKVOrNil(scaledImageConf)
	if err != nil {
		return nil, fmt.Errorf("in UI handler's scaledImage: %v", err)
	}
	if scaledImageKV != nil && cachePrefix == "" {
		return nil, fmt.Errorf("in UI handler, can't specify scaledImage without cache")
	}
	if cachePrefix != "" {
		bs, err := ld.GetStorage(cachePrefix)
		if err != nil {
			return nil, fmt.Errorf("UI handler's cache of %q error: %v", cachePrefix, err)
		}
		ui.Cache = bs
		ui.thumbMeta = NewThumbMeta(scaledImageKV)
	}

	if ui.sourceRoot == "" {
		ui.sourceRoot = os.Getenv("CAMLI_DEV_CAMLI_ROOT")
		if ui.sourceRoot == "" {
			files, err := uistatic.Files.ReadDir(".")
			if err != nil {
				return nil, fmt.Errorf("Could not read static files: %v", err)
			}
			if len(files) == 0 {
				ui.sourceRoot, err = osutil.PkSourceRoot()
				if err != nil {
					log.Printf("Warning: server not compiled with linked-in UI resources (HTML, JS, CSS), and source root folder not found: %v", err)
				} else {
					log.Printf("Using UI resources (HTML, JS, CSS) from disk, under %v", ui.sourceRoot)
				}
			}
		}
	}
	if ui.sourceRoot != "" {
		ui.uiDir = filepath.Join(ui.sourceRoot, filepath.FromSlash("server/perkeepd/ui"))
		// Ignore any fileembed files:
		ui.serverFiles = os.DirFS(filepath.Join(ui.sourceRoot, filepath.FromSlash("pkg/server")))
		ui.uiFiles = os.DirFS(ui.uiDir)
	}

	ui.closureHandler, err = ui.makeClosureHandler(ui.sourceRoot)
	if err != nil {
		return nil, fmt.Errorf(`Invalid "sourceRoot" value of %q: %v"`, ui.sourceRoot, err)
	}

	if ui.sourceRoot != "" {
		ui.fileReactHandler, err = makeFileServer(ui.sourceRoot, filepath.Join(vendorEmbed, "react"), "react-dom.min.js")
		if err != nil {
			return nil, fmt.Errorf("Could not make react handler: %s", err)
		}
		ui.fileLeafletHandler, err = makeFileServer(ui.sourceRoot, filepath.Join(vendorEmbed, "leaflet"), "leaflet.js")
		if err != nil {
			return nil, fmt.Errorf("Could not make leaflet handler: %s", err)
		}
		ui.fileKeepyHandler, err = makeFileServer(ui.sourceRoot, filepath.Join(vendorEmbed, "keepy"), "keepy-dancing.png")
		if err != nil {
			return nil, fmt.Errorf("Could not make keepy handler: %s", err)
		}
		ui.fileFontawesomeHandler, err = makeFileServer(ui.sourceRoot, filepath.Join(vendorEmbed, "fontawesome"), "css/font-awesome.css")
		if err != nil {
			return nil, fmt.Errorf("Could not make fontawesome handler: %s", err)
		}
		ui.fileLessHandler, err = makeFileServer(ui.sourceRoot, filepath.Join(vendorEmbed, "less"), "less.js")
		if err != nil {
			return nil, fmt.Errorf("Could not make less handler: %s", err)
		}
		ui.fileOpenSansHandler, err = makeFileServer(ui.sourceRoot, filepath.Join(vendorEmbed, "opensans"), "OpenSans.css")
		if err != nil {
			return nil, fmt.Errorf("Could not make Open Sans handler: %s", err)
		}
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

	if ui.root.Storage != nil {
		ui.shareImporter = &shareImporter{
			dest: ui.root.Storage,
		}
	}

	return ui, nil
}

type publishRoot struct {
	Name      string
	Permanode blob.Ref
	Prefix    string
}

// InitHandler goes through all the other configured handlers to discover
// the publisher ones, and uses them to populate ui.publishRoots.
func (ui *UIHandler) InitHandler(hl blobserver.FindHandlerByTyper) error {
	// InitHandler is called after all handlers have been setup, so the bootstrap
	// of the camliRoot node for publishers in dev-mode is already done.
	searchPrefix, _, err := hl.FindHandlerByType("search")
	if err != nil {
		return errors.New("No search handler configured, which is necessary for the ui handler")
	}
	var sh *search.Handler
	htype, hi := hl.AllHandlers()
	if h, ok := hi[searchPrefix]; !ok {
		return errors.New("failed to find the \"search\" handler")
	} else {
		sh = h.(*search.Handler)
		ui.search = sh
	}
	camliRootQuery := func(camliRoot string) (*search.SearchResult, error) {
		return sh.Query(context.TODO(), &search.SearchQuery{
			Limit: 1,
			Constraint: &search.Constraint{
				Permanode: &search.PermanodeConstraint{
					Attr:  "camliRoot",
					Value: camliRoot,
				},
			},
		})
	}
	for prefix, typ := range htype {
		if typ != "app" {
			continue
		}
		ah, ok := hi[prefix].(*app.Handler)
		if !ok {
			panic(fmt.Sprintf("UI: handler for %v has type \"app\" but is not app.Handler", prefix))
		}
		// TODO(mpl): this check is weak, as the user could very well
		// use another binary name for the publisher app. We should
		// introduce/use another identifier.
		if ah.ProgramName() != "publisher" {
			continue
		}
		appConfig := ah.AppConfig()
		if appConfig == nil {
			log.Printf("UI: app handler for %v has no appConfig", prefix)
			continue
		}
		camliRoot, ok := appConfig["camliRoot"].(string)
		if !ok {
			log.Printf("UI: camliRoot in appConfig is %T, want string", appConfig["camliRoot"])
			continue
		}
		result, err := camliRootQuery(camliRoot)
		if err != nil {
			log.Printf("UI: could not find permanode for camliRoot %v: %v", camliRoot, err)
			continue
		}
		if len(result.Blobs) == 0 || !result.Blobs[0].Blob.Valid() {
			log.Printf("UI: no valid permanode for camliRoot %v", camliRoot)
			continue
		}
		if ui.publishRoots == nil {
			ui.publishRoots = make(map[string]*publishRoot)
		}
		ui.publishRoots[prefix] = &publishRoot{
			Name:      camliRoot,
			Prefix:    prefix,
			Permanode: result.Blobs[0].Blob,
		}
	}
	return nil
}

func (ui *UIHandler) makeClosureHandler(root string) (http.Handler, error) {
	return makeClosureHandler(root, "ui")
}

// makeClosureHandler returns a handler to serve Closure files.
// root is either:
//  1. empty: use the Closure files compiled in to the binary (if
//     available), else redirect to the Internet.
//  2. a URL prefix: base of Perkeep to get Closure to redirect to
//  3. a path on disk to the root of camlistore's source (which
//     contains the necessary subset of Closure files)
func makeClosureHandler(root, handlerName string) (http.Handler, error) {
	// devcam server environment variable takes precedence:
	if d := os.Getenv("CAMLI_DEV_CLOSURE_DIR"); d != "" {
		log.Printf("%v: serving Closure from devcam server's $CAMLI_DEV_CLOSURE_DIR: %v", handlerName, d)
		return http.FileServer(http.Dir(d)), nil
	}
	if root == "" {
		fs := closurestatic.Closure
		log.Printf("%v: serving Closure from embedded resources", handlerName)
		return http.FileServer(http.FS(fs)), nil
	}
	if strings.HasPrefix(root, "http") {
		log.Printf("%v: serving Closure using redirects to %v", handlerName, root)
		return closureRedirector(root), nil
	}

	path := filepath.Join(vendorEmbed, "closure", "lib", "closure")
	return makeFileServer(root, path, filepath.Join("goog", "base.js"))
}

func makeFileServer(sourceRoot string, pathToServe string, expectedContentPath string) (http.Handler, error) {
	fi, err := os.Stat(sourceRoot)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, errors.New("not a directory")
	}
	dirToServe := filepath.Join(sourceRoot, pathToServe)
	_, err = os.Stat(filepath.Join(dirToServe, expectedContentPath))
	if err != nil {
		return nil, fmt.Errorf("directory doesn't contain %s; wrong directory?", expectedContentPath)
	}
	return http.FileServer(http.Dir(dirToServe)), nil
}

// closureRedirector is a hack to redirect requests for Closure's million *.js files
// to https://closure-library.googlecode.com/git.
// TODO: this doesn't work when offline. We need to run genjsdeps over all of the Perkeep
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

func wantsBlobRef(req *http.Request) bool {
	_, ok := blob.ParseKnown(httputil.PathSuffix(req))
	return ok
}

func wantsDiscovery(req *http.Request) bool {
	return httputil.IsGet(req) &&
		(req.Header.Get("Accept") == "text/x-camli-configuration" ||
			camliMode(req) == "config")
}

func wantsUploadHelper(req *http.Request) bool {
	return req.Method == "POST" && camliMode(req) == "uploadhelper"
}

func wantsPermanode(req *http.Request) bool {
	return httputil.IsGet(req) && blob.ValidRefString(req.FormValue("p"))
}

func wantsBlobInfo(req *http.Request) bool {
	return httputil.IsGet(req) && blob.ValidRefString(req.FormValue("b"))
}

func getSuffixMatches(req *http.Request, pattern *regexp.Regexp) bool {
	if httputil.IsGet(req) {
		suffix := httputil.PathSuffix(req)
		return pattern.MatchString(suffix)
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
	case strings.HasPrefix(suffix, "importshare"):
		ui.importShare(rw, req)
	case strings.HasPrefix(suffix, "thumbnail/"):
		ui.serveThumbnail(rw, req)
	case strings.HasPrefix(suffix, "tree/"):
		ui.serveFileTree(rw, req)
	case strings.HasPrefix(suffix, "qr/"):
		ui.serveQR(rw, req)
	case getSuffixMatches(req, closurePattern):
		ui.serveClosure(rw, req)
	case getSuffixMatches(req, lessPattern):
		ui.serveFromDiskOrStatic(rw, req, lessPattern, ui.fileLessHandler, ui.lessStaticFiles)
	case getSuffixMatches(req, reactPattern):
		ui.serveFromDiskOrStatic(rw, req, reactPattern, ui.fileReactHandler, ui.reactStaticFiles)
	case getSuffixMatches(req, leafletPattern):
		ui.serveFromDiskOrStatic(rw, req, leafletPattern, ui.fileLeafletHandler, ui.leafletStaticFiles)
	case getSuffixMatches(req, keepyPattern):
		ui.serveFromDiskOrStatic(rw, req, keepyPattern, ui.fileKeepyHandler, ui.keepyStaticFiles)
	case getSuffixMatches(req, fontawesomePattern):
		ui.serveFromDiskOrStatic(rw, req, fontawesomePattern, ui.fileFontawesomeHandler, ui.fontawesomeStaticFiles)
	case getSuffixMatches(req, openSansPattern):
		ui.serveFromDiskOrStatic(rw, req, openSansPattern, ui.fileOpenSansHandler, ui.opensansStaticFiles)
	default:
		file := ""
		if m := staticFilePattern.FindStringSubmatch(suffix); m != nil {
			file = m[1]
		} else {
			switch {
			case wantsBlobRef(req):
				file = "index.html"
			case wantsPermanode(req):
				file = "permanode.html"
			case wantsBlobInfo(req):
				file = "blobinfo.html"
			case req.URL.Path == httputil.PathBase(req):
				file = "index.html"
			default:
				http.Error(rw, "Illegal URL.", http.StatusNotFound)
				return
			}
		}
		if file == "deps.js" {
			serveDepsJS(rw, req, ui.uiDir)
			return
		}
		ServeStaticFile(rw, req, ui.uiFiles, file)
	}
}

// ServeStaticFile serves file from the root virtual filesystem.
func ServeStaticFile(rw http.ResponseWriter, req *http.Request, root fs.FS, file string) {
	f, err := root.Open(file)
	if err != nil {
		http.NotFound(rw, req)
		log.Printf("Failed to open file %q from embedded resources: %v", file, err)
		return
	}
	defer f.Close()
	var modTime time.Time
	if fi, err := f.Stat(); err == nil {
		modTime = fi.ModTime()
	}
	http.ServeContent(rw, req, file, modTime, f.(io.ReadSeeker))
}

func (ui *UIHandler) discovery() *camtypes.UIDiscovery {
	pubRoots := map[string]*camtypes.PublishRootDiscovery{}
	for _, v := range ui.publishRoots {
		rd := &camtypes.PublishRootDiscovery{
			Name:             v.Name,
			Prefix:           []string{v.Prefix},
			CurrentPermanode: v.Permanode,
		}
		pubRoots[v.Name] = rd
	}

	mapClustering, _ := strconv.ParseBool(os.Getenv("CAMLI_DEV_MAP_CLUSTERING"))
	uiDisco := &camtypes.UIDiscovery{
		UIRoot:          ui.prefix,
		UploadHelper:    ui.prefix + "?camli.mode=uploadhelper",
		DownloadHelper:  path.Join(ui.prefix, "download") + "/",
		DirectoryHelper: path.Join(ui.prefix, "tree") + "/",
		PublishRoots:    pubRoots,
		MapClustering:   mapClustering,
		ImportShare:     path.Join(ui.prefix, "importshare") + "/",
	}
	return uiDisco
}

func (ui *UIHandler) serveDownload(w http.ResponseWriter, r *http.Request) {
	if ui.root.Storage == nil {
		http.Error(w, "No BlobRoot configured", 500)
		return
	}

	dh := &DownloadHandler{
		// TODO(mpl): for more efficiency, the cache itself should be a
		// blobpacked, or really anything better optimized for file reading
		// than a blobserver.localdisk (which is what ui.Cache most likely is).
		Fetcher: cacher.NewCachingFetcher(ui.Cache, ui.root.Storage),
		Search:  ui.search,
	}
	dh.ServeHTTP(w, r)
}

func (ui *UIHandler) importShare(w http.ResponseWriter, r *http.Request) {
	if ui.shareImporter == nil {
		http.Error(w, "No ShareImporter capacity", 500)
		return
	}
	ui.shareImporter.ServeHTTP(w, r)
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
	width, _ := strconv.Atoi(query.Get("mw"))
	height, _ := strconv.Atoi(query.Get("mh"))
	blobref, ok := blob.Parse(m[1])
	if !ok {
		http.Error(rw, "Invalid blobref", http.StatusBadRequest)
		return
	}

	if width == 0 {
		width = search.MaxImageSize
	}
	if height == 0 {
		height = search.MaxImageSize
	}

	th := &ImageHandler{
		Fetcher:   ui.root.Storage,
		Cache:     ui.Cache,
		MaxWidth:  width,
		MaxHeight: height,
		ThumbMeta: ui.thumbMeta,
		ResizeSem: ui.resizeSem,
		Search:    ui.search,
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

	blobref, ok := blob.Parse(m[1])
	if !ok {
		http.Error(rw, "Invalid blobref", http.StatusBadRequest)
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

// serveFromDiskOrStatic matches rx against req's path and serves the match either from disk (if non-nil) or from static (embedded in the binary).
func (ui *UIHandler) serveFromDiskOrStatic(rw http.ResponseWriter, req *http.Request, rx *regexp.Regexp,
	disk http.Handler, static fs.FS) {
	suffix := httputil.PathSuffix(req)
	m := rx.FindStringSubmatch(suffix)
	if m == nil {
		panic("Caller should verify that rx matches")
	}
	file := m[1]
	if disk != nil {
		req.URL.Path = "/" + file
		disk.ServeHTTP(rw, req)
	} else {
		ServeStaticFile(rw, req, static, file)
	}

}

func (ui *UIHandler) serveQR(rw http.ResponseWriter, req *http.Request) {
	url := req.URL.Query().Get("url")
	if url == "" {
		http.Error(rw, "Missing url parameter.", http.StatusBadRequest)
		return
	}
	code, err := qr.Encode(url, qr.L)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "image/png")
	rw.Write(code.PNG())
}

// serveDepsJS serves an auto-generated Closure deps.js file.
func serveDepsJS(rw http.ResponseWriter, req *http.Request, dir string) {
	var root http.FileSystem
	if dir == "" {
		root = http.FS(uistatic.Files)
	} else {
		root = http.Dir(dir)
	}

	b, err := closure.GenDeps(root)
	if err != nil {
		log.Print(err)
		http.Error(rw, "Server error", 500)
		return
	}
	rw.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	rw.Write([]byte("// auto-generated from perkeepd\n"))
	rw.Write(b)
}
