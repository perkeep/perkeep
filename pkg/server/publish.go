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
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/client" // just for NewUploadHandleFromString.  move elsewhere?
	"camlistore.org/pkg/fileembed"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/jsonsign/signhandler"
	"camlistore.org/pkg/publish"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/types/camtypes"
	uistatic "camlistore.org/server/camlistored/ui"
)

// PublishHandler publishes your info to the world, if permanodes have
// appropriate ACLs set.  (everything is private by default)
type PublishHandler struct {
	RootName string
	Search   *search.Handler
	Storage  blobserver.Storage // of blobRoot
	Cache    blobserver.Storage // or nil

	thumbMeta *thumbMeta // optional cache of scaled images

	CSSFiles []string
	// goTemplate is the go html template used for publishing.
	goTemplate  *template.Template
	closureName string // Name of the closure object used to decorate the published page.

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
	jsFiles := conf.OptionalList("js")
	ph.CSSFiles = conf.OptionalList("css")
	goTemplateFile := conf.RequiredString("goTemplate")
	blobRoot := conf.RequiredString("blobRoot")
	searchRoot := conf.RequiredString("searchRoot")
	cachePrefix := conf.OptionalString("cache", "")
	scaledImageConf := conf.OptionalObject("scaledImage")
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
		pn, ok := blob.Parse(rootNode[1])
		if !ok {
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

	scaledImageKV, err := newKVOrNil(scaledImageConf)
	if err != nil {
		return nil, fmt.Errorf("in publish handler's scaledImage: %v", err)
	}
	if scaledImageKV != nil && cachePrefix == "" {
		return nil, fmt.Errorf("in publish handler, can't specify scaledImage without cache")
	}
	if cachePrefix != "" {
		bs, err := ld.GetStorage(cachePrefix)
		if err != nil {
			return nil, fmt.Errorf("publish handler's cache of %q error: %v", cachePrefix, err)
		}
		ph.Cache = bs
		ph.thumbMeta = newThumbMeta(scaledImageKV)
	}

	// TODO(mpl): check that it works on appengine too.
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

	ph.goTemplate, err = goTemplate(goTemplateFile)
	if err != nil {
		return nil, err
	}
	ph.setClosureName(jsFiles)

	return ph, nil
}

func goTemplate(templateFile string) (*template.Template, error) {
	if filepath.Base(templateFile) != templateFile {
		hint := fmt.Sprintf("The file should either be embedded or placed in %s.",
			filepath.FromSlash("server/camlistored/ui"))
		return nil, fmt.Errorf("Unsupported path %v for template. %s", templateFile, hint)
	}
	f, err := uistatic.Files.Open(templateFile)
	if err != nil {
		return nil, fmt.Errorf("Could not open template %v: %v", templateFile, err)
	}
	defer f.Close()
	templateBytes, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("Could not read template %v: %v", templateFile, err)
	}

	return template.Must(template.New("subject").Parse(string(templateBytes))), nil
}

// setClosureName sets ph.closureName with the first found closure
// namespace provided in jsFiles.
func (ph *PublishHandler) setClosureName(jsFiles []string) {
	for _, v := range jsFiles {
		if ph.closureName == "" {
			if cl := camliClosurePage(v); cl != "" {
				ph.closureName = cl
				break
			}
		}
	}
}

func (ph *PublishHandler) makeClosureHandler(root string) (http.Handler, error) {
	return makeClosureHandler(root, "publish")
}

