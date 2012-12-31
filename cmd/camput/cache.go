/*
Copyright 2012 Google Inc.

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
	"os"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/client"
)

// A HaveCache tracks whether a remove blobserver has a blob or not.
type HaveCache interface {
	BlobExists(br *blobref.BlobRef) bool
	NoteBlobExists(br *blobref.BlobRef)
}

// UploadCache is the "stat cache" for regular files.  Given a current
// working directory, possibly relative filename, and stat info,
// returns what the ultimate put result (the top-level "file" schema
// blob) for that regular file was.
type UploadCache interface {
	CachedPutResult(pwd, filename string, fi os.FileInfo) (*client.PutResult, error)
	AddCachedPutResult(pwd, filename string, fi os.FileInfo, pr *client.PutResult)
}
