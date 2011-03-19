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
	"camli/blobref"
	"camli/httputil"

	"fmt"
	"http"
	"log"
	"os"
	"sort"
	"sync"
	"time"
)

func CreateHandler(idx Index, ownerBlobRef *blobref.BlobRef) func(http.ResponseWriter, *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		handleSearch(conn, req, idx, ownerBlobRef)
	}
}

type jsonMap map[string]interface{}

func handleSearch(conn http.ResponseWriter, req *http.Request, idx Index, ownerBlobRef *blobref.BlobRef) {

	ch := make(chan *Result)
	results := make([]jsonMap, 0)
	errch := make(chan os.Error)
	go func() {
		errch <- idx.GetRecentPermanodes(ch, []*blobref.BlobRef{ownerBlobRef}, 50)
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
			populatePermanodeFields(jm, idx, res.BlobRef, res.Signer)
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
