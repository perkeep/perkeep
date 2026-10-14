//go:build ignore

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

import "testing"

func TestBuildTags(t *testing.T) {
	for _, tt := range []struct {
		arch string
		want string
	}{
		{"386", "nodynamic"},
		{"arm", "nodynamic"},
		{"mips", "nodynamic"},
		{"mipsle", "nodynamic"},
		{"amd64", ""},
		{"arm64", ""},
		{"mips64", ""},
		{"mips64le", ""},
		{"ppc64", ""},
		{"ppc64le", ""},
		{"riscv64", ""},
		{"s390x", ""},
		{"loong64", ""},
		{"wasm", ""},
	} {
		t.Run(tt.arch, func(t *testing.T) {
			if got := buildTags(tt.arch, false); got != tt.want {
				t.Errorf("buildTags(%q, false) = %q; want %q", tt.arch, got, tt.want)
			}
			want := "netgo,osusergo"
			if tt.want != "" {
				want += "," + tt.want
			}
			if got := buildTags(tt.arch, true); got != want {
				t.Errorf("buildTags(%q, true) = %q; want %q", tt.arch, got, want)
			}
		})
	}
}
