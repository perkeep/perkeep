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
	"http"
	"log"
	"os"

	"camli/blobref"
	"camli/schema"
	"camli/httputil"
)

type FileTreeHandler struct {
	Fetcher blobref.StreamingFetcher
	file    *blobref.BlobRef
}

func (fth *FileTreeHandler) storageSeekFetcher() (blobref.SeekFetcher, os.Error) {
	return blobref.SeekerFromStreamingFetcher(fth.Fetcher) // TODO: pass ih.Cache?
}

//TODO(mpl): add dot dot ?
func (fth *FileTreeHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" && req.Method != "HEAD" {
		log.Printf("Invalid method\n")
		return
	}
	ret := make(map[string]interface{})
	defer httputil.ReturnJson(rw, ret)

	fetchSeeker, err := fth.storageSeekFetcher()
	if err != nil {
		log.Printf("%v\n", err)
		return
	}

	dr, err := schema.NewDirReader(fetchSeeker, fth.file)
	if err != nil {
		log.Printf("Can't read dir: %v\n", err)
		return
	}

	entries, err := dr.Read(-1)
	if err != nil {
		log.Printf("Can't read dir entries: %v\n", err)
		return
	}

	children := make([]map[string]interface{}, 0)
	for _, v := range entries {
		child := map[string]interface{}{
			"name":    v.Name,
			"type":    v.Type,
			"blobref": v.Blobref,
		}
		children = append(children, child)
	}
	ret["child"] = children
}
