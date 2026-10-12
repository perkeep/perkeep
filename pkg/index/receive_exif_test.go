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

package index

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"math/rand/v2"
	"os"
	"reflect"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/schema"
)

func TestJPEGAPP1Finder(t *testing.T) {
	for _, data := range []string{
		"", "jpeg", "\xff", "\xff\xe1", "\xff\x00\xe1",
		"\xff\xff\xe1", "\xff\xd8\xff\xda...scan...\xff\xe1",
		"\xff\xd8\xff\xd9...trailer...\xff\xe1",
	} {
		for split := 0; split <= len(data); split++ {
			t.Run(fmt.Sprintf("%x/%d", data, split), func(t *testing.T) {
				var w jpegAPP1Finder
				for _, p := range []string{data[:split], "", data[split:], ""} {
					if n, err := w.Write([]byte(p)); n != len(p) || err != nil {
						t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(p))
					}
				}
				if want := bytes.Contains([]byte(data), []byte("\xff\xe1")); w.found != want {
					t.Errorf("found = %v; want %v", w.found, want)
				}
			})
		}
	}
}

// largeJPEGWithoutEXIF exceeds populateFile's retained image prefix, so a
// fruitless EXIF search would otherwise retry against the whole file.
func largeJPEGWithoutEXIF(tb testing.TB) []byte {
	tb.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, 1024, 768))
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < len(im.Pix); i += 4 {
		v := r.Uint32()
		im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = byte(v), byte(v>>8), byte(v>>16), 255
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, im, &jpeg.Options{Quality: 85}); err != nil {
		tb.Fatal(err)
	}
	data := buf.Bytes()
	if len(data) <= 512<<10 || bytes.Contains(data, []byte("\xff\xe1")) {
		tb.Fatal("expected a JPEG larger than 512 KiB without an APP1 marker")
	}
	return data
}

func exifTestFile(tb testing.TB, data []byte, filename string) (*memory.Storage, *schema.Blob) {
	tb.Helper()
	sto := new(memory.Storage)
	ref := blob.RefFromBytes(data)
	if _, err := sto.ReceiveBlob(context.Background(), ref, bytes.NewReader(data)); err != nil {
		tb.Fatal(err)
	}
	bb := schema.NewFileMap(filename).SetModTime(time.Unix(1700000000, 0).UTC())
	if err := bb.PopulateParts(int64(len(data)), []schema.BytesPart{{BlobRef: ref, Size: uint64(len(data))}}); err != nil {
		tb.Fatal(err)
	}
	return sto, bb.Blob()
}

func TestPopulateFileJPEGWithoutEXIF(t *testing.T) {
	data := largeJPEGWithoutEXIF(t)
	sto, file := exifTestFile(t, data, "no-exif.jpg")
	mm := &mutationMap{kv: make(map[string]string)}
	if err := new(Index).populateFile(context.Background(), &missTrackFetcher{fetcher: sto}, file, mm); err != nil {
		t.Fatal(err)
	}
	whole := blob.RefFromBytes(data)
	want := map[string]string{
		keyFileInfo.Key(file.BlobRef()):              keyFileInfo.Val(len(data), "no-exif.jpg", "image/jpeg", whole),
		keyFileTimes.Key(file.BlobRef()):             keyFileTimes.Val("2023-11-14T22:13:20Z"),
		keyWholeToFileRef.Key(whole, file.BlobRef()): "1",
		keyImageSize.Key(file.BlobRef()):             "1024|768",
	}
	if !reflect.DeepEqual(mm.kv, want) {
		t.Errorf("mutations = %v; want %v", mm.kv, want)
	}
	if n, size := sto.Stats(); n != 1 || size != int64(len(data)) {
		t.Errorf("fetched %d blobs / %d bytes; want one content read (%d bytes), no EXIF retry", n, size, len(data))
	}
}

