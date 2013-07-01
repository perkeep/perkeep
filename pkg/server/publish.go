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
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/client" // just for NewUploadHandleFromString.  move elsewhere?
	"camlistore.org/pkg/fileembed"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/jsonsign/signhandler"
	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	uistatic "camlistore.org/server/camlistored/ui"
)

// PublishHandler publishes your info to the world, if permanodes have
// appropriate ACLs set.  (everything is private by default)
type PublishHandler struct {
	RootName string
	Search   *search.Handler
	Storage  blobserver.Storage // of blobRoot
	Cache    blobserver.Storage // or nil
	sc       ScaledImage        // cache of scaled images, optional

	JSFiles, CSSFiles []string

	handlerFinder blobserver.FindHandlerByTyper

	// sourceRoot optionally specifies the path to root of Camlistore's
	// source. If empty, the UI files must be compiled in to the
	// binary (with go run make.go).  This comes from the "sourceRoot"
	// publish handler config option.
	sourceRoot string

	uiDir string // if sourceRoot != "", this is sourceRoot+"/server/camlistored/ui"

	// closureHandler serves the Closure JS files.
	closureHandler http.Handler
}

func init() {
	blobserver.RegisterHandlerConstructor("publish", newPublishFromConfig)
}

func newPublishFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err error) {
	ph := &PublishHandler{
		handlerFinder: ld,
	}
	ph.RootName = conf.RequiredString("rootName")
	ph.JSFiles = conf.OptionalList("js")
	ph.CSSFiles = conf.OptionalList("css")
	blobRoot := conf.RequiredString("blobRoot")
	searchRoot := conf.RequiredString("searchRoot")
	cachePrefix := conf.OptionalString("cache", "")
	scType := conf.OptionalString("scaledImage", "")
	bootstrapSignRoot := conf.OptionalString("devBootstrapPermanodeUsing", "")
	rootNode := conf.OptionalList("rootPermanode")
	ph.sourceRoot = conf.OptionalString("sourceRoot", "")
	if err = conf.Validate(); err != nil {
		return
	}

	if ph.RootName == "" {
		return nil, errors.New("invalid empty rootName")
	}

	bs, err := ld.GetStorage(blobRoot)
	if err != nil {
		return nil, fmt.Errorf("publish handler's blobRoot of %q error: %v", blobRoot, err)
	}
	ph.Storage = bs

	si, err := ld.GetHandler(searchRoot)
	if err != nil {
		return nil, fmt.Errorf("publish handler's searchRoot of %q error: %v", searchRoot, err)
	}
	var ok bool
	ph.Search, ok = si.(*search.Handler)
	if !ok {
		return nil, fmt.Errorf("publish handler's searchRoot of %q is of type %T, expecting a search handler",
			searchRoot, si)
	}

	if rootNode != nil {
		if len(rootNode) != 2 {
			return nil, fmt.Errorf("rootPermanode config must contain the jsonSignerHandler and the permanode hash")
		}

		if t := ld.GetHandlerType(rootNode[0]); t != "jsonsign" {
			return nil, fmt.Errorf("publish handler's rootPermanode first value not a jsonsign")
		}
		h, _ := ld.GetHandler(rootNode[0])
		jsonSign := h.(*signhandler.Handler)
		pn := blobref.Parse(rootNode[1])
		if pn == nil {
			return nil, fmt.Errorf("Invalid \"rootPermanode\" value; was expecting a blobRef, got %q.", rootNode[1])
		}
		if err := ph.setRootNode(jsonSign, pn); err != nil {
			return nil, fmt.Errorf("error setting publish root permanode: %v", err)
		}
	} else {
		if bootstrapSignRoot != "" {
			if t := ld.GetHandlerType(bootstrapSignRoot); t != "jsonsign" {
				return nil, fmt.Errorf("publish handler's devBootstrapPermanodeUsing must be of type jsonsign")
			}
			h, _ := ld.GetHandler(bootstrapSignRoot)
			jsonSign := h.(*signhandler.Handler)
			if err := ph.bootstrapPermanode(jsonSign); err != nil {
				return nil, fmt.Errorf("error bootstrapping permanode: %v", err)
			}
		}
	}

	if cachePrefix != "" {
		bs, err := ld.GetStorage(cachePrefix)
		if err != nil {
			return nil, fmt.Errorf("publish handler's cache of %q error: %v", cachePrefix, err)
		}
		ph.Cache = bs
		switch scType {
		case "lrucache":
			ph.sc = NewScaledImageLRU()
		case "":
		default:
			return nil, fmt.Errorf("unsupported publish handler's scType: %q ", scType)
		}
	}

	if ph.sourceRoot == "" {
		ph.sourceRoot = os.Getenv("CAMLI_DEV_CAMLI_ROOT")
	}
	if ph.sourceRoot != "" {
		ph.uiDir = filepath.Join(ph.sourceRoot, filepath.FromSlash("server/camlistored/ui"))
		// Ignore any fileembed files:
		Files = &fileembed.Files{
			DirFallback: filepath.Join(ph.sourceRoot, filepath.FromSlash("pkg/server")),
		}
		uistatic.Files = &fileembed.Files{
			DirFallback: ph.uiDir,
		}
	}

	ph.closureHandler, err = ph.makeClosureHandler(ph.sourceRoot)
	if err != nil {
		return nil, fmt.Errorf(`Invalid "sourceRoot" value of %q: %v"`, ph.sourceRoot, err)
	}

	return ph, nil
}

