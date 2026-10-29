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

package takeout

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// testFile is a file to put in a test zip file.
type testFile struct {
	name    string
	data    string
	deflate bool
}

// makeZip returns a zip file of files.
func makeZip(t *testing.T, files []testFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		method := zip.Store
		if f.deflate {
			method = zip.Deflate
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: method, Modified: time.Unix(1700000000, 0)})
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, f.data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var (
	photoA = strings.Repeat("photo A ", 1000)
	videoB = strings.Repeat("video B ", 3000)
)

// testZips returns the zip files of a test Takeout export, by name.
func testZips(t *testing.T) map[string][]byte {
	return map[string][]byte{
		"takeout-20240101T120000Z-001.zip": makeZip(t, []testFile{
			{name: "Takeout/archive_browser.html", data: "<html>"},
			{name: "Takeout/Google Photos/"},
			{name: "Takeout/Google Photos/Photos from 2019/IMG_0001.JPG", data: photoA},
			{name: "Takeout/Google Photos/Photos from 2019/IMG_0001.JPG.supplemental-metadata.json", data: "{}"},
			{name: "Takeout/Google Photos/Photos from 2019/VID_0002.MP4", data: videoB, deflate: true},
		}),
		"takeout-20240101T120000Z-002.zip": makeZip(t, []testFile{
			// The same photo, in an album.
			{name: "Takeout/Google Photos/Trip/IMG_0001.JPG", data: photoA, deflate: true},
			{name: "Takeout/Google Photos/Trip/metadata.json", data: "{}"},
			{name: "Takeout/Mail/All mail Including Spam and Trash.mbox", data: "From x"},
		}),
	}
}

