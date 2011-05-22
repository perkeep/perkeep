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
	"os"

	"camli/blobref"
)

// TODO: it'd be nice to make sources be []BlobEnumerator, but that
// makes callers more complex since assignable interfaces' slice forms
// aren't assignable.
func MergedEnumerated(dest chan<- blobref.SizedBlobRef, sources []Storage, after string, limit uint, waitSeconds int) os.Error {
	return os.NewError("TODO: NOT IMPLEMENTED")
}
