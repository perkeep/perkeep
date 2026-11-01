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

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOwnerOperations(t *testing.T) {
	if OpAll&(OpSecretRead|OpSecretWrite) != 0 {
		t.Fatal("the discovery token's operations include secrets")
	}
	vivify := "upload-only"
	mode := &UserPass{Username: "owner", Password: "password", VivifyPass: &vivify}
	for _, tt := range []struct {
		name  string
		auth  func(*http.Request)
		owner bool
		opAll bool
	}{
		{"owner password", func(r *http.Request) { r.SetBasicAuth("owner", "password") }, true, true},
		{"wrong password", func(r *http.Request) { r.SetBasicAuth("owner", "wrong") }, false, false},
		{"vivify password", func(r *http.Request) { r.SetBasicAuth("owner", vivify) }, false, false},
		{"no credentials", func(*http.Request) {}, false, false},
		{"UI token", func(r *http.Request) { r.Header.Set("Authorization", "Token "+Token()) }, false, true},
		{"websocket token", func(r *http.Request) {
			r.Header.Set("Upgrade", "websocket")
			r.URL.RawQuery = "authtoken=" + Token()
		}, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://perkeep.example/secrets/", nil)
			tt.auth(r)
			for _, op := range []Operation{OpSecretRead, OpSecretWrite, OpOwner} {
				if got := AllowedWithAuth(mode, r, op); got != tt.owner {
					t.Errorf("AllowedWithAuth(%v) = %v; want %v", op, got, tt.owner)
				}
			}
			if got := AllowedWithAuth(mode, r, OpAll); got != tt.opAll {
				t.Errorf("ordinary access = %v; want %v", got, tt.opAll)
			}
		})
	}
}

type scopedOwnerMode struct{ None }

func (scopedOwnerMode) ScopedOnly() {}

func TestOwnerOperationsRejectScopedModesAndGrants(t *testing.T) {
	old := append([]AuthMode(nil), modes()...)
	t.Cleanup(func() { modesPtr.Store(&old) })
	SetMode(scopedOwnerMode{})
	r := httptest.NewRequest("GET", "https://perkeep.example/secrets/", nil)
	for _, op := range []Operation{OpSecretRead, OpSecretWrite} {
		if AllowedUnscoped(r, op) {
			t.Fatal("scoped credentials granted owner-only secret access")
		}
	}
	SetMode(None{})
	r = r.WithContext(GrantCtxKey.WithValue(r.Context(), OpOwner))
	if AllowedUnscoped(r, OpSecretRead) || AllowedUnscoped(r, OpSecretWrite) {
		t.Fatal("listener grant granted owner-only secret access")
	}
}
