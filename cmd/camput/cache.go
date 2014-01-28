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

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/client"
)

// A HaveCache tracks whether a remove blobserver has a blob or not.
type HaveCache interface {
	StatBlobCache(br blob.Ref) (size uint32, ok bool)
	NoteBlobExists(br blob.Ref, size uint32)
	Close() error
}

// UploadCache is the "stat cache" for regular files.  Given a current
// working directory, possibly relative filename, stat info, and
// whether that file was uploaded with a permanode (-filenodes),
// returns what the ultimate put result (the top-level "file" schema
// blob) for that regular file was.
type UploadCache interface {
	// CachedPutResult looks in the cache for the put result for the file
	// that was uploaded. If withPermanode, it is only a hit if a planned
	// permanode for the file was created and uploaded too, and vice-versa.
	// The returned PutResult is always for the "file" schema blob.
	CachedPutResult(pwd, filename string, fi os.FileInfo, withPermanode bool) (*client.PutResult, error)
	// AddCachedPutResult stores in the cache the put result for the file that
	// was uploaded. If withPermanode, it means a planned permanode was created
	// for this file when it was uploaded (with -filenodes), and the cache entry
	// will reflect that.
	AddCachedPutResult(pwd, filename string, fi os.FileInfo, pr *client.PutResult, withPermanode bool)
	Close() error
}
