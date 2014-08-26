/*
Copyright 2013 The Camlistore Authors.

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

package client

import (
	"path/filepath"
	"testing"
)

// this test demonstrates why http://camlistore.org/r/2296 was needed for
// matching to correctly work when the checker has more than one pattern of the
// same kind. Before the fix, this test would fail because the first
// isShellPatternMatch closure of the checker would not end up using the
// intended "*.jpg" pattern.
func TestIgnoreMultiPattern(t *testing.T) {
	ignoredShellPattern := []string{
		"*.jpg",
		"*.png",
		"*.gif",
	}
	ignoreChecker := newIgnoreChecker(ignoredShellPattern)
	toIgnore := "/home/foo/Downloads/pony.jpg"
	if ignoreChecker(toIgnore) != true {
		t.Errorf("Failed to ignore %v with %q among multiple shell patterns in ignore list.", toIgnore, ignoredShellPattern[0])
	}
}

func TestIsIgnoredFile(t *testing.T) {
	old := osutilHomeDir
	defer func() { osutilHomeDir = old }()
	osutilHomeDir = func() string {
		return "/Fake/Home/Camli"
	}

	home := osutilHomeDir()
	fullpath := filepath.Join(home, "Downloads", "pony.jpg")
	var wantIgnored = map[string]bool{
		filepath.Join(home, filepath.FromSlash("Downloads/pony.jpg")): true,
		filepath.Join(home, filepath.FromSlash("Downloads/pony.*")):   true,
		filepath.Join(home, filepath.FromSlash("Downloads/*.jpg")):    true,
		filepath.Join(home, filepath.FromSlash("Downloads/*")):        true,
		"*.jpg":  true,
		"pony.*": true,
		"*.foo":  false,
		"foo.*":  false,
		filepath.Join(home, "Downloads"):           true,
		filepath.Join(home, "Down"):                false,
		filepath.FromSlash("~/Downloads/pony.jpg"): true,
		filepath.FromSlash("~/Downloads/pony.*"):   true,
		filepath.FromSlash("~/Downloads/*.jpg"):    true,
		filepath.FromSlash("~/Downloads"):          true,
		filepath.FromSlash("~/Down"):               false,
		filepath.FromSlash("~/DownloadsAndMore"):   false,
		home:        true,
		"Downloads": true,
		"Down":      false,
	}
	for pattern, want := range wantIgnored {
		ignoreChecker := newIgnoreChecker([]string{pattern})
		if ignoreChecker(fullpath) != want {
			t.Errorf("%v not ignored; did not match %v", fullpath, pattern)
		}
	}
}

type patternTest struct {
	name     string
	fn       func(string, string) bool
	pattern  string
	fullpath string
	want     bool
}

func TestIgnoreFns(t *testing.T) {
	tests := []patternTest{
		{
			name:     "isShellPatternMatch",
			fn:       isShellPatternMatch,
			pattern:  "/home/pony/rainbow.*",
			fullpath: "/home/pony/rainbow.jpg",
			want:     true,
		},
		{
			name:     "isShellPatternMatch",
			fn:       isShellPatternMatch,
			pattern:  "rainbow.*",
			fullpath: "/home/pony/rainbow.jpg",
			want:     true,
		},
		{
			name:     "isShellPatternMatch",
			fn:       isShellPatternMatch,
			pattern:  "*.jpg",
			fullpath: "/home/pony/rainbow.jpg",
			want:     true,
		},
		{
			name:     "isShellPatternMatch",
			fn:       isShellPatternMatch,
			pattern:  "pony/rainbow.*",
			fullpath: "/home/pony/rainbow.jpg",
			want:     false,
		},
		{
			name:     "hasDirPrefix",
			fn:       hasDirPrefix,
			pattern:  "/home/pony",
			fullpath: "/home/pony/rainbow.jpg",
			want:     true,
		},
		{
			name:     "hasDirPrefix",
			fn:       hasDirPrefix,
			pattern:  "/home/lizard",
			fullpath: "/home/pony/rainbow.jpg",
			want:     false,
		},
		{
			name:     "hasComponent",
			fn:       hasComponent,
			pattern:  "pony",
			fullpath: "/home/pony/rainbow.jpg",
			want:     true,
		},
		{
			name:     "hasComponent",
			fn:       hasComponent,
			pattern:  "pon",
			fullpath: "/home/pony/rainbow.jpg",
			want:     false,
		},
	}
	for _, v := range tests {
		if v.fn(v.pattern, v.fullpath) != v.want {
			t.Errorf("%v(%q, %q) != %v", v.name, v.pattern, v.fullpath, v.want)
		}
	}
}
