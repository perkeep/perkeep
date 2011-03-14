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
	"os"
	"sync"
	"time"
)

func CreateHandler(idx Index) func(http.ResponseWriter, *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		handleSearch(conn, req, idx)
	}
}

type jsonMap map[string]interface{}

func handleSearch(conn http.ResponseWriter, req *http.Request, idx Index) {

	user := blobref.Parse("sha1-c4da9d771661563a27704b91b67989e7ea1e50b8")

	ch := make(chan *Result)
	results := make([]jsonMap, 0)
	errch := make(chan os.Error)
	go func() {
		errch <- idx.GetRecentPermanodes(ch, []*blobref.BlobRef{user}, 50)
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
	jm["content"] = "sha1-4dc0d8d22de9979f74f3cc6ab59c6592061e24b9" // TODO: un-hardcode
	jm["type"] = "directory"
	attr := make(jsonMap)
	jm["attr"] = attr
	attr["title"] = []string{"camlistore lib/"}
	attr["tag"] = []string{"test", "code"}
}
