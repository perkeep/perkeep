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
	pub := &PublishHandler{}
	pub.RootName = conf.RequiredString("rootName")
	blobRoot := conf.RequiredString("blobRoot")
	searchRoot := conf.RequiredString("searchRoot")
	cachePrefix := conf.OptionalString("cache", "")
	bootstrapSignRoot := conf.OptionalString("devBootstrapPermanodeUsing", "")
	if err = conf.Validate(); err != nil {
		return
	}

	if pub.RootName == "" {
		return nil, os.NewError("invalid empty rootName")
	}

	bs, err := ld.GetStorage(blobRoot)
	if err != nil {
		return nil, fmt.Errorf("publish handler's blobRoot of %q error: %v", blobRoot, err)
	}
	pub.Storage = bs

	si, err := ld.GetHandler(searchRoot)
	if err != nil {
		return nil, fmt.Errorf("publish handler's searchRoot of %q error: %v", searchRoot, err)
	}
	var ok bool
	pub.Search, ok = si.(*search.Handler)
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
		if err := pub.bootstrapPermanode(jsonSign); err != nil {
			return nil, fmt.Errorf("error bootstrapping permanode: %v", err)
		}
	}

	if cachePrefix != "" {
		bs, err := ld.GetStorage(cachePrefix)
		if err != nil {
			return nil, fmt.Errorf("publish handler's cache of %q error: %v", cachePrefix, err)
		}
		pub.Cache = bs
	}

	return pub, nil
}

func (pub *PublishHandler) rootPermanode() (*blobref.BlobRef, os.Error) {
	// TODO: caching, but this can change over time (though
	// probably rare). might be worth a 5 second cache or
	// something in-memory? better invalidation story first would
	// be nice.
	br, err := pub.Search.Index().PermanodeOfSignerAttrValue(pub.Search.Owner(), "camliRoot", pub.RootName)
	if err != nil {
		log.Printf("Error: publish handler at serving root name %q has no configured permanode: %v",
			pub.RootName, err)
	}
	return br, err
}

type publishHttpRequest struct {
	httpReq              *http.Request
	base, suffix, subres string
}

func (pr *publishHttpRequest) Debug() bool {
	return pr.httpReq.FormValue("debug") == "1"
}

func (pr *publishHttpRequest) NoSubresource() bool {
	return pr.subres == ""
}

func (pr *publishHttpRequest) SubresFile(path []*blobref.BlobRef, fileName string) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s%s/camli/res/file", pr.base, pr.suffix)
	for _, br := range path {
		fmt.Fprintf(&buf, "/%s", br)
	}
	fmt.Fprintf(&buf, "/%s", http.URLEscape(fileName))
	return buf.String()
}

func NewPublishRequest(req *http.Request) *publishHttpRequest {
	// splits a path request into its suffix and subresource parts.
	// e.g. /blog/foo/camli/res/file/xxx -> ("foo", "file/xxx")
	suffix, res := req.Header.Get("X-PrefixHandler-PathSuffix"), ""
	if s := strings.SplitN(suffix, "/camli/res/", 2); len(s) == 2 {
		suffix, res = s[0], s[1]
	}
	return &publishHttpRequest{
		httpReq: req,
		suffix:  suffix,
		base:    req.Header.Get("X-PrefixHandler-PathBase"),
		subres:  res,
	}
}

func (pub *PublishHandler) lookupPathTarget(root *blobref.BlobRef, suffix string) (*blobref.BlobRef, os.Error) {
	path, err := pub.Search.Index().PathLookup(pub.Search.Owner(), root, suffix, nil /* as of now */ )
	if err != nil {
		return nil, err
	}
	if path.Target == nil {
		return nil, os.ENOENT
	}
	return path.Target, nil
}

