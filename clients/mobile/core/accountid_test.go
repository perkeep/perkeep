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

package main

import (
	"net/http"
	"testing"
)

func TestAddAccountWithID(t *testing.T) {
	s := newTestServer(t, t.TempDir())
	var a Account
	do(t, s, "POST", "/accounts", map[string]string{"id": "abc123", "server": "pk.example.com"}, http.StatusCreated, &a)
	if a.ID != "abc123" || a.Server != "https://pk.example.com" {
		t.Errorf("account = %+v", a)
	}
	do(t, s, "POST", "/accounts", map[string]string{"id": "abc123", "server": "pk.example.com"}, http.StatusConflict, nil)
	for _, bad := range []string{"../x", "ABC", "a b"} {
		do(t, s, "POST", "/accounts", map[string]string{"id": bad, "server": "pk.example.com"}, http.StatusBadRequest, nil)
	}
}
