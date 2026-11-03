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

package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"perkeep.org/pkg/filesource"
)

func TestSource(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	outside := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(dir, "a.zip"), "zip contents")
	write(filepath.Join(dir, "sub", "b.txt"), "b")
	write(filepath.Join(outside, "secret"), "outside")
	if err := os.Symlink("a.zip", filepath.Join(dir, "link")); err != nil {
		t.Skip(err) // no symbolic links here
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}

	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !root.Dir || root.ID != "." || root.Name != filepath.Base(dir) {
		t.Errorf("Root = %+v", root)
	}
	files, err := s.List(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	// Not the link leaving the Source.
	if got, want := names, []string{"a.zip", "link", "sub"}; !slices.Equal(got, want) {
		t.Errorf("listed %q; want %q", got, want)
	}
	a := files[0]
	if a.Dir || a.Size != 12 || a.ID != "a.zip" || a.Version == "" {
		t.Errorf("a.zip = %+v", a)
	}
	if !files[2].Dir || files[2].Version != "" {
		t.Errorf("sub = %+v", files[2])
	}
	sub, err := s.List(ctx, files[2])
	if err != nil || len(sub) != 1 || sub[0].ID != "sub/b.txt" {
		t.Errorf("List(sub) = %+v, %v", sub, err)
	}

	got, err := filesource.ReadRange(ctx, s, a, 4, 8)
	if err != nil || string(got) != "contents" {
		t.Errorf("ReadRange = %q, %v; want %q", got, err, "contents")
	}
	if _, err := s.OpenRange(ctx, a, 10, 3); err == nil {
		t.Error("OpenRange past the end succeeded")
	}
	for _, id := range []string{"../" + filepath.Base(outside) + "/secret", "escape", "/etc/passwd"} {
		if f, err := s.Stat(ctx, id); err == nil {
			t.Errorf("Stat(%q) = %+v; want an error, as it leaves the Source", id, f)
		}
	}

	// Once a.zip changes, reading what was listed fails.
	write(filepath.Join(dir, "a.zip"), "changed zip contents")
	if _, err := s.OpenRange(ctx, a, 0, 4); !errors.Is(err, filesource.ErrChanged) {
		t.Errorf("OpenRange of a changed file: %v; want ErrChanged", err)
	}
}
