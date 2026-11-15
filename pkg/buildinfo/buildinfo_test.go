/*
Copyright 2014 The Perkeep Authors

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

package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestTestingLinked(t *testing.T) {
	if !TestingLinked() {
		t.Error("testingLinked = false; want true")
	}
}

func TestVCSGitInfo(t *testing.T) {
	git := func(kv ...string) []debug.BuildSetting {
		s := []debug.BuildSetting{{Key: "vcs", Value: "git"}}
		for i := 0; i < len(kv); i += 2 {
			s = append(s, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return s
	}
	const rev = "1379c4ddd1e0f6e2c3b4a5968778695a4b3c2d1e"
	tests := []struct {
		name     string
		settings []debug.BuildSetting
		want     string
	}{
		{"none", nil, ""},
		{"clean", git("vcs.revision", rev, "vcs.time", "2026-11-15T16:44:26Z", "vcs.modified", "false"), "2026-11-15-1379c4ddd1"},
		{"modified", git("vcs.revision", rev, "vcs.time", "2026-11-15T16:44:26Z", "vcs.modified", "true"), "2026-11-15-1379c4ddd1+"},
		{"no time", git("vcs.revision", rev), "1379c4ddd1"},
		{"short revision", git("vcs.revision", "1379c4d"), ""},
		{"not git", []debug.BuildSetting{{Key: "vcs", Value: "hg"}, {Key: "vcs.revision", Value: rev}}, ""},
	}
	for _, tt := range tests {
		if got := vcsGitInfo(tt.settings); got != tt.want {
			t.Errorf("%s: vcsGitInfo = %q; want %q", tt.name, got, tt.want)
		}
	}
}
