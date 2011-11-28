// Copyright 2011 The LevelDB-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package db

import (
	"testing"
)

func TestDefCmp(t *testing.T) {
	testCases := []struct {
		a, b, want string
	}{
		// Examples from the doc comments.
		{"black", "blue", "blb"},
		{"green", "", "h"},
		// Non-empty b values. The C++ Level-DB code calls these separators.
		{"", "2", ""},
		{"1", "2", "1"},
		{"1", "29", "1"},
		{"13", "19", "14"},
		{"13", "99", "2"},
		{"135", "19", "14"},
		{"1357", "19", "14"},
		{"1357", "2", "1357"},
		{"13\xff", "14", "13\xff"},
		{"13\xff", "19", "14"},
		{"1\xff\xff", "19", "1\xff\xff"},
		{"1\xff\xff", "2", "1\xff\xff"},
		{"1\xff\xff", "9", "2"},
		// Empty b values. The C++ Level-DB code calls these successors.
		{"", "", ""},
		{"1", "", "2"},
		{"11", "", "2"},
		{"11\xff", "", "2"},
		{"1\xff", "", "2"},
		{"1\xff\xff", "", "2"},
		{"\xff", "", "\xff"},
		{"\xff\xff", "", "\xff\xff"},
		{"\xff\xff\xff", "", "\xff\xff\xff"},
	}
	for _, tc := range testCases {
		const s = "pqrs"
		got := string(DefaultComparer.AppendSeparator([]byte(s), []byte(tc.a), []byte(tc.b)))
		if got != s+tc.want {
			t.Errorf("a, b = %q, %q: got %q, want %q", tc.a, tc.b, got, s+tc.want)
		}
	}
}
