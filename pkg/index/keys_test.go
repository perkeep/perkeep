/*
Copyright 2011 Google Inc.

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

package index

import (
	"testing"
)

func TestKeyPrefix(t *testing.T) {
	if g, e := keyRecentPermanode.Prefix("ABC"), "recpn|ABC|"; g != e {
		t.Errorf("recpn = %q; want %q", g, e)
	}
}

func TestTypeOfKey(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"foo:bar", "foo"},
		{"foo|bar", "foo"},
		{"foo|bar:blah", "foo"},
		{"foo:bar|blah", "foo"},
		{"fooo", ""},
	}
	for _, tt := range tests {
		if got := typeOfKey(tt.in); got != tt.want {
			t.Errorf("typeOfKey(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}
