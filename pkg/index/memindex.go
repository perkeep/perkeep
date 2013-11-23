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

package index

import (
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/sorted"
)

func init() {
	blobserver.RegisterStorageConstructor("memory-only-dev-indexer",
		blobserver.StorageConstructor(newMemoryIndexFromConfig))
}

// NewMemoryIndex returns an Index backed only by memory, for use in tests.
func NewMemoryIndex() *Index {
	return New(sorted.NewMemoryKeyValue())
}

func newMemoryIndexFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	blobPrefix := config.RequiredString("blobSource")
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto, err := ld.GetStorage(blobPrefix)
	if err != nil {
		return nil, err
	}

	ix := NewMemoryIndex()
	ix.BlobSource = sto

	// Good enough, for now:
	ix.KeyFetcher = ix.BlobSource

	return ix, err
}