func (ph *PublishHandler) makeClosureHandler(root string) (http.Handler, error) {
	return makeClosureHandler(root, "publish")
}

func (ph *PublishHandler) rootPermanode() (*blobref.BlobRef, error) {
	// TODO: caching, but this can change over time (though
	// probably rare). might be worth a 5 second cache or
	// something in-memory? better invalidation story first would
	// be nice.
	br, err := ph.Search.Index().PermanodeOfSignerAttrValue(ph.Search.Owner(), "camliRoot", ph.RootName)
	if err != nil {
		log.Printf("Error: publish handler at serving root name %q has no configured permanode: %v",
			ph.RootName, err)
	}
	return br, err
}

func (ph *PublishHandler) lookupPathTarget(root *blobref.BlobRef, suffix string) (*blobref.BlobRef, error) {
	if suffix == "" {
		return root, nil
	}
	path, err := ph.Search.Index().PathLookup(ph.Search.Owner(), root, suffix, time.Time{})
	if err != nil {
		return nil, err
	}
	if path.Target == nil {
		return nil, os.ErrNotExist
	}
	return path.Target, nil
}

func (ph *PublishHandler) serveDiscovery(rw http.ResponseWriter, req *http.Request) {
	if !ph.ViewerIsOwner(req) {
		discoveryHelper(rw, req, map[string]interface{}{
			"error": "viewer isn't owner",
		})
		return
	}
	_, handler, err := ph.handlerFinder.FindHandlerByType("ui")
	if err != nil {
		discoveryHelper(rw, req, map[string]interface{}{
			"error": "no admin handler running",
		})
		return
	}
	ui := handler.(*UIHandler)
	ui.root.serveDiscovery(rw, req)
}

func (ph *PublishHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.URL.Query().Get("camli.mode") == "config" {
		ph.serveDiscovery(rw, req)
		return
	}
	preq := ph.NewRequest(rw, req)
	preq.serveHTTP()
}

// publishRequest is the state around a single HTTP request to the
// publish handler
type publishRequest struct {
	ph                   *PublishHandler
	rw                   http.ResponseWriter
	req                  *http.Request
	base, suffix, subres string
	rootpn               *blobref.BlobRef
	subject              *blobref.BlobRef
	inSubjectChain       map[string]bool // blobref -> true
	subjectBasePath      string

	// A describe request that we can reuse, sharing its map of
	// blobs already described.
	dr *search.DescribeRequest
}

