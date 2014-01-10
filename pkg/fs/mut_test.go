// +build linux darwin

/*
Copyright 2014 Google Inc.

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

package fs

import (
	"testing"
	"time"
)

func TestDeleteEligibility(t *testing.T) {
	tests := []struct {
		name string
		ts   time.Time
		exp  bool
	}{
		{"zero", time.Time{}, true},
		{"now", time.Now(), false},
		{"future", time.Now().Add(time.Hour), false},
		{"recent", time.Now().Add(-(deletionRefreshWindow / 2)), false},
		{"past", time.Now().Add(-(deletionRefreshWindow * 2)), true},
	}

	for _, test := range tests {
		d := &mutDir{localCreateTime: test.ts}
		if d.eligibleToDelete() != test.exp {
			t.Errorf("Expected %v %T/%v", test.exp, d, test.name)
		}
		f := &mutFile{localCreateTime: test.ts}
		if f.eligibleToDelete() != test.exp {
			t.Errorf("Expected %v for %T/%v", test.exp, f, test.name)
		}
	}
}
