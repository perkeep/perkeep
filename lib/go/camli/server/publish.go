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
	"bytes"
	"fmt"
	"html"
	"http"
	"json"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"

	"camli/blobref"
	"camli/blobserver"
	"camli/client" // just for NewUploadHandleFromString.  move elsewhere?
	"camli/jsonconfig"
	"camli/schema"
	"camli/search"
	"url"
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

	bsLoader      blobserver.Loader
	staticHandler http.Handler
}

func init() {
	blobserver.RegisterHandlerConstructor("publish", newPublishFromConfig)
}

func newPublishFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err os.Error) {
	ph := &PublishHandler{
		bsLoader: ld,
	}
	ph.RootName = conf.RequiredString("rootName")
	ph.JSFiles = conf.OptionalList("js")
	ph.CSSFiles = conf.OptionalList("css")
	blobRoot := conf.RequiredString("blobRoot")
	searchRoot := conf.RequiredString("searchRoot")
	cachePrefix := conf.OptionalString("cache", "")
	scType := conf.OptionalString("scaledImage", "")
	bootstrapSignRoot := conf.OptionalString("devBootstrapPermanodeUsing", "")
	if err = conf.Validate(); err != nil {
		return
	}

	if ph.RootName == "" {
		return nil, os.NewError("invalid empty rootName")
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

	if bootstrapSignRoot != "" {
		if t := ld.GetHandlerType(bootstrapSignRoot); t != "jsonsign" {
			return nil, fmt.Errorf("publish handler's devBootstrapPermanodeUsing must be of type jsonsign")
		}
		h, _ := ld.GetHandler(bootstrapSignRoot)
		jsonSign := h.(*JSONSignHandler)
		if err := ph.bootstrapPermanode(jsonSign); err != nil {
			return nil, fmt.Errorf("error bootstrapping permanode: %v", err)
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
			ph.sc = NewScaledImageLru()
		case "":
		default:
			return nil, fmt.Errorf("unsupported publish handler's scType: %q ", scType)
		}
	}

	ph.staticHandler = http.FileServer(uiFiles)

	return ph, nil
}

func (ph *PublishHandler) rootPermanode() (*blobref.BlobRef, os.Error) {
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

func (ph *PublishHandler) lookupPathTarget(root *blobref.BlobRef, suffix string) (*blobref.BlobRef, os.Error) {
	if suffix == "" {
		return root, nil
	}
	path, err := ph.Search.Index().PathLookup(ph.Search.Owner(), root, suffix, nil /* as of now */ )
	if err != nil {
		return nil, err
	}
	if path.Target == nil {
		return nil, os.ENOENT
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
	_, handler, err := ph.bsLoader.FindHandlerByTypeIfLoaded("ui")
	if err != nil {
		discoveryHelper(rw, req, map[string]interface{}{
			"error": "no admin handler running",
		})
		return
	}
	ui := handler.(*UIHandler)
	ui.serveDiscovery(rw, req)
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
	suffix, res := req.Header.Get("X-PrefixHandler-PathSuffix"), ""
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
		base:            req.Header.Get("X-PrefixHandler-PathBase"),
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

func (pr *publishRequest) findSubject() os.Error {
	if strings.HasPrefix(pr.suffix, "=s/") {
		pr.subres = "/" + pr.suffix
		return nil
	}

	subject, err := pr.ph.lookupPathTarget(pr.rootpn, pr.suffix)
	if err != nil {
		return err
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
		if err == os.ENOENT {
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
		pr.ph.staticHandler.ServeHTTP(pr.rw, pr.req)
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
	{
		jm := make(map[string]interface{})
		dr.PopulateJSON(jm)
		pr.pf("<!doctype html>\n<html>\n<head>\n <title>%s</title>\n", html.EscapeString(title))
		for _, filename := range pr.ph.CSSFiles {
			pr.pf(" <link rel='stylesheet' type='text/css' href='%s'>\n", pr.staticPath(filename))
		}
		for _, filename := range pr.ph.JSFiles {
			// TODO(bradfitz): Remove this manual dependency hack once Issue 37 is resolved.
			if filename == "camli.js" {
				pr.pf(" <script src='%s'></script>\n", pr.staticPath("base64.js"))
				pr.pf(" <script src='%s'></script>\n", pr.staticPath("Crypto.js"))
				pr.pf(" <script src='%s'></script>\n", pr.staticPath("SHA1.js"))
			}
			pr.pf(" <script src='%s'></script>\n", pr.staticPath(filename))
			if filename == "camli.js" && pr.ViewerIsOwner() {
				pr.pf(" <script src='%s'></script>\n", pr.base+"?camli.mode=config&cb=onConfiguration")
			}
		}
		pr.pf(" <script>\n")
		pr.pf("var camliViewIsOwner = %v;\n", pr.ViewerIsOwner())
		pr.pf("var camliPagePermanode = %q;\n", pr.subject)
		pr.pf("var camliPageMeta = \n")
		json, _ := json.MarshalIndent(jm, "", "  ")
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
				des.File.MimeType)
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
		pr.pf("<ul>\n")
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
		mime = fileinfo.MimeType
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

func (ph *PublishHandler) bootstrapPermanode(jsonSign *JSONSignHandler) (err os.Error) {
	if pn, err := ph.Search.Index().PermanodeOfSignerAttrValue(ph.Search.Owner(), "camliRoot", ph.RootName); err == nil {
		log.Printf("Publish root %q using existing permanode %s", ph.RootName, pn)
		return nil
	}
	log.Printf("Publish root %q needs a permanode + claim", ph.RootName)

	defer func() {
		if perr := recover(); perr != nil {
			err = perr.(os.Error)
		}
	}()
	signUpload := func(name string, m map[string]interface{}) *blobref.BlobRef {
		signed, err := jsonSign.SignMap(m)
		if err != nil {
			panic(fmt.Errorf("error signing %s: %v", name, err))
		}
		uh := client.NewUploadHandleFromString(signed)
		_, err = ph.Storage.ReceiveBlob(uh.BlobRef, uh.Contents)
		if err != nil {
			panic(fmt.Errorf("error uploading %s: %v", name, err))
		}
		return uh.BlobRef
	}

	pn := signUpload("permanode", schema.NewUnsignedPermanode())
	signUpload("set-attr camliRoot", schema.NewSetAttributeClaim(pn, "camliRoot", ph.RootName))
	signUpload("set-attr title", schema.NewSetAttributeClaim(pn, "title", "Publish root node for "+ph.RootName))
	return nil
}