func (ph *PublishHandler) NewRequest(rw http.ResponseWriter, req *http.Request) *publishRequest {
	// splits a path request into its suffix and subresource parts.
	// e.g. /blog/foo/camli/res/file/xxx -> ("foo", "file/xxx")
	suffix, res := httputil.PathSuffix(req), ""
	if strings.HasPrefix(suffix, "-/") {
		suffix, res = "", suffix[2:]
	} else if s := strings.SplitN(suffix, "/-/", 2); len(s) == 2 {
		suffix, res = s[0], s[1]
	}
	rootpn, _ := ph.rootPermanode()
	return &publishRequest{
		ph:              ph,
		rw:              rw,
		req:             req,
		suffix:          suffix,
		base:            httputil.PathBase(req),
		subres:          res,
		rootpn:          rootpn,
		dr:              ph.Search.NewDescribeRequest(),
		inSubjectChain:  make(map[string]bool),
		subjectBasePath: "",
	}
}

func (ph *PublishHandler) ViewerIsOwner(req *http.Request) bool {
	// TODO: better check later
	return strings.HasPrefix(req.RemoteAddr, "127.") ||
		strings.HasPrefix(req.RemoteAddr, "localhost:")
}

func (pr *publishRequest) ViewerIsOwner() bool {
	return pr.ph.ViewerIsOwner(pr.req)
}

func (pr *publishRequest) Debug() bool {
	return pr.req.FormValue("debug") == "1"
}

func (pr *publishRequest) SubresourceType() string {
	if len(pr.subres) >= 3 && strings.HasPrefix(pr.subres, "/=") {
		return pr.subres[2:3]
	}
	return ""
}

func (pr *publishRequest) SubresFileURL(path []*blobref.BlobRef, fileName string) string {
	return pr.SubresThumbnailURL(path, fileName, -1)
}

func (pr *publishRequest) SubresThumbnailURL(path []*blobref.BlobRef, fileName string, maxDimen int) string {
	var buf bytes.Buffer
	resType := "i"
	if maxDimen == -1 {
		resType = "f"
	}
	fmt.Fprintf(&buf, "%s", pr.subjectBasePath)
	if !strings.Contains(pr.subjectBasePath, "/-/") {
		buf.Write([]byte("/-"))
	}
	for _, br := range path {
		if pr.inSubjectChain[br.String()] {
			continue
		}
		fmt.Fprintf(&buf, "/h%s", br.DigestPrefix(10))
	}
	fmt.Fprintf(&buf, "/=%s", resType)
	fmt.Fprintf(&buf, "/%s", url.QueryEscape(fileName))
	if maxDimen != -1 {
		fmt.Fprintf(&buf, "?mw=%d&mh=%d", maxDimen, maxDimen)
	}
	return buf.String()
}

var memberRE = regexp.MustCompile(`^/?h([0-9a-f]+)`)

func (pr *publishRequest) findSubject() error {
	if strings.HasPrefix(pr.suffix, "=s/") {
		pr.subres = "/" + pr.suffix
		return nil
	}

	subject, err := pr.ph.lookupPathTarget(pr.rootpn, pr.suffix)
	if err != nil {
		return err
	}
	if strings.HasPrefix(pr.subres, "=z/") {
		// this happens when we are at the root of the published path,
		// e.g /base/suffix/-/=z/foo.zip
		// so we need to reset subres as fullpath so that it is detected
		// properly when switching on pr.SubresourceType()
		pr.subres = "/" + pr.subres
		// since we return early, we set the subject because that is
		// what is going to be used as a root node by the zip handler.
		pr.subject = subject
		return nil
	}

	pr.inSubjectChain[subject.String()] = true
	pr.subjectBasePath = pr.base + pr.suffix

	// Chase /h<xxxxx> hops in suffix.
	for {
		m := memberRE.FindStringSubmatch(pr.subres)
		if m == nil {
			break
		}
		match, memberPrefix := m[0], m[1]

		if err != nil {
			return fmt.Errorf("Error looking up potential member %q in describe of subject %q: %v",
				memberPrefix, subject, err)
		}

		subject, err = pr.ph.Search.ResolvePrefixHop(subject, memberPrefix)
		if err != nil {
			return err
		}
		pr.inSubjectChain[subject.String()] = true
		pr.subres = pr.subres[len(match):]
		pr.subjectBasePath = addPathComponent(pr.subjectBasePath, match)
	}

	pr.subject = subject
	return nil
}

