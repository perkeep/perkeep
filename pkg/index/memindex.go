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
	"camlistore.org/pkg/sorted"
	"go4.org/jsonconfig"
)

func init() {
	blobserver.RegisterStorageConstructor("memory-only-dev-indexer",
		blobserver.StorageConstructor(newMemoryIndexFromConfig))
}

// NewMemoryIndex returns an Index backed only by memory, for use in tests.
func NewMemoryIndex() *Index {
	ix, err := New(sorted.NewMemoryKeyValue())
	if err != nil {
		// Nothing to fail in memory, so worth panicing about
		// if we ever see something.
		panic(err)
	}
	return ix
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
	ix.InitBlobSource(sto)

	return ix, err
}
