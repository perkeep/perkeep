// Copyright (c) 2009 The Go Authors. All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are
// met:
//
//    * Redistributions of source code must retain the above copyright
// notice, this list of conditions and the following disclaimer.
//    * Redistributions in binary form must reproduce the above
// copyright notice, this list of conditions and the following disclaimer
// in the documentation and/or other materials provided with the
// distribution.
//    * Neither the name of Google Inc. nor the names of its
// contributors may be used to endorse or promote products derived from
// this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
// OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
// SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
// LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
// DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
// THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package gotool

import (
	"sort"
	"testing"
)

// This file contains code from the Go distribution.

var matchPatternTests = []stringPairTest{
	{"...", "foo", true},
	{"net", "net", true},
	{"net", "net/http", false},
	{"net/http", "net", false},
	{"net/http", "net/http", true},
	{"net...", "netchan", true},
	{"net...", "net", true},
	{"net...", "net/http", true},
	{"net...", "not/http", false},
	{"net/...", "netchan", false},
	{"net/...", "net", true},
	{"net/...", "net/http", true},
	{"net/...", "not/http", false},
}

func TestMatchPattern(t *testing.T) {
	testStringPairs(t, "matchPattern", matchPatternTests, func(pattern, name string) bool {
		return matchPattern(pattern)(name)
	})
}

var treeCanMatchPatternTests = []stringPairTest{
	{"...", "foo", true},
	{"net", "net", true},
	{"net", "net/http", false},
	{"net/http", "net", true},
	{"net/http", "net/http", true},
	{"net...", "netchan", true},
	{"net...", "net", true},
	{"net...", "net/http", true},
	{"net...", "not/http", false},
	{"net/...", "netchan", false},
	{"net/...", "net", true},
	{"net/...", "net/http", true},
	{"net/...", "not/http", false},
	{"abc.../def", "abcxyz", true},
	{"abc.../def", "xyxabc", false},
	{"x/y/z/...", "x", true},
	{"x/y/z/...", "x/y", true},
	{"x/y/z/...", "x/y/z", true},
	{"x/y/z/...", "x/y/z/w", true},
	{"x/y/z", "x", true},
	{"x/y/z", "x/y", true},
	{"x/y/z", "x/y/z", true},
	{"x/y/z", "x/y/z/w", false},
	{"x/.../y/z", "x/a/b/c", true},
	{"x/.../y/z", "y/x/a/b/c", false},
}

func TestChildrenCanMatchPattern(t *testing.T) {
	testStringPairs(t, "treeCanMatchPattern", treeCanMatchPatternTests, func(pattern, name string) bool {
		return treeCanMatchPattern(pattern)(name)
	})
}

var hasPathPrefixTests = []stringPairTest{
	{"abc", "a", false},
	{"a/bc", "a", true},
	{"a", "a", true},
	{"a/bc", "a/", true},
}

func TestHasPathPrefix(t *testing.T) {
	testStringPairs(t, "hasPathPrefix", hasPathPrefixTests, hasPathPrefix)
}

type stringPairTest struct {
	in1 string
	in2 string
	out bool
}

func testStringPairs(t *testing.T, name string, tests []stringPairTest, f func(string, string) bool) {
	for _, tt := range tests {
		if out := f(tt.in1, tt.in2); out != tt.out {
			t.Errorf("%s(%q, %q) = %v, want %v", name, tt.in1, tt.in2, out, tt.out)
		}
	}
}

// containsString reports whether strings contains x. strings is assumed to be sorted.
func containsString(strings []string, x string) bool {
	return strings[sort.SearchStrings(strings, x)] == x
}

func TestMatchStdPackages(t *testing.T) {
	packages := DefaultContext.matchPackages("std")
	sort.Strings(packages)
	// some common packages all Go versions should have
	commonPackages := []string{"bufio", "bytes", "crypto", "fmt", "io", "os"}
	for _, p := range commonPackages {
		if !containsString(packages, p) {
			t.Errorf("std package set doesn't contain expected package %s", p)
		}
	}
}