func (ph *PublishHandler) rootPermanode() (blob.Ref, error) {
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

func (ph *PublishHandler) lookupPathTarget(root blob.Ref, suffix string) (blob.Ref, error) {
	if suffix == "" {
		return root, nil
	}
	path, err := ph.Search.Index().PathLookup(ph.Search.Owner(), root, suffix, time.Time{})
	if err != nil {
		return blob.Ref{}, err
	}
	if !path.Target.Valid() {
		return blob.Ref{}, os.ErrNotExist
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
	rootpn               blob.Ref
	subject              blob.Ref
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

func (pr *publishRequest) SubresFileURL(path []blob.Ref, fileName string) string {
	return pr.SubresThumbnailURL(path, fileName, -1)
}

func (pr *publishRequest) SubresThumbnailURL(path []blob.Ref, fileName string, maxDimen int) string {
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
	if !pr.rootpn.Valid() {
		pr.rw.WriteHeader(404)
		return
	}

	if pr.Debug() {
		pr.rw.Header().Set("Content-Type", "text/html")
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
		pr.serveSubjectTemplate()
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
		if file == "deps.js" {
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

func (pr *publishRequest) memberPath(member blob.Ref) string {
	return addPathComponent(pr.subjectBasePath, "/h"+member.DigestPrefix(10))
}

var provCamliRx = regexp.MustCompile(`^goog\.(provide)\(['"]camlistore\.(.*)['"]\)`)

// camliClosurePage checks if filename is a .js file using closure
// and if yes, if it provides a page in the camlistore namespace.
// It returns that page name, or the empty string otherwise.
func camliClosurePage(filename string) string {
	f, err := uistatic.Files.Open(filename)
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

const (
	resSeparator = "/-"
	digestPrefix = "h"
	digestLen    = 10
)

var hopRE = regexp.MustCompile(fmt.Sprintf("^/%s([0-9a-f]{%d})", digestPrefix, digestLen))

// publishedPath is a URL suffix path of the kind
// suffix + resSeparator + subresource(s), such as:
// /foo/bar/-/subres1/subres2
type publishedPath string

// splitHops returns a slice containing the subresource(s)
// digests. For example, with /foo/bar/-/he0917e5bcf/h5f46bb454d
// it will yield []string{"e0917e5bcf", "5f46bb454d"}
func (p publishedPath) splitHops() []string {
	ps := string(p)
	var hops []string
	if idx := strings.Index(ps, resSeparator); idx != -1 {
		ps = ps[idx+len(resSeparator):]
	}
	matchLen := 1 + len(digestPrefix) + digestLen
	for {
		m := memberRE.FindStringSubmatch(ps)
		if m == nil {
			break
		}
		hops = append(hops, m[1])
		ps = ps[matchLen:]
	}
	return hops
}

// parent returns the base path and the blobRef of pr.subject's parent.
// It returns an error if pr.subject or pr.subjectBasePath were not set
// properly (with findSubject), or if the parent was not found.
func (pr *publishRequest) parent() (parentPath string, parentBlobRef blob.Ref, err error) {
	if !pr.subject.Valid() {
		return "", blob.Ref{}, errors.New("subject not set")
	}
	if pr.subjectBasePath == "" {
		return "", blob.Ref{}, errors.New("subjectBasePath not set")
	}
	hops := publishedPath(pr.subjectBasePath).splitHops()
	if len(hops) == 0 {
		return "", blob.Ref{}, errors.New("No subresource digest in subjectBasePath")
	}
	subjectDigest := hops[len(hops)-1]
	if subjectDigest != pr.subject.DigestPrefix(digestLen) {
		return "", blob.Ref{}, errors.New("subject digest not in subjectBasePath")
	}
	parentPath = strings.TrimSuffix(pr.subjectBasePath, "/"+digestPrefix+subjectDigest)

	if len(hops) == 1 {
		// the parent is the suffix, not one of the subresource hops
		for br, _ := range pr.inSubjectChain {
			if br != pr.subject.String() {
				parentBlobRef = blob.ParseOrZero(br)
				break
			}
		}
	} else {
		// nested collection(s)
		parentDigest := hops[len(hops)-2]
		for br, _ := range pr.inSubjectChain {
			bref, ok := blob.Parse(br)
			if !ok {
				return "", blob.Ref{}, fmt.Errorf("Could not parse %q as blobRef", br)
			}
			if bref.DigestPrefix(10) == parentDigest {
				parentBlobRef = bref
				break
			}
		}
	}
	if !parentBlobRef.Valid() {
		return "", blob.Ref{}, fmt.Errorf("No parent found for %v", pr.subjectBasePath)
	}
	return parentPath, parentBlobRef, nil
}

func (pr *publishRequest) cssFiles() []string {
	files := []string{}
	for _, filename := range pr.ph.CSSFiles {
		files = append(files, pr.staticPath(filename))
	}
	return files
}

// jsDeps returns the list of paths that should be included
// as javascript files in the published page to enable and use
// additional javascript closure code.
func (pr *publishRequest) jsDeps() []string {
	var js []string
	closureDeps := []string{
		"closure/goog/base.js",
		"deps.js",
		// TODO(mpl): fix the deps generator and/or the SHA1.js etc files so they get into deps.js and we
		// do not even have to include them here. detection fails for them because the provide statement
		// is not at the beginning of the line.
		// Not doing it right away because it might have consequences for the rest of the ui I suppose.
		"base64.js",
		"Crypto.js",
		"SHA1.js",
	}
	for _, v := range closureDeps {
		js = append(js, pr.staticPath(v))
	}
	js = append(js, pr.base+"?camli.mode=config&var=CAMLISTORE_CONFIG")
	return js
}

// subjectHeader returns the PageHeader corresponding to the described subject.
func (pr *publishRequest) subjectHeader(described map[string]*search.DescribedBlob) *publish.PageHeader {
	subdes := described[pr.subject.String()]
	header := &publish.PageHeader{
		Title:    html.EscapeString(subdes.Title()),
		CSSFiles: pr.cssFiles(),
		Meta: func() string {
			jsonRes, _ := json.MarshalIndent(described, "", "  ")
			return string(jsonRes)
		}(),
		Subject: pr.subject.String(),
	}
	header.JSDeps = pr.jsDeps()
	if pr.ph.closureName != "" {
		header.CamliClosure = template.JS("camlistore." + pr.ph.closureName)
	}
	if pr.ViewerIsOwner() {
		header.ViewerIsOwner = true
	}
	return header
}

// subjectFile returns the relevant PageFile if the described subject is a file permanode.
func (pr *publishRequest) subjectFile(described map[string]*search.DescribedBlob) (*publish.PageFile, error) {
	subdes := described[pr.subject.String()]
	contentRef, ok := subdes.ContentRef()
	if !ok {
		return nil, nil
	}
	fileDes, err := pr.dr.DescribeSync(contentRef)
	if err != nil {
		return nil, fmt.Errorf("Could not describe %v: %v", contentRef, err)
	}
	path := []blob.Ref{pr.subject, contentRef}
	downloadURL := pr.SubresFileURL(path, fileDes.File.FileName)
	thumbnailURL := ""
	if fileDes.File.IsImage() {
		thumbnailURL = pr.SubresThumbnailURL(path, fileDes.File.FileName, 600)
	}
	fileName := html.EscapeString(fileDes.File.FileName)
	return &publish.PageFile{
		FileName:     fileName,
		Size:         fileDes.File.Size,
		MIMEType:     fileDes.File.MIMEType,
		IsImage:      fileDes.File.IsImage(),
		DownloadURL:  downloadURL,
		ThumbnailURL: thumbnailURL,
		DomID:        contentRef.DomID(),
		Nav: func() *publish.Nav {
			nv, err := pr.fileNavigation()
			if err != nil {
				log.Print(err)
				return nil
			}
			return nv
		},
	}, nil
}

func (pr *publishRequest) fileNavigation() (*publish.Nav, error) {
	// first get the parent path and blob
	parentPath, parentbr, err := pr.parent()
	if err != nil {
		return nil, fmt.Errorf("Could not get subject %v's parent's info: %v", pr.subject, err)
	}
	parentNav := strings.TrimSuffix(parentPath, resSeparator)
	fileNav := &publish.Nav{
		ParentPath: parentNav,
	}

	// describe the parent so we get the siblings (members of the parent)
	dr := pr.ph.Search.NewDescribeRequest()
	dr.Describe(parentbr, 3)
	parentRes, err := dr.Result()
	if err != nil {
		return nil, fmt.Errorf("Could not \"deeply\" describe subject %v's parent %v: %v", pr.subject, parentbr, err)
	}
	members := parentRes[parentbr.String()].Members()
	if len(members) == 0 {
		return fileNav, nil
	}

	pos := 0
	var prev, next blob.Ref
	for k, member := range members {
		if member.BlobRef.String() == pr.subject.String() {
			pos = k
			break
		}
	}
	if pos > 0 {
		prev = members[pos-1].BlobRef
	}
	if pos < len(members)-1 {
		next = members[pos+1].BlobRef
	}
	if !prev.Valid() && !next.Valid() {
		return fileNav, nil
	}
	if prev.Valid() {
		fileNav.PrevPath = fmt.Sprintf("%s/%s%s", parentPath, digestPrefix, prev.DigestPrefix(10))
	}
	if next.Valid() {
		fileNav.NextPath = fmt.Sprintf("%s/%s%s", parentPath, digestPrefix, next.DigestPrefix(10))
	}
	return fileNav, nil
}

// subjectMembers returns the relevant PageMembers if the described subject is a permanode with members.
func (pr *publishRequest) subjectMembers(resMap map[string]*search.DescribedBlob) (*publish.PageMembers, error) {
	subdes := resMap[pr.subject.String()]
	members := subdes.Members()
	if len(members) == 0 {
		return nil, nil
	}
	zipName := ""
	if title := subdes.Title(); title == "" {
		zipName = "download.zip"
	} else {
		zipName = title + ".zip"
	}
	subjectPath := pr.subjectBasePath
	if !strings.Contains(subjectPath, "/-/") {
		subjectPath += "/-"
	}

	return &publish.PageMembers{
		SubjectPath: subjectPath,
		ZipName:     zipName,
		Members:     members,
		Description: func(member *search.DescribedBlob) string {
			des := member.Description()
			if des != "" {
				des = " - " + des
			}
			return des
		},
		Title: func(member *search.DescribedBlob) string {
			memberTitle := member.Title()
			if memberTitle == "" {
				memberTitle = member.BlobRef.DigestPrefix(10)
			}
			return html.EscapeString(memberTitle)
		},
		Path: func(member *search.DescribedBlob) string {
			return pr.memberPath(member.BlobRef)
		},
		DomID: func(member *search.DescribedBlob) string {
			return member.DomID()
		},
		FileInfo: func(member *search.DescribedBlob) *publish.MemberFileInfo {
			if path, fileInfo, ok := member.PermanodeFile(); ok {
				info := &publish.MemberFileInfo{
					FileName:  fileInfo.FileName,
					FileDomID: path[len(path)-1].DomID(),
					FilePath:  html.EscapeString(pr.SubresFileURL(path, fileInfo.FileName)),
				}
				if fileInfo.IsImage() {
					info.FileThumbnailURL = pr.SubresThumbnailURL(path, fileInfo.FileName, 200)
				}
				return info
			}
			return nil
		},
	}, nil
}

// serveSubjectTemplate creates the funcs to generate the PageHeader, PageFile,
// and pageMembers that can be used by the subject template, and serves the template.
func (pr *publishRequest) serveSubjectTemplate() {
	dr := pr.ph.Search.NewDescribeRequest()
	dr.Describe(pr.subject, 3)
	res, err := dr.Result()
	if err != nil {
		log.Printf("Errors loading %s, permanode %s: %v, %#v", pr.req.URL, pr.subject, err, err)
		http.Error(pr.rw, "Error loading describe request", http.StatusInternalServerError)
		return
	}
	subdes := res[pr.subject.String()]
	if subdes.CamliType == "file" {
		pr.serveFileDownload(subdes)
		return
	}

	headerFunc := func() *publish.PageHeader {
		return pr.subjectHeader(res)
	}
	fileFunc := func() *publish.PageFile {
		file, err := pr.subjectFile(res)
		if err != nil {
			log.Printf("%v", err)
			return nil
		}
		return file
	}
	membersFunc := func() *publish.PageMembers {
		members, err := pr.subjectMembers(res)
		if err != nil {
			log.Printf("%v", err)
			return nil
		}
		return members
	}
	page := &publish.SubjectPage{
		Header:  headerFunc,
		File:    fileFunc,
		Members: membersFunc,
	}

	err = pr.ph.goTemplate.Execute(pr.rw, page)
	if err != nil {
		log.Printf("Error serving subject template: %v", err)
		http.Error(pr.rw, "Error serving template", http.StatusInternalServerError)
		return
	}
}

func (pr *publishRequest) validPathChain(path []blob.Ref) bool {
	bi := pr.subject
	for len(path) > 0 {
		var next blob.Ref
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
		thumbMeta: pr.ph.thumbMeta,
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
func (pr *publishRequest) fileSchemaRefFromBlob(des *search.DescribedBlob) (fileref blob.Ref, fileinfo *camtypes.FileInfo, ok bool) {
	if des == nil {
		http.NotFound(pr.rw, pr.req)
		return
	}
	if des.Permanode != nil {
		// TODO: get "forceMime" attr out of the permanode? or
		// fileName content-disposition?
		if cref := des.Permanode.Attr.Get("camliContent"); cref != "" {
			cbr, ok2 := blob.Parse(cref)
			if !ok2 {
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

func (ph *PublishHandler) signUpload(jsonSign *signhandler.Handler, name string, bb *schema.Builder) (blob.Ref, error) {
	signed, err := jsonSign.Sign(bb)
	if err != nil {
		return blob.Ref{}, fmt.Errorf("error signing %s: %v", name, err)
	}
	uh := client.NewUploadHandleFromString(signed)
	_, err = blobserver.Receive(ph.Storage, uh.BlobRef, uh.Contents)
	if err != nil {
		return blob.Ref{}, fmt.Errorf("error uploading %s: %v", name, err)
	}
	return uh.BlobRef, nil
}

func (ph *PublishHandler) setRootNode(jsonSign *signhandler.Handler, pn blob.Ref) (err error) {
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