func (pr *publishRequest) serveHTTP() {
	if pr.rootpn == nil {
		pr.rw.WriteHeader(404)
		return
	}

	if pr.Debug() {
		pr.pf("I am publish handler at base %q, serving root %q (permanode=%s), suffix %q, subreq %q<hr>",
			pr.base, pr.ph.RootName, pr.rootpn, html.EscapeString(pr.suffix), html.EscapeString(pr.subres))
	}

	if err := pr.findSubject(); err != nil {
		if err == os.ErrNotExist {
			pr.rw.WriteHeader(404)
			return
		}
		log.Printf("Error looking up %s/%q: %v", pr.rootpn, pr.suffix, err)
		pr.rw.WriteHeader(500)
		return
	}

	if pr.Debug() {
		pr.pf("<p><b>Subject:</b> <a href='/ui/?p=%s'>%s</a></p>", pr.subject, pr.subject)
		return
	}

	switch pr.SubresourceType() {
	case "":
		pr.serveSubject()
	case "b":
		// TODO: download a raw blob
	case "f": // file download
		pr.serveSubresFileDownload()
	case "i": // image, scaled
		pr.serveSubresImage()
	case "s": // static
		pr.req.URL.Path = pr.subres[len("/=s"):]
		if len(pr.req.URL.Path) <= 1 {
			http.Error(pr.rw, "Illegal URL.", http.StatusNotFound)
			return
		}
		file := pr.req.URL.Path[1:]
		if m := closurePattern.FindStringSubmatch(file); m != nil {
			pr.req.URL.Path = "/" + m[1]
			pr.ph.closureHandler.ServeHTTP(pr.rw, pr.req)
			return
		}
		// TODO: this assumes that deps.js either dev server, or that deps.js
		// is embedded in the binary. We want to NOT embed deps.js, but also
		// serve dynamic deps.js from other resources embedded in the server
		// when not in dev-server mode.  So fix this later, when serveDepsJS
		// can work over embedded resources.
		if file == "deps.js" && pr.ph.sourceRoot != "" {
			serveDepsJS(pr.rw, pr.req, pr.ph.uiDir)
			return
		}
		serveStaticFile(pr.rw, pr.req, uistatic.Files, file)
	case "z":
		pr.serveZip()
	default:
		pr.rw.WriteHeader(400)
		pr.pf("<p>Invalid or unsupported resource request.</p>")
	}
}

func (pr *publishRequest) pf(format string, args ...interface{}) {
	fmt.Fprintf(pr.rw, format, args...)
}

func (pr *publishRequest) staticPath(fileName string) string {
	return pr.base + "=s/" + fileName
}

func addPathComponent(base, addition string) string {
	if !strings.HasPrefix(addition, "/") {
		addition = "/" + addition
	}
	if strings.Contains(base, "/-/") {
		return base + addition
	}
	return base + "/-" + addition
}

func (pr *publishRequest) memberPath(member *blobref.BlobRef) string {
	return addPathComponent(pr.subjectBasePath, "/h"+member.DigestPrefix(10))
}

var provCamliRx = regexp.MustCompile(`^goog\.(provide)\(['"]camlistore\.(.*)['"]\)`)

