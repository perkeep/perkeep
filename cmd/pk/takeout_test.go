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
	"net/url"
	"strings"
	"testing"
)

func TestTakeoutAuthCode(t *testing.T) {
	for _, tt := range []struct {
		pasted   string
		code     string
		errMatch string
	}{
		{"http://127.0.0.1:41234/?state=s1&code=4/abc&scope=https://www.googleapis.com/auth/drive.readonly", "4/abc", ""},
		{"http://127.0.0.1:41234/?state=old&code=4/abc", "", "wrong state"},
		{"http://127.0.0.1:41234/?state=s1&error=access_denied", "", "access_denied"},
		{"http://127.0.0.1:41234/?state=s1", "", "without a code"},
	} {
		u, err := url.Parse(tt.pasted)
		if err != nil {
			t.Fatal(err)
		}
		code, err := takeoutAuthCode(u.Query(), "s1")
		if code != tt.code || (err == nil) != (tt.errMatch == "") || err != nil && !strings.Contains(err.Error(), tt.errMatch) {
			t.Errorf("takeoutAuthCode(%q) = %q, %v; want %q, error matching %q", tt.pasted, code, err, tt.code, tt.errMatch)
		}
	}
}
