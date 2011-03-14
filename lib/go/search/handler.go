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
	"time"
)

func CreateHandler(idx Index) func(http.ResponseWriter, *http.Request) {
	return func(conn http.ResponseWriter, req *http.Request) {
		handleSearch(conn, req, idx)
	}
}

type jsonMap map[string]interface{}

func handleSearch(conn http.ResponseWriter, req *http.Request, idx Index) {
	ch := make(chan *Result)
	results := make([]jsonMap, 0)
	errch := make(chan os.Error)
	go func() {
		errch <- idx.GetRecentPermanodes(ch, []*blobref.BlobRef{blobref.Parse("sha1-c4da9d771661563a27704b91b67989e7ea1e50b8")},
			50)
	}()
	for res := range ch {
		jm := make(jsonMap)
		jm["blobref"] = res.BlobRef.String()
		t := time.SecondsToUTC(res.LastModTime)
		jm["modtime"] = t.Format(time.RFC3339)
		results = append(results, jm)
	}
	err := <-errch

	ret := make(jsonMap)
	ret["results"] = results
	ret["_err"] = fmt.Sprintf("%v", err)
	httputil.ReturnJson(conn, ret)
}
