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

import (
	"context"
	"io"
	"maps"
	"strings"
	"testing"

	"perkeep.org/internal/takeout"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/upload"
)

// readArchiveTree returns the contents of the files in the directory
// dir, by their paths under it, and the names of the directories.
func readArchiveTree(t *testing.T, st *takeoutStore, dir blob.Ref) (files map[string]string, dirName string) {
	t.Helper()
	ctx := context.Background()
	files = make(map[string]string)
	var walk func(br blob.Ref, prefix string) string
	var setMembers func(ss blob.Ref) []blob.Ref
	setMembers = func(ss blob.Ref) []blob.Ref {
		b, err := st.c.FetchSchemaBlob(ctx, ss)
		if err != nil {
			t.Fatal(err)
		}
		members := b.StaticSetMembers()
		for _, sub := range b.StaticSetMergeSets() {
			members = append(members, setMembers(sub)...)
		}
		return members
	}
	walk = func(br blob.Ref, prefix string) string {
		b, err := st.c.FetchSchemaBlob(ctx, br)
		if err != nil {
			t.Fatal(err)
		}
		name := b.FileName()
		switch b.Type() {
		case schema.TypeDirectory:
			ss, ok := b.DirectoryEntries()
			if !ok {
				t.Fatalf("directory %v has no entries", br)
			}
			for _, m := range setMembers(ss) {
				walk(m, prefix+name+"/")
			}
		case schema.TypeFile:
			fr, err := schema.NewFileReader(ctx, st.c, br)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(fr)
			fr.Close()
			if err != nil {
				t.Fatal(err)
			}
			files[prefix+name] = string(data)
		default:
			t.Fatalf("%v is a %q", br, b.Type())
		}
		return name
	}
	dirName = walk(dir, "")
	return files, dirName
}

func TestTakeoutArchive(t *testing.T) {
	st := newTakeoutTestStore(t)
	ctx := context.Background()
	photo := strings.Repeat("photo ", 1000)
	video := strings.Repeat("video ", 100000) // more than one chunk

	// The server already has the photo.
	if _, err := upload.NewFileFromReader(st.c, "elsewhere.jpg", strings.NewReader(photo), nil).Run(ctx); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	zip1 := map[string]string{
		"Takeout/archive_browser.html":                        "<html>",
		"Takeout/Google Photos/2018-07-13/A.JPG":              photo,
		"Takeout/Google Photos/2018-07-13/A.JPG.json":         testSidecar("A.JPG", ""),
		"Takeout/Google Photos/2018-07-13/A-edited.JPG":       photo + "edited",
		"Takeout/Google Photos/Trip/metadata.json":            `{"albumData": {"title": "Trip"}}`,
		"Takeout/Google Photos/Trip/A.JPG":                    photo,
		"Takeout/Google Photos/2018-07-13/empty-file.txt":     "",
		"Takeout/Google Photos/2018-07-14/VID.MP4.json":       testSidecar("VID.MP4", ""),
		"Takeout/Google Photos/2018-07-14/VID.MP4":            video,
		"Takeout/Google Photos/2018-07-14/IMG(1).jpg":         photo + "1",
		"Takeout/Google Photos/2018-07-14/IMG.jpg(1).json":    testSidecar("IMG(1).jpg", ""),
		"Takeout/Google Photos/2018-07-14/photo-only-in-zip1": "x",
	}
	zip2 := map[string]string{
		"Takeout/Google Photos/Trip/VID.MP4": video,
		"Takeout/Mail/All mail.mbox":         "From x",
	}
	writeTakeoutZip(t, dir, "takeout-20240101T000000Z-001.zip", zip1)
	writeTakeoutZip(t, dir, "takeout-20240101T000000Z-002.zip", zip2)
	exps, err := takeout.LocalExports(dir)
	if err != nil {
		t.Fatal(err)
	}
	exp := exps[0]
	if err := exp.LoadTOCs(ctx, 2); err != nil {
		t.Fatal(err)
	}
	a := &takeoutArchive{st: st, exp: exp, workers: 4}
	root, err := a.run(ctx)
	if err != nil {
		t.Fatal(err)
	}

	got, name := readArchiveTree(t, st, root)
	if name != "takeout-20240101T000000Z" {
		t.Errorf("root directory named %q", name)
	}
	want := make(map[string]string)
	for zipName, files := range map[string]map[string]string{
		"takeout-20240101T000000Z-001.zip": zip1,
		"takeout-20240101T000000Z-002.zip": zip2,
	} {
		for p, data := range files {
			want["takeout-20240101T000000Z/"+zipName+"/"+p] = data
		}
	}
	if !maps.Equal(got, want) {
		for p := range want {
			if got[p] != want[p] {
				t.Errorf("archive's %s: got %d bytes, want %d", p, len(got[p]), len(want[p]))
			}
		}
		for p := range got {
			if _, ok := want[p]; !ok {
				t.Errorf("archive has unexpected %s", p)
			}
		}
	}

	pn, err := st.plannedPermanodeRef(ctx, exportPermanodeKeyPrefix+exp.Name)
	if err != nil {
		t.Fatal(err)
	}
	attrs := permanodeAttrs(t, st, pn)
	for attr, want := range map[string]string{
		"camliContent":  root.String(),
		"camliNodeType": nodeTypeTakeoutExport,
		"title":         "Google Takeout Raw Zips (takeout-20240101T000000Z)",
		"dateCreated":   "2024-01-01T00:00:00Z",
		"takeoutExport": "takeout-20240101T000000Z",
	} {
		if got := attrs.Get(attr); got != want {
			t.Errorf("export permanode's %s = %q; want %q", attr, got, want)
		}
	}

	// Archiving it again makes the same directory.
	a = &takeoutArchive{st: st, exp: exp, workers: 4}
	root2, err := a.run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if root2 != root {
		t.Errorf("archiving again made directory %v; want %v", root2, root)
	}
}