// camliClosurePage checks if filename is a .js file using closure
// and if yes, if it provides a page in the camlistore namespace.
// It returns that page name, or the empty string otherwise.
func camliClosurePage(filename string) string {
	camliRootPath, err := osutil.GoPackagePath("camlistore.org")
	if err != nil {
		return ""
	}
	fullpath := filepath.Join(camliRootPath, "server", "camlistored", "ui", filename)
	f, err := os.Open(fullpath)
	if err != nil {
		return ""
	}
	defer f.Close()
	br := bufio.NewReader(f)
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			return ""
		}
		if !strings.HasPrefix(l, "goog.") {
			continue
		}
		m := provCamliRx.FindStringSubmatch(l)
		if m != nil {
			return m[2]
		}
	}
	return ""
}

// serveZip streams a zip archive of all the files "under"
// pr.subject. That is, all the files pointed by file permanodes,
// which are directly members of pr.subject or recursively down
// directory permanodes and permanodes members.
func (pr *publishRequest) serveZip() {
	filename := ""
	if len(pr.subres) > len("/=z/") {
		filename = pr.subres[4:]
	}
	zh := &zipHandler{
		fetcher:  pr.ph.Storage,
		search:   pr.ph.Search,
		root:     pr.subject,
		filename: filename,
	}
	zh.ServeHTTP(pr.rw, pr.req)
}

func (pr *publishRequest) serveSubject() {
	dr := pr.ph.Search.NewDescribeRequest()
	dr.Describe(pr.subject, 3)
	res, err := dr.Result()
	if err != nil {
		log.Printf("Errors loading %s, permanode %s: %v, %#v", pr.req.URL, pr.subject, err, err)
		pr.pf("<p>Errors loading.</p>")
		return
	}

	subdes := res[pr.subject.String()]

	if subdes.CamliType == "file" {
		pr.serveFileDownload(subdes)
		return
	}

	title := subdes.Title()

	// HTML header + Javascript
	var camliPage string
	{
		pr.pf("<!doctype html>\n<html>\n<head>\n <title>%s</title>\n", html.EscapeString(title))
		// TODO(mpl): We are only using the first .js file, and expecting it to be
		// using closure. Do we want to be more open about this?
		if len(pr.ph.JSFiles) > 0 {
			camliPage = camliClosurePage(pr.ph.JSFiles[0])
		}

		if camliPage != "" {
			pr.pf(" <script src='%s'></script>\n", pr.staticPath("closure/goog/base.js"))
			pr.pf(" <script src='%s'></script>\n", pr.staticPath("deps.js"))
			if pr.ViewerIsOwner() {
				pr.pf(" <script src='%s'></script>\n", pr.base+"?camli.mode=config&var=CAMLISTORE_CONFIG")
			}
			pr.pf(" <script src='%s'></script>\n", pr.staticPath("base64.js"))
			pr.pf(" <script src='%s'></script>\n", pr.staticPath("Crypto.js"))
			pr.pf(" <script src='%s'></script>\n", pr.staticPath("SHA1.js"))
			pr.pf("<script>\n goog.require('camlistore.%s');\n </script>\n", camliPage)
		}
		for _, filename := range pr.ph.CSSFiles {
			pr.pf(" <link rel='stylesheet' type='text/css' href='%s'>\n", pr.staticPath(filename))
		}

		pr.pf(" <script>\n")
		pr.pf("var camliViewIsOwner = %v;\n", pr.ViewerIsOwner())
		pr.pf("var camliPagePermanode = %q;\n", pr.subject)
		pr.pf("var camliPageMeta = \n")
		json, _ := json.MarshalIndent(res, "", "  ")
		pr.rw.Write(json)
		pr.pf(";\n </script>\n</head>\n<body>\n")
		defer pr.pf("</body>\n</html>\n")
	}

	if title != "" {
		pr.pf("<h1>%s</h1>\n", html.EscapeString(title))
	}

	if cref, ok := subdes.ContentRef(); ok {
		des, err := pr.dr.DescribeSync(cref)
		if err == nil && des.File != nil {
			path := []*blobref.BlobRef{pr.subject, cref}
			downloadURL := pr.SubresFileURL(path, des.File.FileName)
			pr.pf("<div>File: %s, %d bytes, type %s</div>",
				html.EscapeString(des.File.FileName),
				des.File.Size,
				des.File.MIMEType)
			if des.File.IsImage() {
				pr.pf("<a href='%s'><img src='%s'></a>",
					downloadURL,
					pr.SubresThumbnailURL(path, des.File.FileName, 600))
			}
			pr.pf("<div id='%s' class='camlifile'>[<a href='%s'>download</a>]</div>",
				cref.DomID(),
				downloadURL)
		}
	}

	if members := subdes.Members(); len(members) > 0 {
		zipName := ""
		if title == "" {
			zipName = "download.zip"
		} else {
			zipName = html.EscapeString(title) + ".zip"
		}
		subjectPath := pr.subjectBasePath
		if !strings.Contains(subjectPath, "/-/") {
			subjectPath += "/-"
		}
		pr.pf("<div><a href='%s/=z/%s'>%s</a></div>\n", subjectPath, zipName, zipName)
		pr.pf("<ul id='members'>\n")
		for _, member := range members {
			des := member.Description()
			if des != "" {
				des = " - " + des
			}
			var fileLink, thumbnail string
			if path, fileInfo, ok := member.PermanodeFile(); ok {
				fileLink = fmt.Sprintf("<div id='%s' class='camlifile'><a href='%s'>file</a></div>",
					path[len(path)-1].DomID(),
					html.EscapeString(pr.SubresFileURL(path, fileInfo.FileName)),
				)
				if fileInfo.IsImage() {
					thumbnail = fmt.Sprintf("<img src='%s'>", pr.SubresThumbnailURL(path, fileInfo.FileName, 200))
				}
			}
			pr.pf("  <li id='%s'><a href='%s'>%s<span>%s</span></a>%s%s</li>\n",
				member.DomID(),
				pr.memberPath(member.BlobRef),
				thumbnail,
				html.EscapeString(member.Title()),
				des,
				fileLink)
		}
		pr.pf("</ul>\n")
	}

	if camliPage != "" {
		pr.pf("<script>\n")
		pr.pf("var page = new camlistore.%s(CAMLISTORE_CONFIG);\n", camliPage)
		pr.pf("page.decorate(document.body);\n")
		pr.pf("</script>\n")
	}
}

