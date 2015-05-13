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
	"log"
	"net/http"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/schema"
)

type FileTreeHandler struct {
	Fetcher blob.Fetcher
	file    blob.Ref
}

// FileTreeNode represents a file in a file tree.
// It is part of the FileTreeResponse.
type FileTreeNode struct {
	// Name is the basename of the node.
	Name string `json:"name"`
	// Type is the camliType of the node. This may be "file", "directory", "symlink"
	// or other in the future.
	Type string `json:"type"`
	// BlobRef is the blob.Ref of the node.
	BlobRef blob.Ref `json:"blobRef"`
}

// FileTreeResponse is the JSON response for the FileTreeHandler.
type FileTreeResponse struct {
	// Children is the list of children files of a directory.
	Children []FileTreeNode `json:"children"`
}

func (fth *FileTreeHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if req.Method != "GET" && req.Method != "HEAD" {
		http.Error(rw, "Invalid method", 400)
		return
	}

	de, err := schema.NewDirectoryEntryFromBlobRef(fth.Fetcher, fth.file)
	if err != nil {
		http.Error(rw, "Error reading directory", 500)
		log.Printf("Error reading directory from blobref %s: %v\n", fth.file, err)
		return
	}
	dir, err := de.Directory()
	if err != nil {
		http.Error(rw, "Error reading directory", 500)
		log.Printf("Error reading directory from blobref %s: %v\n", fth.file, err)
		return
	}
	entries, err := dir.Readdir(-1)
	if err != nil {
		http.Error(rw, "Error reading directory", 500)
		log.Printf("reading dir from blobref %s: %v\n", fth.file, err)
		return
	}

	var ret = FileTreeResponse{
		Children: make([]FileTreeNode, 0, len(entries)),
	}
	for _, v := range entries {
		ret.Children = append(ret.Children, FileTreeNode{
			Name:    v.FileName(),
			Type:    v.CamliType(),
			BlobRef: v.BlobRef(),
		})
	}
	httputil.ReturnJSON(rw, ret)
}
