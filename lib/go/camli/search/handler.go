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

package search

import (
	"fmt"
	"http"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"camli/blobref"
	"camli/blobserver"
	"camli/jsonconfig"
	"camli/httputil"
)

func init() {
	blobserver.RegisterHandlerConstructor("search", newHandlerFromConfig)
}

type searchHandler struct {
	index Index
	owner *blobref.BlobRef
}

func newHandlerFromConfig(ld blobserver.Loader, conf jsonconfig.Obj) (http.Handler, os.Error) {
	indexPrefix := conf.RequiredString("index") // TODO: add optional help tips here?
	ownerBlobStr := conf.RequiredString("owner")
	if err := conf.Validate(); err != nil {
		return nil, err
	}

	indexHandler, err := ld.GetHandler(indexPrefix)
	if err != nil {
		return nil, fmt.Errorf("search config references unknown handler %q", indexPrefix)
	}
	indexer, ok := indexHandler.(Index)
	if !ok {
		return nil, fmt.Errorf("search config references invalid indexer %q (actually a %T)", indexPrefix, indexHandler)
	}
	ownerBlobRef := blobref.Parse(ownerBlobStr)
	if ownerBlobRef == nil {
		return nil, fmt.Errorf("search 'owner' has malformed blobref %q; expecting e.g. sha1-xxxxxxxxxxxx",
			ownerBlobStr)
	}
	return &searchHandler{
		index: indexer,
		owner: ownerBlobRef,
	}, nil
}


type jsonMap map[string]interface{}

func (sh *searchHandler) ServeHTTP(conn http.ResponseWriter, req *http.Request) {
	ch := make(chan *Result)
	results := make([]jsonMap, 0)
	errch := make(chan os.Error)
	go func() {
		log.Printf("finding recent permanodes for %s", sh.owner)
		errch <- sh.index.GetRecentPermanodes(ch, []*blobref.BlobRef{sh.owner}, 50)
	}()

	wg := new(sync.WaitGroup)
	for res := range ch {
		jm := make(jsonMap)
		jm["blobref"] = res.BlobRef.String()
		jm["owner"] = res.Signer.String()
		t := time.SecondsToUTC(res.LastModTime)
		jm["modtime"] = t.Format(time.RFC3339)
		results = append(results, jm)
		wg.Add(1)
		go func() {
			populatePermanodeFields(jm, sh.index, res.BlobRef, res.Signer)
			wg.Done()
		}()
	}
	wg.Wait()

	err := <-errch

	ret := make(jsonMap)
	ret["results"] = results
	if err != nil {
		// TODO: return error status code
		ret["error"] = fmt.Sprintf("%v", err)
	}
	httputil.ReturnJson(conn, ret)
}

func populatePermanodeFields(jm jsonMap, idx Index, pn, signer *blobref.BlobRef) {
	jm["content"] = ""
	attr := make(jsonMap)
	jm["attr"] = attr

	claims, err := idx.GetOwnerClaims(pn, signer)
	if err != nil {
		log.Printf("Error getting claims of %s: %v", pn.String(), err)
	} else {
		sort.Sort(claims)
		for _, cl := range claims {
			switch cl.Type {
			case "del-attribute":
				attr[cl.Attr] = nil, false
			case "set-attribute":
				attr[cl.Attr] = nil, false
				fallthrough
			case "add-attribute":
				sl, ok := attr[cl.Attr].([]string)
				if !ok {
					sl = make([]string, 0, 1)
					attr[cl.Attr] = sl
				}
				attr[cl.Attr] = append(sl, cl.Value)
			}
		}
		if sl, ok := attr["camliContent"].([]string); ok && len(sl) > 0 {
			jm["content"] = sl[0]
			attr["camliContent"] = nil, false
		}
	}

	// If the content permanode is now known, look up its type
	if content, ok := jm["content"].(string); ok && content != "" {
		cbr := blobref.Parse(content)
		mime, ok, _ := idx.GetBlobMimeType(cbr)
		if ok {
			jm["type"] = mime
		}
	}
}
