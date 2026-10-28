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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/test"
	"perkeep.org/pkg/types/camtypes"
)

func TestUploadPublicKeyNoDestination(t *testing.T) {
	err := new(Handler).UploadPublicKey(context.Background())
	if err == nil || !strings.Contains(err.Error(), "publicKeyDest") {
		t.Fatalf("UploadPublicKey without destination: %v", err)
	}
}

func setupHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := newJSONSignFromConfig(
		test.NewLoader(),
		jsonconfig.Obj{
			"keyId":      "2931A67C26F5ABDA",
			"secretRing": "../testdata/test-secring.gpg",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func setupServer(t *testing.T) *httptest.Server {
	t.Helper()
	handler := &httputil.PrefixHandler{
		Prefix:  "/",
		Handler: setupHandler(t),
	}
	return httptest.NewServer(handler)
}

func TestDiscovery(t *testing.T) {
	ts := setupServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/camli/sig/discovery")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 status: %v", resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var disco camtypes.SignDiscovery
	if err := json.Unmarshal(body, &disco); err != nil {
		t.Fatal(err)
	}

	if expectedFingerprint := "FBB89AA320A2806FE497C0492931A67C26F5ABDA"; disco.PublicKeyFingerprint != expectedFingerprint {
		t.Errorf("Got Fingerprint %s, expected %s ", disco.PublicKeyFingerprint, expectedFingerprint)
	}
	if disco.PublicKeyID != "2931A67C26F5ABDA" {
		t.Errorf("PublicKeyID = %q; want 2931A67C26F5ABDA", disco.PublicKeyID)
	}
	// Decode the same response as a client predating fingerprint discovery.
	var legacy struct {
		PublicKeyID string `json:"publicKeyId"`
	}
	if err := json.Unmarshal(body, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.PublicKeyID != "2931A67C26F5ABDA" {
		t.Errorf("legacy publicKeyId = %q; want 2931A67C26F5ABDA", legacy.PublicKeyID)
	}
}
