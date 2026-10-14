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

package webserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStartupHandler(t *testing.T) {
	s := New()
	s.SetStartupHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "starting")
	}))
	s.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ready")
	})
	get := func() string {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		return rec.Body.String()
	}
	if got := get(); got != "starting" {
		t.Errorf("before EndStartup, got %q; want the startup handler's response", got)
	}
	s.EndStartup()
	if got := get(); got != "ready" {
		t.Errorf("after EndStartup, got %q; want the registered handler's response", got)
	}
}
