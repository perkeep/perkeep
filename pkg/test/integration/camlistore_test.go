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

package integration

import (
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/test"
)

// Test that running:
//   $ camput permanode
// ... creates and uploads a permanode, and that we can camget it back.
func TestCamputPermanode(t *testing.T) {
	w := test.GetWorld(t)
	out := test.MustRunCmd(t, w.Cmd("camput", "permanode"))
	br, ok := blob.Parse(strings.TrimSpace(out))
	if !ok {
		t.Fatalf("Expected permanode in stdout; got %q", out)
	}

	out = test.MustRunCmd(t, w.Cmd("camget", br.String()))
	mustHave := []string{
		`{"camliVersion": 1,`,
		`"camliSigner": "`,
		`"camliType": "permanode",`,
		`random": "`,
		`,"camliSig":"`,
	}
	for _, str := range mustHave {
		if !strings.Contains(out, str) {
			t.Errorf("Expected permanode response to contain %q; it didn't. Got: %s", str, out)
		}
	}
}
