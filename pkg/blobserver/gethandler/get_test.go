/*
Copyright 2013 Google Inc.

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

package gethandler

import (
	"testing"

	"camlistore.org/pkg/blob"
)

func TestBlobFromURLPath(t *testing.T) {
	br := blobFromURLPath("/foo/bar/camli/sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15")
	if !br.Valid() {
		t.Fatal("nothing found")
	}
	want := blob.MustParse("sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15")
	if want != br {
		t.Fatalf("got = %v; want %v", br, want)
	}
}
