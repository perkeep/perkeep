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

package localdisk

import (
	"camlistore.org/pkg/blobref"

	"testing"
)

func TestPaths(t *testing.T) {
	br := blobref.Parse("digalg-abc")
	ds := &DiskStorage{root: "/tmp/dir"}

	if e, g := "/tmp/dir/digalg/abc/___", ds.blobDirectory("", br); e != g {
		t.Errorf("short blobref dir; expected path %q; got %q", e, g)
	}
	if e, g := "/tmp/dir/digalg/abc/___/digalg-abc.dat", ds.blobPath("", br); e != g {
		t.Errorf("short blobref path; expected path %q; got %q", e, g)
	}

	br = blobref.Parse("sha1-c22b5f9178342609428d6f51b2c5af4c0bde6a42")
	if e, g := "/tmp/dir/partition/foo/sha1/c22/b5f", ds.blobDirectory("foo", br); e != g {
		t.Errorf("amazon queue dir; expected path %q; got %q", e, g)
	}

}
