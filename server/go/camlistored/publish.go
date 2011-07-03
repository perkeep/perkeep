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
	"html"
	"http"
	"json"
	"log"
	"os"
	"strings"

	"camli/blobref"
	"camli/blobserver"
	"camli/client" // just for NewUploadHandleFromString.  move elsewhere?
	"camli/jsonconfig"
	"camli/schema"
	"camli/search"
)

// PublishHandler publishes your info to the world, if permanodes have
// appropriate ACLs set.  (everything is private by default)
type PublishHandler struct {
	RootName string
	Search   *search.Handler
	Storage  blobserver.Storage // of blobRoot
	Cache    blobserver.Storage // or nil
}

func init() {
	blobserver.RegisterHandlerConstructor("publish", newPublishFromConfig)
}

func newPublishFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (h http.Handler, err os.Error) {
	ph := &PublishHandler{}
	ph.RootName = conf.RequiredString("rootName")
	blobRoot := conf.RequiredString("blobRoot")
	searchRoot := conf.RequiredString("searchRoot")
	cachePrefix := conf.OptionalString("cache", "")
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
	}

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
	path, err := ph.Search.Index().PathLookup(ph.Search.Owner(), root, suffix, nil /* as of now */ )
	if err != nil {
		return nil, err
	}
	if path.Target == nil {
		return nil, os.ENOENT
	}
	return path.Target, nil
}

func (ph *PublishHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	preq := ph.NewRequest(rw, req)
	preq.serveHTTP()
}

func (ph *PublishHandler) NewRequest(rw http.ResponseWriter, req *http.Request) *publishRequest {
	// splits a path request into its suffix and subresource parts.
	// e.g. /blog/foo/camli/res/file/xxx -> ("foo", "file/xxx")
	suffix, res := req.Header.Get("X-PrefixHandler-PathSuffix"), ""
	if s := strings.SplitN(suffix, "/camli/res/", 2); len(s) == 2 {
		suffix, res = s[0], s[1]
	}
	rootpn, _ := ph.rootPermanode()
	return &publishRequest{
		ph:     ph,
		rw:     rw,
		req:    req,
		suffix: suffix,
		base:   req.Header.Get("X-PrefixHandler-PathBase"),
		subres: res,
		rootpn: rootpn,
	}
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
}

func (pr *publishRequest) Debug() bool {
	return pr.req.FormValue("debug") == "1"
}

func (pr *publishRequest) SubresourceType() string {
	if parts := strings.SplitN(pr.subres, "/", 2); len(parts) > 0 {
		return parts[0]
	}
	return ""
}

func (pr *publishRequest) SubresFile(path []*blobref.BlobRef, fileName string) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s%s/camli/res/file", pr.base, pr.suffix)
	for _, br := range path {
		fmt.Fprintf(&buf, "/%s", br)
	}
	fmt.Fprintf(&buf, "/%s", http.URLEscape(fileName))
	return buf.String()
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
	var err os.Error
	pr.subject, err = pr.ph.lookupPathTarget(pr.rootpn, pr.suffix)
	if err != nil {
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

	// TODO: serve /camli/res/file/<blobref>/<blobref>/[dummyname] downloads
	switch pr.SubresourceType() {
	case "":
		pr.serveHtmlDescribe()
	case "blob":
		// TODO: download a raw blob
	case "file":
		pr.serveFileDownload()
	default:
		pr.rw.WriteHeader(400)
		pr.pf("<p>Invalid or unsupported resource request.</p>")
	}
}

func (pr *publishRequest) pf(format string, args ...interface{}) {
	fmt.Fprintf(pr.rw, format, args...)
}

func (pr *publishRequest) serveHtmlDescribe() {
	dr := pr.ph.Search.NewDescribeRequest()
	dr.Describe(pr.subject, 3)
	res, err := dr.Result()
	if err != nil {
		log.Printf("Errors loading %s, permanode %s: %v, %#v", pr.req.URL, pr.subject, err, err)
		pr.pf("<p>Errors loading.</p>")
		return
	}

	subdes := res[pr.subject.String()]
	title := subdes.Title()

	// HTML header + Javascript
	{
		jm := make(map[string]interface{})
		dr.PopulateJSON(jm)
		pr.pf("<html>\n<head>\n <title>%s</title>\n <script>\nvar camliPageMeta = \n",
			html.EscapeString(title))
		json, _ := json.MarshalIndent(jm, "", "  ")
		pr.rw.Write(json)
		pr.pf(";\n </script>\n</head>\n<body>\n")
		defer pr.pf("</body>\n</html>\n")
	}

	if title != "" {
		pr.pf("<h1>%s</h1>\n", html.EscapeString(title))
	}

	if members := subdes.Members(); len(members) > 0 {
		pr.pf("<ul>\n")
		for _, member := range members {
			des := member.Description()
			if des != "" {
				des = " - " + des
			}
			link := "#"
			if path, fileInfo, ok := member.PermanodeFile(); ok {
				link = pr.SubresFile(path, fileInfo.FileName)
			}
			pr.pf("  <li><a href='%s'>%s</a>%s</li>\n",
				link,
				html.EscapeString(member.Title()),
				des)
		}
		pr.pf("</ul>\n")
	}
}

func (pr *publishRequest) serveFileDownload() {

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
