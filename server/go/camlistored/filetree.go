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

func (fth *FileTreeHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" && req.Method != "HEAD" {
		http.Error(rw, "Invalid method", 400)
		return
	}
	ret := make(map[string]interface{})
	defer httputil.ReturnJson(rw, ret)

	fetchSeeker, err := fth.storageSeekFetcher()
	if err != nil {
		log.Printf("getting fetcher: %v\n", err)
		return
	}

	de, err := schema.NewDirectoryEntryFromBlobRef(fetchSeeker, fth.file)
	entries, err := de.Directory().Readdir(-1)
	if err != nil {
		log.Printf("reading dir from blobref %s: %v\n", fth.file, err)
		return
	}
	children := make([]map[string]interface{}, 0)
	for _, v := range entries {
		child := map[string]interface{}{
			"name":    v.FileName(),
			"type":    v.CamliType(),
			"blobRef": v.BlobRef(),
		}
		children = append(children, child)
	}
	ret["children"] = children
}
