// Copyright (c) 2016 Paul Jolly <paul@myitcv.org.uk>, all rights reserved.
// Use of this document is governed by a license found in the LICENSE document.

package gogenerate

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestFilesContaining(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Error(err)
	}

	checks := []struct {
		d       string
		cmds    []string
		matches []string
	}{
		{"_testFiles/eg01", []string{"ls", "/bin/ls"}, []string{"a.go", "b.go", "c.go", "d.go"}},
	}

Checks:
	for _, c := range checks {

		path := filepath.Join(cwd, c.d)

		res, err := FilesContainingCmd(path, c.cmds...)
		if err != nil {
			t.Errorf("Got unexpected error find matches in %v: %v", c.d, err)
			continue Checks
		}

		if len(res) != len(c.matches) {
			t.Errorf("Matches not up to expectations: %v vs %v", res, c.matches)
			continue Checks
		}

		// just in case we were sloppy in the test table
		sort.Sort(byBase(c.matches))
		for i := range res {
			if res[i] != c.matches[i] {
				t.Errorf("Matches not up to expectations: %v vs %v", res, c.matches)
				continue Checks
			}
		}
	}
}

func TestNameFile(t *testing.T) {
	checks := []struct {
		n string
		c string
		r string
	}{
		{"a", "bananaGen", "gen_a_bananaGen.go"},
	}

	for _, c := range checks {
		r := NameFile(c.n, c.c)
		if r != c.r {
			t.Errorf("Expected NameFile(%q, %q) to be %v got %v", c.n, c.c, c.r, r)
		}
	}
}

func TestNameTestFile(t *testing.T) {
	checks := []struct {
		n string
		c string
		r string
	}{
		{"a", "bananaGen", "gen_a_bananaGen_test.go"},
	}

	for _, c := range checks {
		r := NameTestFile(c.n, c.c)
		if r != c.r {
			t.Errorf("Expected NameFile(%q, %q) to be %v got %v", c.n, c.c, c.r, r)
		}
	}
}

func TestFileIsGenerated(t *testing.T) {
	checks := []struct {
		p   string
		cmd string
		r   bool
	}{
		{"is_a_gen_file", "", false},
		{"gen_file", "", false},
		{"gen_cmd.go", "cmd", true},
		{"gen_my_cmd.go", "cmd", true},
		{"genfile.go", "", false},
		{"/path/to/gen_file", "", false},
		{"/path/to/gen_cmd.go", "cmd", true},
		{"/path/to/gen_my_cmd.go", "cmd", true},
		{"/path/to/genfile.go", "", false},
		{"/", "", false},
	}

	for _, c := range checks {
		cmd, r := FileIsGenerated(c.p)
		if r != c.r || cmd != c.cmd {
			t.Errorf("Expected FileIsGenerated(%q) to be (%v, %q) got (%v, %q)", c.p, c.r, c.cmd, r, cmd)
		}
	}
}

func TestFileGeneratedBy(t *testing.T) {
	checks := []struct {
		n string
		c string
		r bool
	}{
		{"gen_bananaGen.go", "bananaGen", true},
		{"gen_bananaGen_test.go", "bananaGen", true},
		{"gen_a_bananaGen.go", "bananaGen", true},
		{"gen_a_bananaGen_test.go", "bananaGen", true},
		{"gen_abananaGen.go", "bananaGen", false},
		{"gen_", "bananaGen", false},
	}

	for _, c := range checks {
		r := FileGeneratedBy(c.n, c.c)
		if r != c.r {
			t.Errorf("Expected FileGeneratedBy(%q, %q) to be %v got %v", c.n, c.c, c.r, r)
		}
	}
}

func TestNameFileFromFile(t *testing.T) {
	checks := []struct {
		n  string
		o  string
		ok bool
	}{
		{"/path/to/a.txt", "", false},
		{"/path/to/a.go", "/path/to/gen_a_bananaGen.go", true},
		{"path/to/a.go", "path/to/gen_a_bananaGen.go", true},
		{"a.go", "gen_a_bananaGen.go", true},
		{"/path/to/a_test.go", "/path/to/gen_a_bananaGen_test.go", true},
		{"path/to/a_test.go", "path/to/gen_a_bananaGen_test.go", true},
		{"a_test.go", "gen_a_bananaGen_test.go", true},
	}

	for _, c := range checks {
		o, ok := NameFileFromFile(c.n, "bananaGen")
		if o != c.o || ok != c.ok {
			t.Errorf("Expected NameFileFromFile(%q) to be %v got %v", c.n, c.o, o)
		}
	}
}

func TestCommentLicenseHeader(t *testing.T) {
	checks := []struct {
		fn  string
		exp string
	}{
		{"", ""},
		{"_testFiles/licenseFile.txt", "// Copyright (c) Bananaman 2016\n// Line 2\n\n"},
	}

	for _, c := range checks {
		res, err := CommentLicenseHeader(&c.fn)

		if err != nil {
			t.Fatalf("CommentLicenseHeader(&%q) failed when it should not have: %v", c.fn, err)
		}

		if res != c.exp {
			t.Errorf("Actual output %q was not as expected %q", res, c.exp)
		}
	}
}
