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

package takeoutimport

import (
	"context"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"perkeep.org/internal/takeout"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/upload"
)

// readArchiveTree returns the contents of the files in the directory
// dir, by their paths under it, and the names of the directories.
func readArchiveTree(t *testing.T, st *Importer, dir blob.Ref) (files map[string]string, dirName string) {
	t.Helper()
	ctx := context.Background()
	files = make(map[string]string)
	var walk func(br blob.Ref, prefix string) string
	var setMembers func(ss blob.Ref) []blob.Ref
	setMembers = func(ss blob.Ref) []blob.Ref {
		b, err := st.client().FetchSchemaBlob(ctx, ss)
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
		b, err := st.client().FetchSchemaBlob(ctx, br)
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
			// Its zip entry's time is when the export was made.
			if mt := b.ModTime(); !mt.IsZero() {
				t.Errorf("%s%s has modtime %v, from its zip entry; want none", prefix, name, mt)
			}
			fr, err := schema.NewFileReader(ctx, st.client(), br)
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
	st := newTestImporter(t)
	ctx := context.Background()
	photo := strings.Repeat("photo ", 1000)
	video := strings.Repeat("video ", 100000) // more than one chunk

	// The server already has the photo.
	if _, err := upload.NewFileFromReader(st.client(), "elsewhere.jpg", strings.NewReader(photo), nil).Run(ctx); err != nil {
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
	// Flushing the session often, as for many more files.
	st.Workers = 4
	st.Server.(*Client).FlushEvery = 3
	root, err := st.Archive(ctx, exp)
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

	pn, err := st.plannedPermanodeRef(ctx, ExportPermanodeKeyPrefix+exp.Name)
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
	st.Server.(*Client).FlushEvery = 0
	root2, err := st.Archive(ctx, exp)
	if err != nil {
		t.Fatal(err)
	}
	if root2 != root {
		t.Errorf("archiving again made directory %v; want %v", root2, root)
	}
}

// TestTakeoutImportPhotos checks that uploading an export's photos also
// archives it, once.
func TestTakeoutImportPhotos(t *testing.T) {
	st := newTestImporter(t)
	ctx := context.Background()
	photo := strings.Repeat("photo ", 1000)
	files := map[string]string{
		"Takeout/Google Photos/2018-07-13/A.JPG":             photo,
		"Takeout/Google Photos/2018-07-13/A.JPG.json":        testSidecar("A.JPG", "Space Needle"),
		"Takeout/Google Photos/2018-07-13/A-edited.JPG":      photo + "edited",
		"Takeout/Maps/My labeled places/Labeled places.json": "{}",
	}
	dir := t.TempDir()
	writeTakeoutZip(t, dir, "takeout-20240101T000000Z-001.zip", files)
	exps, err := takeout.LocalExports(dir)
	if err != nil {
		t.Fatal(err)
	}
	exp := exps[0]
	if err := exp.LoadTOCs(ctx, 2); err != nil {
		t.Fatal(err)
	}
	importPhotos := func() {
		t.Helper()
		var ents []takeout.Entry
		for _, e := range exp.Entries() {
			if takeout.IsPhotosMedia(e.File.Name) && !takeout.IsEdited(e.File.Name) {
				ents = append(ents, e)
			}
		}
		cs := takeout.Contents(ents)
		_, missing, err := takeout.Check(ctx, st.store(false), cs, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Import(ctx, exp, cs, missing, Options{Permanodes: true}); err != nil {
			t.Fatalf("Import: %v", err)
		}
	}
	importPhotos()

	if got := permanodeAttrs(t, st, photoPermanode(t, st, "A.JPG")).Get("description"); got != "Space Needle" {
		t.Errorf("A.JPG's description = %q; want Space Needle", got)
	}
	pn, err := st.plannedPermanodeRef(ctx, ExportPermanodeKeyPrefix+exp.Name)
	if err != nil {
		t.Fatal(err)
	}
	root, ok := blob.Parse(permanodeAttrs(t, st, pn).Get("camliContent"))
	if !ok {
		t.Fatal("export's permanode has no camliContent")
	}
	got, _ := readArchiveTree(t, st, root)
	want := make(map[string]string)
	for p, data := range files {
		want["takeout-20240101T000000Z/takeout-20240101T000000Z-001.zip/"+p] = data
	}
	if !maps.Equal(got, want) {
		t.Errorf("archive has %d files: %v; want %d: %v", len(got), slices.Sorted(maps.Keys(got)), len(want), slices.Sorted(maps.Keys(want)))
	}

	// Importing it again, as after an interruption, finds it all done.
	// Only the metadata sync uploads anything: the planned permanode of
	// the albums' root (one per album, with albums), which it has.
	before := st.client().Stats().UploadRequests
	importPhotos()
	if after := st.client().Stats().UploadRequests; after.Blobs-before.Blobs > 1 {
		t.Errorf("importing again requested %d more uploads (%d bytes); want just the albums' root permanode",
			after.Blobs-before.Blobs, after.Bytes-before.Bytes)
	}
}

// TestTakeoutArchiveReplacesOldArchive checks that archiving an export
// archived as another directory (as by an older pk takeout, its claim
// dated by the export or later) makes the new one its permanode's
// contents.
func TestTakeoutArchiveReplacesOldArchive(t *testing.T) {
	st := newTestImporter(t)
	ctx := context.Background()
	dir := t.TempDir()
	writeTakeoutZip(t, dir, "takeout-20240101T000000Z-001.zip", map[string]string{
		"Takeout/Google Photos/2018-07-13/A.JPG": strings.Repeat("photo ", 1000),
	})
	exps, err := takeout.LocalExports(dir)
	if err != nil {
		t.Fatal(err)
	}
	exp := exps[0]
	if err := exp.LoadTOCs(ctx, 2); err != nil {
		t.Fatal(err)
	}
	pn, err := st.plannedPermanode(ctx, ExportPermanodeKeyPrefix+exp.Name)
	if err != nil {
		t.Fatal(err)
	}
	old := blob.RefFromString("an older archive's directory")
	if err := st.applyChanges(ctx, pn, []attrChange{{"set", "camliContent", old.String()}}, exp.Time.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	root, err := st.Archive(ctx, exp)
	if err != nil {
		t.Fatal(err)
	}
	if got := permanodeAttrs(t, st, pn).Get("camliContent"); got != root.String() {
		t.Errorf("export's camliContent = %s; want the new archive, %v (old %v)", got, root, old)
	}
}
