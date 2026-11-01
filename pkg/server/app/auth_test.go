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

package app

import (
	"net/http/httptest"
	"testing"

	"perkeep.org/pkg/auth"
)

func TestAppCredentialsAreNotOwnerCredentials(t *testing.T) {
	h, err := NewHandler(HandlerConfig{
		Program: "publisher", Prefix: "/publish/",
		ServerBaseURL: "https://perkeep.example",
		Listen:        "localhost:3434",
	})
	if err != nil {
		t.Fatal(err)
	}
	mode := h.AuthMode()
	r := httptest.NewRequest("GET", "https://perkeep.example/secrets/", nil)
	mode.AddAuthHeader(r)
	if got := mode.AllowedAccess(r); got != auth.OpAll {
		t.Fatalf("app operations = %v; want ordinary operations %v", got, auth.OpAll)
	}
	if _, ok := mode.(auth.ScopedMode); !ok {
		t.Fatal("app mode is not marked scoped")
	}
	for _, op := range []auth.Operation{auth.OpSecretRead, auth.OpSecretWrite} {
		if auth.AllowedWithAuth(mode, r, op) {
			t.Fatal("generated app credentials permit owner secret access")
		}
	}
}