func (pr *publishRequest) validPathChain(path []*blobref.BlobRef) bool {
	bi := pr.subject
	for len(path) > 0 {
		var next *blobref.BlobRef
		next, path = path[0], path[1:]

		desi, err := pr.dr.DescribeSync(bi)
		if err != nil {
			return false
		}
		if !desi.HasSecureLinkTo(next) {
			return false
		}
		bi = next
	}
	return true
}

func (pr *publishRequest) serveSubresImage() {
	params := pr.req.URL.Query()
	mw, _ := strconv.Atoi(params.Get("mw"))
	mh, _ := strconv.Atoi(params.Get("mh"))
	des, err := pr.dr.DescribeSync(pr.subject)
	if err != nil {
		log.Printf("error describing subject %q: %v", pr.subject, err)
		return
	}
	pr.serveScaledImage(des, mw, mh, params.Get("square") == "1")
}

func (pr *publishRequest) serveSubresFileDownload() {
	des, err := pr.dr.DescribeSync(pr.subject)
	if err != nil {
		log.Printf("error describing subject %q: %v", pr.subject, err)
		return
	}
	pr.serveFileDownload(des)
}

func (pr *publishRequest) serveScaledImage(des *search.DescribedBlob, maxWidth, maxHeight int, square bool) {
	fileref, _, ok := pr.fileSchemaRefFromBlob(des)
	if !ok {
		log.Printf("scaled image fail; failed to get file schema from des %q", des.BlobRef)
		return
	}
	th := &ImageHandler{
		Fetcher:   pr.ph.Storage,
		Cache:     pr.ph.Cache,
		MaxWidth:  maxWidth,
		MaxHeight: maxHeight,
		Square:    square,
		sc:        pr.ph.sc,
	}
	th.ServeHTTP(pr.rw, pr.req, fileref)
}

