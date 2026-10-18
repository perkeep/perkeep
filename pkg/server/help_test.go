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

package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/types/clientconfig"
)

func TestHelpCredentialFileInstructions(t *testing.T) {
	const authConfig = "userpass:alice:@/server-only/password"
	h := &HelpHandler{}
	h.SetServerConfig(jsonconfig.Obj{
		"auth":    authConfig,
		"baseURL": "https://example.com",
		"prefixes": map[string]any{
			"/sighelper/": map[string]any{
				"handlerArgs": map[string]any{
					"keyId":      "test-key",
					"secretRing": "/server-only/identity-secring.gpg",
				},
			},
		},
	})
	if err := h.InitHandler(nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/help/", "/help/?clientConfig=true"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: status %d: %s", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(path, "?") {
			var cc clientconfig.Config
			if err := json.Unmarshal(rec.Body.Bytes(), &cc); err != nil {
				t.Fatal(err)
			}
			if cc.Servers["default"].Auth != authConfig {
				t.Error("downloaded config changed the auth file reference")
			}
		} else if !strings.Contains(rec.Body.String(), "must also exist on the client") {
			t.Error("help page lacks client-local credential file instructions")
		}
	}
}
