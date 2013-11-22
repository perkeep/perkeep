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

package localdisk

import (
	"time"

	"camlistore.org/pkg/blobserver"
)

// Compile-time check that *DiskStorage implements blobserver.Generationer
var _ blobserver.Generationer = (*DiskStorage)(nil)

// StorageGeneration returns the generation's initialization time,
// and the random string.
func (ds *DiskStorage) StorageGeneration() (initTime time.Time, random string, err error) {
	return ds.gen.StorageGeneration()
}

// ResetStorageGeneration reinitializes the generation by recreating the
// GENERATION.dat file with a new random string
func (ds *DiskStorage) ResetStorageGeneration() error {
	return ds.gen.ResetStorageGeneration()
}
