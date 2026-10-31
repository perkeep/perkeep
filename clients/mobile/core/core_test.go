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
	"encoding/json"
	"net/http"
	"runtime"
	"testing"
)

func TestHello(t *testing.T) {
	status, body := call("GET", "/hello", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d; body: %s", status, body)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["goos"] != runtime.GOOS {
		t.Errorf("goos = %q; want %q", got["goos"], runtime.GOOS)
	}
}

func TestNotFound(t *testing.T) {
	if status, _ := call("GET", "/nope", nil); status != http.StatusNotFound {
		t.Errorf("status = %d; want 404", status)
	}
}
