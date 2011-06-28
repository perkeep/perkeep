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
	"fmt"
	"html"
	"http"
	"log"
	"os"

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

func (pub *PublishHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	base := req.Header.Get("X-PrefixHandler-PathBase")
	suffix := req.Header.Get("X-PrefixHandler-PathSuffix")

	pn, err := pub.Search.Index().PermanodeOfSignerAttrValue(pub.Search.Owner(), "camliRoot", pub.RootName)
	if err != nil {
		rw.WriteHeader(404)
		fmt.Fprintf(rw, "Error: publish handler at base %q, serving root name %q has no configured permanode",
			base, pub.RootName)
		return
	}

	fmt.Fprintf(rw, "I am publish handler at base %q, serving root %q (permanode=%s), suffix %q<hr>",
		base, pub.RootName, pn, html.EscapeString(suffix))
	paths, err := pub.Search.Index().PathLookup(pub.Search.Owner(), pn, suffix)
	if err != nil {
		fmt.Fprintf(rw, "<b>Error:</b> %v", err)
		return
	}
	for _, path := range paths {
		fmt.Fprintf(rw, "<p><b>Target:</b> <a href='/ui/?p=%s'>%s</a></p>", path.Target, path.Target)
	}
}

func (pub *PublishHandler) bootstrapPermanode(jsonSign *JSONSignHandler) os.Error {
	if pn, err := pub.Search.Index().PermanodeOfSignerAttrValue(pub.Search.Owner(), "camliRoot", pub.RootName); err == nil {
		log.Printf("Publish root %q using existing permanode %s", pub.RootName, pn)
		return nil
	}
	log.Printf("Publish root %q needs a permanode + claim", pub.RootName)

	// Step 1: create a permanode
	pn, err := jsonSign.SignMap(schema.NewUnsignedPermanode())
	if err != nil {
		return fmt.Errorf("error creating new permanode: %v", err)
	}
	ph := client.NewUploadHandleFromString(pn)
	_, err = pub.Storage.ReceiveBlob(ph.BlobRef, ph.Contents)
	if err != nil {
		return fmt.Errorf("error uploading permanode: %v", err)
	}

	// Step 2: addd a claim that the new permanode is the desired root.
	claim, err := jsonSign.SignMap(schema.NewSetAttributeClaim(ph.BlobRef, "camliRoot", pub.RootName))
	if err != nil {
                return fmt.Errorf("error creating claim: %v", err)
        }
	ch := client.NewUploadHandleFromString(claim)
	_, err = pub.Storage.ReceiveBlob(ch.BlobRef, ch.Contents)
        if err != nil {
                return fmt.Errorf("error uploading claim: %v", err)
        }
	return nil
}
