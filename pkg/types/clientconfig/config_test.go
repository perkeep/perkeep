/*
Copyright 2026 The Perkeep Authors.

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

package clientconfig

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"go4.org/jsonconfig"
)

func TestGenerateClientConfigPreservesAuthReferences(t *testing.T) {
	t.Chdir(t.TempDir())
	const private = "@private-server-password:with:colons:+localhost:vivify=private"
	for _, name := range []string{"password", "vivify"} {
		if err := os.WriteFile(name, []byte(" \t"+private+"\r\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, auth := range []string{
		"userpass:alice:@./password:vivify=@./vivify",
		"basic:alice:@./password",
		"devauth:@./password",
		"userpass:alice:@@literal:vivify=@@vivify",
		"basic:alice:@@literal",
		"devauth:@@literal",
		"userpass:alice:@./missing:vivify=@./also-missing",
		"basic:alice:@./missing",
		"devauth:@./missing",
		"userpass:alice:@.",
	} {
		t.Run(auth, func(t *testing.T) {
			const ring = "./missing-identity-secring.gpg"
			serverConfig := jsonconfig.Obj{
				"auth":   auth,
				"listen": "localhost:3179",
				"prefixes": map[string]any{
					"/sighelper/": map[string]any{
						"handlerArgs": map[string]any{
							"keyId":      "2931A67C26F5ABDA",
							"secretRing": ring,
						},
					},
				},
			}
			got, err := GenerateClientConfig(serverConfig)
			if err != nil {
				t.Fatal(err)
			}
			if got.Servers["default"].Auth != auth {
				t.Errorf("auth = %q; want verbatim %q", got.Servers["default"].Auth, auth)
			}
			if serverConfig["auth"] != auth {
				t.Errorf("input auth changed to %q", serverConfig["auth"])
			}
			if got.IdentitySecretRing != ring {
				t.Errorf("identitySecretRing = %q; want %q", got.IdentitySecretRing, ring)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), private) {
				t.Error("generated JSON contains private credential file contents")
			}
		})
	}
}
