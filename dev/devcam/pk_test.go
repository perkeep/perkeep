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

package main

import "testing"

func TestToolNeedsPerkeepd(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "copy options", args: []string{"copy-storage", "-src", "source", "-dest", "destination"}, want: true},
		{name: "global bools", args: []string{"--verbose", "-help=false", "copy-storage"}, want: true},
		{name: "end flags", args: []string{"--", "copy-storage"}, want: true},
		{name: "global help", args: []string{"-help", "copy-storage"}},
		{name: "global version", args: []string{"-version", "copy-storage"}},
		{name: "global legal", args: []string{"-legal", "copy-storage"}},
		{name: "bad global flag", args: []string{"-unknown", "copy-storage"}},
		{name: "no mode"},
		{name: "another mode flag value", args: []string{"sync", "-src", "copy-storage"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolNeedsPerkeepd(tt.args); got != tt.want {
				t.Errorf("toolNeedsPerkeepd(%q) = %v; want %v", tt.args, got, tt.want)
			}
		})
	}
}
