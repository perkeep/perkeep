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

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePDF writes a minimal PDF with the given number of pages to a
// temp file and returns its name. If nesting is positive, the page tree
// root also gets an entry holding that many nested arrays.
func writePDF(t *testing.T, pages, nesting int) string {
	t.Helper()
	var b bytes.Buffer
	var offsets []int
	obj := func(format string, args ...any) {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n", len(offsets))
		fmt.Fprintf(&b, format, args...)
		b.WriteString("\nendobj\n")
	}
	b.WriteString("%PDF-1.4\n")
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	var kids []string
	for i := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", 3+i))
	}
	var extra string
	if nesting > 0 {
		extra = " /X " + strings.Repeat("[", nesting) + strings.Repeat("]", nesting)
	}
	obj("<< /Type /Pages /Kids [%s] /Count %d%s >>", strings.Join(kids, " "), pages, extra)
	for range pages {
		obj("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>")
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)

	name := filepath.Join(t.TempDir(), "test.pdf")
	if err := os.WriteFile(name, b.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestPageCountNative(t *testing.T) {
	// pdfcpu's default configuration would create a config directory
	// under the user's config dir. Make sure we don't.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	got, err := pageCountNative(writePDF(t, 3, 0))
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Errorf("pageCountNative = %d; want 3", got)
	}

	var created []string
	filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if path != home {
			created = append(created, path)
		}
		return nil
	})
	if len(created) > 0 {
		t.Errorf("pageCountNative created files in the home directory: %q", created)
	}
}

// TestPageCountNativeDeeplyNested tests that a PDF nested deeply
// enough to overflow the stack of a naive recursive parser (as
// rsc.io/pdf was; see GO-2026-5781) is rejected rather than crashing.
func TestPageCountNativeDeeplyNested(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if n, err := pageCountNative(writePDF(t, 1, 10_000_000)); err == nil {
		t.Errorf("pageCountNative = %d, nil; want an error", n)
	}
}