func writeZips(t *testing.T, zips map[string][]byte) string {
	dir := t.TempDir()
	for name, data := range zips {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Not part of any export.
	os.WriteFile(filepath.Join(dir, "notes.zip"), []byte("not a takeout"), 0o644)
	return dir
}

// photosEntries returns the Google Photos media entries of exp, loading
// its tables of contents.
func photosEntries(t *testing.T, exp *Export) []Entry {
	t.Helper()
	if err := exp.LoadTOCs(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	var ents []Entry
	for _, e := range exp.Entries() {
		if IsPhotosMedia(e.File.Name) {
			ents = append(ents, e)
		}
	}
	return ents
}

func entryNames(ents []Entry) []string {
	var names []string
	for _, e := range ents {
		names = append(names, e.File.Name)
	}
	return names
}

func checkExport(t *testing.T, exps []*Export) {
	t.Helper()
	if len(exps) != 1 {
		t.Fatalf("got %d exports; want 1", len(exps))
	}
	exp := exps[0]
	if exp.Name != "takeout-20240101T120000Z" || !exp.Time.Equal(time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("export %q at %v; want takeout-20240101T120000Z at 2024-01-01 12:00 UTC", exp.Name, exp.Time)
	}
	if len(exp.Zips) != 2 {
		t.Fatalf("export has %d zips; want 2", len(exp.Zips))
	}
	ents := photosEntries(t, exp)
	want := []string{
		"Takeout/Google Photos/Photos from 2019/IMG_0001.JPG",
		"Takeout/Google Photos/Photos from 2019/VID_0002.MP4",
		"Takeout/Google Photos/Trip/IMG_0001.JPG",
	}
	if got := entryNames(ents); !slices.Equal(got, want) {
		t.Fatalf("photos entries = %q; want %q", got, want)
	}
	for _, e := range ents {
		rc, err := e.Open(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("reading %s: %v", e.File.Name, err)
		}
		want := photoA
		if strings.HasSuffix(e.File.Name, ".MP4") {
			want = videoB
		}
		if string(data) != want {
			t.Errorf("%s: read %d bytes, not its contents", e.File.Name, len(data))
		}
	}

	cs := Contents(ents)
	if len(cs) != 2 {
		t.Fatalf("got %d contents; want 2", len(cs))
	}
	if cs[0].Size != int64(len(photoA)) || cs[0].CRC32 != crc32.ChecksumIEEE([]byte(photoA)) || len(cs[0].Entries) != 2 {
		t.Errorf("first content = %d bytes, CRC-32 %08x, %d entries; want photo A, in 2 entries", cs[0].Size, cs[0].CRC32, len(cs[0].Entries))
	}
	if got, want := cs[1].Digest(), fmt.Sprintf("crc32-%08x", crc32.ChecksumIEEE([]byte(videoB))); got != want {
		t.Errorf("video digest = %q; want %q", got, want)
	}
}

func TestLocalExports(t *testing.T) {
	exps, err := LocalExports(writeZips(t, testZips(t)))
	if err != nil {
		t.Fatal(err)
	}
	checkExport(t, exps)
}

// fakeStore is a Store in memory.
type fakeStore struct {
	mu   sync.Mutex
	have map[[2]int64]bool // size, CRC-32
	puts map[string]string // entry name to contents
}

func (s *fakeStore) Has(ctx context.Context, size int64, crc uint32) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.have[[2]int64{size, int64(crc)}], nil
}

func (s *fakeStore) Put(ctx context.Context, e Entry, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts[e.File.Name] = string(data)
	s.have[[2]int64{int64(len(data)), int64(crc32.ChecksumIEEE(data))}] = true
	return nil
}

func TestCheckAndUpload(t *testing.T) {
	ctx := context.Background()
	exps, err := LocalExports(writeZips(t, testZips(t)))
	if err != nil {
		t.Fatal(err)
	}
	cs := Contents(photosEntries(t, exps[0]))
	s := &fakeStore{
		have: map[[2]int64]bool{{int64(len(photoA)), int64(crc32.ChecksumIEEE([]byte(photoA)))}: true},
		puts: make(map[string]string),
	}
	var progress []int
	have, missing, err := Check(ctx, s, cs, 4, func(done int) { progress = append(progress, done) })
	if err != nil {
		t.Fatal(err)
	}
	if len(have) != 1 || len(missing) != 1 || missing[0].Size != int64(len(videoB)) {
		t.Fatalf("Check: have %d, missing %d; want photo A had and video B missing", len(have), len(missing))
	}
	if !slices.Equal(progress, []int{1, 2}) {
		t.Errorf("progress = %v; want [1 2]", progress)
	}

	failed, err := Upload(ctx, s, missing, 4, nil)
	if err != nil || failed != 0 {
		t.Fatalf("Upload: %d failed, error %v", failed, err)
	}
	want := map[string]string{"Takeout/Google Photos/Photos from 2019/VID_0002.MP4": videoB}
	if !maps.Equal(s.puts, want) {
		t.Errorf("uploaded %d files; want just the video", len(s.puts))
	}
	if _, missing, _ := Check(ctx, s, cs, 4, nil); len(missing) != 0 {
		t.Errorf("after upload, %d contents missing; want 0", len(missing))
	}
}

// TestUploadCorrupt checks that an entry whose contents don't match its
// CRC-32 isn't put.
func TestUploadCorrupt(t *testing.T) {
	zips := testZips(t)
	name := "takeout-20240101T120000Z-001.zip"
	data := zips[name]
	i := bytes.Index(data, []byte(photoA))
	if i < 0 {
		t.Fatal("stored photo not found in zip")
	}
	data[i+100] ^= 0xff
	exps, err := LocalExports(writeZips(t, zips))
	if err != nil {
		t.Fatal(err)
	}
	ents := photosEntries(t, exps[0])
	s := &fakeStore{have: make(map[[2]int64]bool), puts: make(map[string]string)}
	var errs []error
	failed, err := Upload(context.Background(), s, Contents(ents[:1]), 1, func(c *Content, err error) { errs = append(errs, err) })
	if err != nil {
		t.Fatal(err)
	}
	if failed != 1 || len(errs) != 1 || !errors.Is(errs[0], zip.ErrChecksum) {
		t.Errorf("Upload of corrupt entry: %d failed, errors %v; want 1 failure, a checksum error", failed, errs)
	}
	if len(s.puts) != 0 {
		t.Errorf("corrupt entry was put")
	}
}

func TestIsPhotosMedia(t *testing.T) {
	for name, want := range map[string]bool{
		"Takeout/Google Photos/Photos from 2019/IMG_0001.JPG":                             true,
		"Takeout/Google Photos/Photos from 2019/IMG_0001.HEIC":                            true,
		"Takeout/Google Photos/Photos from 2019/IMG_0001.HEIC.supplemental-metadata.json": false,
		"Takeout/Google Photos/Trip/metadata.json":                                        false,
		"Takeout/Google Photos/":                                                          false,
		"Takeout/Google Photos/Trip/":                                                     false,
		"Takeout/Google Photos/print-subscriptions.json":                                  false,
		"Takeout/archive_browser.html":                                                    false,
		"Takeout/Drive/IMG_0001.JPG":                                                      false,
	} {
		if got := IsPhotosMedia(name); got != want {
			t.Errorf("IsPhotosMedia(%q) = %v; want %v", name, got, want)
		}
	}
}

func TestIsEdited(t *testing.T) {
	for name, want := range map[string]bool{
		"Takeout/Google Photos/Trip/IMG_20140620_175629-edited.jpg":     true,
		"Takeout/Google Photos/2013-06-26/IMG_20130626-edited(1).jpg":   true,
		"Takeout/Google Photos/2013-06-26/IMG_20130626-edited(2).MP4":   true,
		"Takeout/Google Photos/2013-06-26/IMG_20130626.jpg":             false,
		"Takeout/Google Photos/2013-06-26/IMG_20130626(1).jpg":          false,
		"Takeout/Google Photos/2013-06-26/my-edited-photos.jpg":         false,
		"Takeout/Google Photos/2013-06-26/IMG_20130626-edited(x).jpg":   false,
		"Takeout/Google Photos/2013-06-26/IMG_20130626-edited.jpg.json": false,
	} {
		if got := IsEdited(name); got != want {
			t.Errorf("IsEdited(%q) = %v; want %v", name, got, want)
		}
	}
}

func TestExportName(t *testing.T) {
	for zipName, want := range map[string]string{
		"takeout-20240101T120000Z-001.zip":   "takeout-20240101T120000Z",
		"takeout-20240101T120000Z-3-001.zip": "takeout-20240101T120000Z",
		"takeout-20240101T120000Z-001.tgz":   "",
		"takeout-2024-001.zip":               "",
		"notes.zip":                          "",
	} {
		got, _, ok := exportName(zipName)
		if got != want || ok != (want != "") {
			t.Errorf("exportName(%q) = %q, %v; want %q", zipName, got, ok, want)
		}
	}
}