func (pr *publishRequest) serveFileDownload(des *search.DescribedBlob) {
	fileref, fileinfo, ok := pr.fileSchemaRefFromBlob(des)
	if !ok {
		log.Printf("Didn't get file schema from described blob %q", des.BlobRef)
		return
	}
	mime := ""
	if fileinfo != nil && fileinfo.IsImage() {
		mime = fileinfo.MIMEType
	}
	dh := &DownloadHandler{
		Fetcher:   pr.ph.Storage,
		Cache:     pr.ph.Cache,
		ForceMime: mime,
	}
	dh.ServeHTTP(pr.rw, pr.req, fileref)
}

// Given a described blob, optionally follows a camliContent and
// returns the file's schema blobref and its fileinfo (if found).
func (pr *publishRequest) fileSchemaRefFromBlob(des *search.DescribedBlob) (fileref *blobref.BlobRef, fileinfo *search.FileInfo, ok bool) {
	if des == nil {
		http.NotFound(pr.rw, pr.req)
		return
	}
	if des.Permanode != nil {
		// TODO: get "forceMime" attr out of the permanode? or
		// fileName content-disposition?
		if cref := des.Permanode.Attr.Get("camliContent"); cref != "" {
			cbr := blobref.Parse(cref)
			if cbr == nil {
				http.Error(pr.rw, "bogus camliContent", 500)
				return
			}
			des = des.PeerBlob(cbr)
			if des == nil {
				http.Error(pr.rw, "camliContent not a peer in describe", 500)
				return
			}
		}
	}
	if des.CamliType == "file" {
		return des.BlobRef, des.File, true
	}
	http.Error(pr.rw, "failed to find fileSchemaRefFromBlob", 404)
	return
}

func (ph *PublishHandler) signUpload(jsonSign *signhandler.Handler, name string, bb *schema.Builder) (*blobref.BlobRef, error) {
	signed, err := jsonSign.Sign(bb)
	if err != nil {
		return nil, fmt.Errorf("error signing %s: %v", name, err)
	}
	uh := client.NewUploadHandleFromString(signed)
	_, err = ph.Storage.ReceiveBlob(uh.BlobRef, uh.Contents)
	if err != nil {
		return nil, fmt.Errorf("error uploading %s: %v", name, err)
	}
	return uh.BlobRef, nil
}

func (ph *PublishHandler) setRootNode(jsonSign *signhandler.Handler, pn *blobref.BlobRef) (err error) {
	_, err = ph.signUpload(jsonSign, "set-attr camliRoot", schema.NewSetAttributeClaim(pn, "camliRoot", ph.RootName))
	if err != nil {
		return err
	}
	_, err = ph.signUpload(jsonSign, "set-attr title", schema.NewSetAttributeClaim(pn, "title", "Publish root node for "+ph.RootName))
	return err
}

func (ph *PublishHandler) bootstrapPermanode(jsonSign *signhandler.Handler) (err error) {
	if pn, err := ph.Search.Index().PermanodeOfSignerAttrValue(ph.Search.Owner(), "camliRoot", ph.RootName); err == nil {
		log.Printf("Publish root %q using existing permanode %s", ph.RootName, pn)
		return nil
	}
	log.Printf("Publish root %q needs a permanode + claim", ph.RootName)

	pn, err := ph.signUpload(jsonSign, "permanode", schema.NewUnsignedPermanode())
	if err != nil {
		return err
	}
	err = ph.setRootNode(jsonSign, pn)
	return err
}
