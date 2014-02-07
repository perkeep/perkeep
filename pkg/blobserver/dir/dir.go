/*
Copyright 2014 The Camlistore Authors

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

// Package dir implements the blobserver Storage interface for a directory,
// detecting whether the directory is file-per-blob (localdisk) or diskpacked.
// If neither, it initializes diskpacked.
package dir

import (
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/diskpacked"
	"camlistore.org/pkg/blobserver/localdisk"
)

// New returns a new blobserver Storage implementation, storing blobs in the provided dir.
// If dir has an index.kv file, a diskpacked implementation is returnd.
func New(dir string) (blobserver.Storage, error) {
	if v, err := diskpacked.IsDir(dir); err != nil {
		return nil, err
	} else if v {
		return diskpacked.New(dir)
	}
	if v, err := localdisk.IsDir(dir); err != nil {
		return nil, err
	} else if v {
		return localdisk.New(dir)
	}
	return diskpacked.New(dir)
}
