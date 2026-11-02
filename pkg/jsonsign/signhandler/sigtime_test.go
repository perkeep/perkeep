/*
Copyright 2026 The Perkeep Authors

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

package signhandler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/types/camtypes"
)

// TestSignatureTime checks that signing at a given signatureTime makes the
// same signed blob every time, as planned permanodes need.
func TestSignatureTime(t *testing.T) {
	ts := setupServer(t)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/camli/sig/discovery")
	if err != nil {
		t.Fatal(err)
	}
	var disco camtypes.SignDiscovery
	err = json.NewDecoder(res.Body).Decode(&disco)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	unsigned, err := schema.NewPlannedPermanode("sha224-0123").SetSigner(disco.PublicKeyBlobRef).JSON()
	if err != nil {
		t.Fatal(err)
	}
	sign := func(form url.Values) (int, string) {
		t.Helper()
		form.Set("json", unsigned)
		res, err := http.PostForm(ts.URL+"/camli/sig/sign", form)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body)
	}
	at := url.Values{"signatureTime": {"1970-01-01T00:00:00Z"}}
	code1, first := sign(at)
	code2, second := sign(at)
	if code1 != 200 || code2 != 200 || !strings.Contains(first, `"camliSig"`) {
		t.Fatalf("signing: %d %q", code1, first)
	}
	if first != second {
		t.Errorf("signing at the same signatureTime twice made different blobs:\n%s\n%s", first, second)
	}
	if code, _ := sign(url.Values{"signatureTime": {"yesterday"}}); code != http.StatusBadRequest {
		t.Errorf("bad signatureTime: status %d; want 400", code)
	}
}