func (pub *PublishHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	rootpn, err := pub.rootPermanode()
	if err != nil {
		rw.WriteHeader(404)
		return
	}

	preq := NewPublishRequest(req)

	if preq.Debug() {
		fmt.Fprintf(rw, "I am publish handler at base %q, serving root %q (permanode=%s), suffix %q, subreq %q<hr>",
			preq.base, pub.RootName, rootpn, html.EscapeString(preq.suffix), html.EscapeString(preq.subres))
	}
	target, err := pub.lookupPathTarget(rootpn, preq.suffix)
	if err != nil {
		if err == os.ENOENT {
			rw.WriteHeader(404)
			return
		}
		log.Printf("Error looking up %s/%q: %v", rootpn, preq.suffix, err)
		rw.WriteHeader(500)
		return
	}

	if preq.Debug() {
		fmt.Fprintf(rw, "<p><b>Target:</b> <a href='/ui/?p=%s'>%s</a></p>", target, target)
		return
	}

	// TODO: serve /camli/res/file/<blobref>/<blobref>/[dummyname] downloads
	switch {
	case preq.NoSubresource():
		pub.serveHtmlDescribe(rw, preq, target)
	default:
		rw.WriteHeader(400)
		fmt.Fprintf(rw, "<p>Invalid or unsupported resource request.</p>")
	}
}

func (pub *PublishHandler) serveHtmlDescribe(rw http.ResponseWriter, preq *publishHttpRequest, subject *blobref.BlobRef) {
	dr := pub.Search.NewDescribeRequest()
	dr.Describe(subject, 3)
	res, err := dr.Result()
	if err != nil {
		log.Printf("Errors loading %s, permanode %s: %v, %#v", preq.httpReq.URL, subject, err, err)
		fmt.Fprintf(rw, "<p>Errors loading.</p>")
		return
	}

	subdes := res[subject.String()]
	title := subdes.Title()

	// HTML header + Javascript
	{
		jm := make(map[string]interface{})
		dr.PopulateJSON(jm)
		fmt.Fprintf(rw, "<html>\n<head>\n <title>%s</title>\n <script>\nvar camliPageMeta = \n",
			html.EscapeString(title))
		json, _ := json.MarshalIndent(jm, "", "  ")
		rw.Write(json)
		fmt.Fprintf(rw, ";\n </script>\n</head>\n<body>\n")
		defer fmt.Fprintf(rw, "</body>\n</html>\n")
	}

	if title != "" {
		fmt.Fprintf(rw, "<h1>%s</h1>\n", html.EscapeString(title))
	}

	if members := subdes.Members(); len(members) > 0 {
		fmt.Fprintf(rw, "<ul>\n")
		for _, member := range members {
			des := member.Description()
			if des != "" {
				des = " - " + des
			}
			link := "#"
			if path, fileInfo, ok := member.PermanodeFile(); ok {
				link = preq.SubresFile(path, fileInfo.FileName)
			}
			fmt.Fprintf(rw, "  <li><a href='%s'>%s</a>%s</li>\n",
				link,
				html.EscapeString(member.Title()),
				des)
		}
		fmt.Fprintf(rw, "</ul>\n")
	}
}

func (pub *PublishHandler) bootstrapPermanode(jsonSign *JSONSignHandler) (err os.Error) {
	if pn, err := pub.Search.Index().PermanodeOfSignerAttrValue(pub.Search.Owner(), "camliRoot", pub.RootName); err == nil {
		log.Printf("Publish root %q using existing permanode %s", pub.RootName, pn)
		return nil
	}
	log.Printf("Publish root %q needs a permanode + claim", pub.RootName)

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
		ph := client.NewUploadHandleFromString(signed)
		_, err = pub.Storage.ReceiveBlob(ph.BlobRef, ph.Contents)
		if err != nil {
			panic(fmt.Errorf("error uploading %s: %v", name, err))
		}
		return ph.BlobRef
	}

	pn := signUpload("permanode", schema.NewUnsignedPermanode())
	signUpload("set-attr camliRoot", schema.NewSetAttributeClaim(pn, "camliRoot", pub.RootName))
	signUpload("set-attr title", schema.NewSetAttributeClaim(pn, "title", "Publish root node for "+pub.RootName))
	return nil
}