func TestPopulateFileJPEGEXIFFallback(t *testing.T) {
	orig, err := os.ReadFile("indextest/testdata/dude-exif.jpg")
	if err != nil {
		t.Fatal(err)
	}
	pos := bytes.Index(orig, []byte("\xff\xe1"))
	if pos < 0 {
		t.Fatal("fixture has no APP1 marker")
	}
	end := pos + 2 + int(binary.BigEndian.Uint16(orig[pos+2:pos+4]))
	app1 := orig[pos:end]
	large := largeJPEGWithoutEXIF(t)
	// Put FF at offset 1023 and E1 at 1024, across the MIME sniff/replay
	// boundary. The intervening APP2 section is valid JPEG padding.
	const padSize = 1017
	atSniffBoundary := []byte{0xff, 0xd8, 0xff, 0xe2, (padSize + 2) >> 8, (padSize + 2) & 0xff}
	atSniffBoundary = append(atSniffBoundary, make([]byte, padSize)...)
	atSniffBoundary = append(atSniffBoundary, app1...)
	atSniffBoundary = append(atSniffBoundary, large[2:]...)

	// APP1 candidates must retain the old decoder behavior, even when they
	// follow the retained prefix, SOS, or EOI. The decoder searches raw bytes,
	// not just JPEG headers. A .jpg extension alone is not proof of JPEG data:
	// raw EXIF has no APP1 marker but is still decoded by FileTime/indexEXIF.
	for _, tt := range []struct {
		name      string
		data      []byte
		fileTimes string
		gps       string
		refetch   bool
	}{
		{"early", orig, "2013-02-18T01:11:20Z,2023-11-14T22:13:20Z", "-0.0000010|-120.0000000", false},
		{"sniffBoundary", atSniffBoundary, "2013-02-18T01:11:20Z,2023-11-14T22:13:20Z", "-0.0000010|-120.0000000", false},
		{"afterEOI", append(bytes.Clone(large), app1...), "2023-11-14T22:13:20Z", "-0.0000010|-120.0000000", true},
		{"falseCandidate", append(bytes.Clone(large), []byte("\xff\xe1\x00\x08notEXF")...), "2023-11-14T22:13:20Z", "", true},
		{"truncatedCandidate", append(bytes.Clone(large), []byte("\xff\xe1\x00")...), "2023-11-14T22:13:20Z", "", true},
		{"rawExifNamedJPEG", app1[4:], "2013-02-18T01:11:20Z,2023-11-14T22:13:20Z", "-0.0000010|-120.0000000", false},
		{"tiffNamedJPEG", app1[10:], "2013-02-18T01:11:20Z,2023-11-14T22:13:20Z", "-0.0000010|-120.0000000", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sto, file := exifTestFile(t, tt.data, "image.jpg")
			mm := &mutationMap{kv: make(map[string]string)}
			if err := new(Index).populateFile(context.Background(), &missTrackFetcher{fetcher: sto}, file, mm); err != nil {
				t.Fatal(err)
			}
			whole := blob.RefFromBytes(tt.data)
			if got, want := mm.kv[keyEXIFGPS.Key(whole)], tt.gps; got != want {
				t.Errorf("EXIF GPS = %q; want %q", got, want)
			}
			if got, want := mm.kv[keyFileTimes.Key(file.BlobRef())], keyFileTimes.Val(tt.fileTimes); got != want {
				t.Errorf("filetimes = %q; want %q", got, want)
			}
			if n, _ := sto.Stats(); tt.refetch && n < 2 {
				t.Errorf("fetched %d times; want full-file EXIF fallback", n)
			}
		})
	}
}

func TestPopulateFileJPEGEXIFPastPrefix(t *testing.T) {
	orig, err := os.ReadFile("../../internal/images/testdata/f6-exif.jpg")
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Clone(orig[:2])
	// Keep every APP2 section within its 16-bit length limit while placing
	// the original APP1 and image headers past the 512 KiB retained prefix.
	const padSize = 60 << 10
	for range 9 {
		data = append(data, 0xff, 0xe2, (padSize+2)>>8, (padSize+2)&0xff)
		data = append(data, make([]byte, padSize)...)
	}
	data = append(data, orig[2:]...)
	sto, file := exifTestFile(t, data, "oriented.jpg")
	mm := &mutationMap{kv: make(map[string]string)}
	if err := new(Index).populateFile(context.Background(), &missTrackFetcher{fetcher: sto}, file, mm); err != nil {
		t.Fatal(err)
	}
	if got := mm.kv[keyImageSize.Key(file.BlobRef())]; got != "40|80" {
		t.Errorf("visible dimensions = %q; want 40|80 (stored 80x40, orientation 6)", got)
	}
	if got := mm.kv[keyEXIFTag.Key(blob.RefFromBytes(data), "0112")]; got != "int|1|6" {
		t.Errorf("EXIF orientation = %q; want int|1|6", got)
	}
}

func TestPopulateFileJPEGIncomplete(t *testing.T) {
	data := largeJPEGWithoutEXIF(t)
	// MIME sniffing succeeds and the APP1 finder sees the initial bytes, but
	// the rest of the file is missing. That is not proof of EXIF absence.
	sto, _ := exifTestFile(t, data[:1024], "incomplete.jpg")
	bb := schema.NewFileMap("incomplete.jpg")
	if err := bb.PopulateParts(int64(len(data)), []schema.BytesPart{
		{BlobRef: blob.RefFromBytes(data[:1024]), Size: 1024},
		{BlobRef: blob.RefFromBytes(data[1024:]), Size: uint64(len(data) - 1024)},
	}); err != nil {
		t.Fatal(err)
	}
	mm := &mutationMap{kv: make(map[string]string)}
	err := new(Index).populateFile(context.Background(), &missTrackFetcher{fetcher: sto}, bb.Blob(), mm)
	if !errors.Is(err, errMissingDep) {
		t.Fatalf("populateFile = %v; want missing dependency error", err)
	}
	if len(mm.kv) != 0 {
		t.Errorf("indexed incomplete file: %v", mm.kv)
	}
}

func BenchmarkPopulateFileJPEGWithoutEXIF(b *testing.B) {
	data := largeJPEGWithoutEXIF(b)
	sto, file := exifTestFile(b, data, "no-exif.jpg")
	ix := new(Index)
	fetcher := &missTrackFetcher{fetcher: sto}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		mm := &mutationMap{kv: make(map[string]string)}
		if err := ix.populateFile(context.Background(), fetcher, file, mm); err != nil {
			b.Fatal(err)
		}
	}
}
