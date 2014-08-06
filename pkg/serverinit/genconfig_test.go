/*
Copyright 2014 The Camlistore Authors

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

package serverinit

import "testing"

func TestParseUserHostPass(t *testing.T) {
	tests := []struct {
		in                   string
		user, host, password string
	}{
		{in: "foo"},
		{in: "foo@bar"},
		{"bob@server:pass", "bob", "server", "pass"},
		{"bob@server:3307:pass", "bob", "server:3307", "pass"},
		{"bob@server:pass:word", "bob", "server", "pass:word"},
		{"bob@server:9999999:word", "bob", "server", "9999999:word"},
		{"bob@server:123:123:word", "bob", "server:123", "123:word"},
		{"bob@server:123", "bob", "server", "123"},
		{"bob@server:123:", "bob", "server:123", ""},
	}
	for _, tt := range tests {
		user, host, password, ok := parseUserHostPass(tt.in)
		if ok != (user != "" || host != "" || password != "") {
			t.Errorf("For input %q, inconsistent output %q, %q, %q, %v", tt.in, user, host, password, ok)
			continue
		}
		if user != tt.user || host != tt.host || password != tt.password {
			t.Errorf("parseUserHostPass(%q) = %q, %q, %q; want %q, %q, %q", tt.in, user, host, password, tt.user, tt.host, tt.password)
		}
	}
}
