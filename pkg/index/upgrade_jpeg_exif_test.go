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

package index

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"maps"
	"os"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

// upgradeJPEGEXIF upgrades the version 12 index kv to version 13 with
// addJPEGEXIF, reading files from fetcher.
func upgradeJPEGEXIF(t *testing.T, kv sorted.KeyValue, fetcher blob.Fetcher) {
	t.Helper()
	ix, err := New(kv)
	if !errors.Is(err, errMissingJPEGEXIF) {
		t.Fatalf("New of version 12 index: error %v; want errMissingJPEGEXIF", err)
	}
	ix.Logger = test.NewLogger(t, "")
	if err := ix.addJPEGEXIF(context.Background(), fetcher); err != nil {
		t.Fatalf("addJPEGEXIF: %v", err)
	}
	if _, err := New(kv); err != nil {
		t.Fatalf("New after upgrade: %v", err)
	}
}

// xmpSegment is a JPEG APP1 segment with XMP, rather than EXIF.
var xmpSegment = []byte("\xff\xe1\x00\x2bhttp://ns.adobe.com/xap/1.0/\x00<x:xmpmeta/>")

// exifJPEG returns a JPEG with EXIF, and the offset and end of its EXIF
// APP1 segment, its first.
func exifJPEG(t *testing.T) (data []byte, app1, end int) {
	t.Helper()
	data, err := os.ReadFile("indextest/testdata/dude-exif.jpg")
	if err != nil {
		t.Fatal(err)
	}
	app1 = bytes.Index(data, []byte("\xff\xe1"))
	if app1 < 0 || !bytes.HasPrefix(data[app1+4:], []byte("Exif\x00\x00")) {
		t.Fatal("fixture's first APP1 segment isn't EXIF")
	}
	return data, app1, app1 + 2 + int(binary.BigEndian.Uint16(data[app1+2:]))
}

// insertAt returns data with ins inserted at off.
func insertAt(data []byte, off int, ins ...[]byte) []byte {
	out := bytes.Clone(data[:off])
	for _, b := range ins {
		out = append(out, b...)
	}
	return append(out, data[off:]...)
}

