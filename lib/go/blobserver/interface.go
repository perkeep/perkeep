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

package blobserver

import (
	"camli/blobref"
	"os"
)

type Storage interface {
	blobref.Fetcher

	// Remove 0 or more blobs from provided partition, which should be empty
	// for the default partition.  Removal of non-existent items isn't an error.
	// Returns failure if any items existed but failed to be deleted.
	Remove(partition string, blobs []*blobref.BlobRef) os.Error
}