func TestJPEGEXIFMoved(t *testing.T) {
	data, app1, end := exifJPEG(t)
	padding := func(n int) []byte {
		return append([]byte{0xff, 0xe2, byte((n + 2) >> 8), byte(n + 2)}, make([]byte, n)...)
	}
	pastPrefix := bytes.Clone(data[:app1])
	for range imagePrefixSize/(60<<10) + 1 {
		pastPrefix = append(pastPrefix, padding(60<<10)...)
	}
	pastPrefix = append(pastPrefix, data[app1:]...)
	for _, tt := range []struct {
		name string
		data []byte
		want bool
	}{
		{"first", data, false},
		{"afterXMP", insertAt(data, app1, xmpSegment), true},
		{"afterFalseMarker", insertAt(data, app1, []byte("\xff\xe2\x00\x0a\xff\xe1\x00\x08notE")), true},
		{"afterOtherSegment", insertAt(data, app1, padding(100)), false},
		// goexif reads FF FF as a pair, and then finds the FF E1.
		{"twoFillBytes", insertAt(data, app1, []byte{0xff, 0xff}), false},
		// goexif reads FF FF twice, missing the FF E1.
		{"threeFillBytes", insertAt(data, app1, []byte{0xff, 0xff, 0xff}), true},
		{"pastPrefix", pastPrefix, true},
		{"noEXIF", largeJPEGWithoutEXIF(t), false},
		{"truncated", data[:app1+20], true},
		{"notJPEG", data[app1+4 : end], false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := jpegEXIFMoved(bytes.NewReader(tt.data))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("jpegEXIFMoved = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestGoexifAPP1(t *testing.T) {
	for _, tt := range []struct {
		b    string
		want int64
	}{
		{"\xff\xd8\xff\xe1\x00\x10", 2},
		{"\xff\xd8\xff\xe0\x00\x10\xff\xe1\x00\x10", 6},
		{"\xff\xd8\xff\xff\xe1\x00\x10", -1},
		{"\xff\xd8\xff\xff\xff\xe1\x00\x10", 4},
		{"\xff\xd8\xff\xe1\x00\x02\xff\xe1\x00\x10", 6}, // skips an empty segment
		{"\xff\xd8\xff\xe1\x00", -1},
		{"\xff\xd8", -1},
	} {
		if got := goexifAPP1([]byte(tt.b)); got != tt.want {
			t.Errorf("goexifAPP1(%q) = %d; want %d", tt.b, got, tt.want)
		}
	}
}

// TestAddJPEGEXIF checks that upgrading a version 12 index to version 13
// gives JPEGs whose EXIF version 12 missed the rows that indexing them at
// version 13 does, and leaves other JPEGs alone.
func TestAddJPEGEXIF(t *testing.T) {
	ctx := context.Background()
	modTime := time.Date(2026, 10, 20, 0, 10, 43, 0, time.UTC)
	data, app1, _ := exifJPEG(t)
	afterXMP := insertAt(data, app1, xmpSegment)
	files := []struct {
		name  string
		data  []byte
		moved bool // whether version 12 indexed it differently
	}{
		{"photoshop.jpg", afterXMP, true},
		{"photoshop-copy.jpg", afterXMP, true},
		{"plain.jpg", data, false},
		{"no-exif.jpg", largeJPEGWithoutEXIF(t), false},
	}
	sto := new(memory.Storage)
	ix := newLoggingIndex(t)
	mm := &mutationMap{kv: make(map[string]string)}
	old := make(map[string]string) // rows as version 12 indexed them, or "" for none
	for i, f := range files {
		if _, err := sto.ReceiveBlob(ctx, blob.RefFromBytes(f.data), bytes.NewReader(f.data)); err != nil {
			t.Fatal(err)
		}
		bb := schema.NewFileMap(f.name).SetModTime(modTime.Add(time.Duration(i) * time.Hour))
		if err := bb.PopulateParts(int64(len(f.data)), []schema.BytesPart{{BlobRef: blob.RefFromBytes(f.data), Size: uint64(len(f.data))}}); err != nil {
			t.Fatal(err)
		}
		file := bb.Blob()
		if _, err := sto.ReceiveBlob(ctx, file.BlobRef(), strings.NewReader(file.JSON())); err != nil {
			t.Fatal(err)
		}
		if err := ix.populateFile(ctx, &missTrackFetcher{fetcher: sto}, file, mm); err != nil {
			t.Fatalf("populateFile(%s): %v", f.name, err)
		}
		timesKey := keyFileTimes.Key(file.BlobRef())
		if f.moved {
			old[timesKey] = keyFileTimes.Val(fileTimesValue([]time.Time{file.ModTime()}))
		} else {
			// Rows of files that addJPEGEXIF doesn't reindex
			// don't change, even if they're stale.
			old[timesKey] = keyFileTimes.Val("2000-01-01T00:00:00Z")
			mm.kv[timesKey] = old[timesKey]
		}
	}
	whole := blob.RefFromBytes(afterXMP)
	gpsKey := keyEXIFGPS.Key(whole)
	if mm.kv[gpsKey] != "-0.0000010|-120.0000000" {
		t.Fatalf("version 13 indexing of photoshop.jpg: GPS %q; want its EXIF GPS", mm.kv[gpsKey])
	}
	want := maps.Clone(mm.kv)
	want[keySchemaVersion.name] = "13"
	for k := range want {
		if strings.HasPrefix(k, keyEXIFTag.Prefix(whole)) || k == gpsKey {
			old[k] = "" // version 12 found no EXIF
		}
	}
	// Rows that a mistaken EXIF segment may have had.
	stale := keyEXIFTag.Key(whole, exifIFD0, "beef")
	old[stale] = "int|1|1"

	kv := sorted.NewMemoryKeyValue()
	t.Cleanup(func() { kv.Close() })
	for k, v := range want {
		if ov, ok := old[k]; ok {
			v = ov
		}
		if k == keySchemaVersion.name {
			v = "12"
		}
		if v == "" {
			continue
		}
		if err := kv.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := kv.Set(stale, old[stale]); err != nil {
		t.Fatal(err)
	}
	upgradeJPEGEXIF(t, kv, sto)

	got := make(map[string]string)
	it := kv.Find("", "")
	for it.Next() {
		got[it.Key()] = it.Value()
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("after upgrade, row %q = %q; want %q", k, got[k], v)
		}
	}
	for k, v := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("after upgrade, unexpected row %q = %q", k, v)
		}
	}
}

// TestPopulateFileJPEGEXIFAfterXMP checks that indexing a JPEG whose EXIF
// segment follows one with XMP, as Photoshop writes, finds its EXIF.
func TestPopulateFileJPEGEXIFAfterXMP(t *testing.T) {
	orig, app1, _ := exifJPEG(t)
	if n := binary.BigEndian.Uint16(xmpSegment[2:]); int(n)+2 != len(xmpSegment) {
		t.Fatalf("XMP segment length %d; want %d", n, len(xmpSegment)-2)
	}
	// exifRows returns the EXIF rows of indexing data, by their keys after
	// their contents' wholeRef, and its file times.
	exifRows := func(data []byte) (rows map[string]string, times string) {
		sto, file := exifTestFile(t, data, "photo.jpg")
		mm := &mutationMap{kv: make(map[string]string)}
		if err := newLoggingIndex(t).populateFile(context.Background(), &missTrackFetcher{fetcher: sto}, file, mm); err != nil {
			t.Fatal(err)
		}
		whole := blob.RefFromBytes(data)
		rows = make(map[string]string)
		for k, v := range mm.kv {
			if rest, ok := strings.CutPrefix(k, keyEXIFTag.Prefix(whole)); ok {
				rows[rest] = v
			}
			if k == keyEXIFGPS.Key(whole) {
				rows["gps"] = v
			}
		}
		return rows, mm.kv[keyFileTimes.Key(file.BlobRef())]
	}
	want, wantTimes := exifRows(orig)
	if want["gps"] == "" || len(want) < 2 || !strings.Contains(urld(wantTimes), ",") {
		t.Fatalf("fixture's EXIF rows = %v, times %q; want tags, GPS and an EXIF time", want, wantTimes)
	}
	got, gotTimes := exifRows(insertAt(orig, app1, xmpSegment, []byte("\xff\xed\x00\x10Photoshop 3.0\x00")))
	if !maps.Equal(got, want) {
		t.Errorf("EXIF rows after XMP = %v; want %v", got, want)
	}
	if gotTimes != wantTimes {
		t.Errorf("filetimes = %q; want %q", gotTimes, wantTimes)
	}
}
