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

	"github.com/rwcarlsen/goexif/tiff"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/schema"
)

func TestIndexEXIFDirectories(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		t.Run(order.String(), func(t *testing.T) {
			// Construct all five directories, including colliding tag numbers
			// and IFD1 fields that goexif's flattened map doesn't expose.
			type tag struct {
				id      uint16
				typ     tiff.DataType
				count   uint32
				data    []byte
				pointer int // target directory; -1 for ordinary values
			}
			short := func(id uint16, value uint16) tag {
				data := make([]byte, 2)
				order.PutUint16(data, value)
				return tag{id, tiff.DTShort, 1, data, -1}
			}
			str := func(id uint16, value string) tag {
				data := append([]byte(value), 0)
				return tag{id, tiff.DTAscii, uint32(len(data)), data, -1}
			}
			ptr := func(id uint16, dir int) tag {
				return tag{id, tiff.DTLong, 1, make([]byte, 4), dir}
			}
			rat := func(id uint16, values ...uint32) tag {
				data := make([]byte, 4*len(values))
				for i, v := range values {
					order.PutUint32(data[4*i:], v)
				}
				return tag{id, tiff.DTRational, uint32(len(values) / 2), data, -1}
			}
			dirs := [][]tag{
				{short(0x0100, 640), short(0x0112, 1), ptr(0x8769, 2), ptr(0x8825, 3)},
				{short(0x0100, 160), short(0x0112, 6)},
				{short(0x0112, 3), str(0x9003, "2026:01:02 03:04:05"), ptr(0xa005, 4),
					ptr(0x8825, 3), str(0xc001, "unsupported Exif field")},
				{str(0x0001, "N"), rat(0x0002, 12, 1, 30, 1, 0, 1),
					str(0x0003, "E"), rat(0x0004, 42, 1, 15, 1, 0, 1),
					str(0x0100, "unsupported GPS field")},
				{str(0x0001, "R98"), str(0x1001, "ignored")},
			}
			offsets := make([]uint32, len(dirs))
			size := uint32(8)
			for i, dir := range dirs {
				offsets[i] = size
				size += 2 + 12*uint32(len(dir)) + 4
			}
			data := make([]byte, size)
			if order == binary.LittleEndian {
				copy(data, "II")
			} else {
				copy(data, "MM")
			}
			order.PutUint16(data[2:], 42)
			order.PutUint32(data[4:], offsets[0])
			for i, dir := range dirs {
				pos := offsets[i]
				order.PutUint16(data[pos:], uint16(len(dir)))
				pos += 2
				for _, tag := range dir {
					order.PutUint16(data[pos:], tag.id)
					order.PutUint16(data[pos+2:], uint16(tag.typ))
					order.PutUint32(data[pos+4:], tag.count)
					if tag.pointer >= 0 {
						order.PutUint32(data[pos+8:], offsets[tag.pointer])
					} else if len(tag.data) <= 4 {
						copy(data[pos+8:pos+12], tag.data)
					} else {
						order.PutUint32(data[pos+8:], uint32(len(data)))
						data = append(data, tag.data...)
					}
					pos += 12
				}
				if i == 0 {
					order.PutUint32(data[pos:], offsets[1])
				}
			}

			whole := blob.RefFromBytes(data)
			want := map[string]string{
				keyEXIFTag.Key(whole, exifIFD0, "0100"):   "int|1|640",
				keyEXIFTag.Key(whole, exifIFD0, "0112"):   "int|1|1",
				keyEXIFTag.Key(whole, exifIFD0, "8769"):   fmt.Sprintf("int|1|%d", offsets[2]),
				keyEXIFTag.Key(whole, exifIFD0, "8825"):   fmt.Sprintf("int|1|%d", offsets[3]),
				keyEXIFTag.Key(whole, exifIFD1, "0100"):   "int|1|160",
				keyEXIFTag.Key(whole, exifIFD1, "0112"):   "int|1|6",
				keyEXIFTag.Key(whole, exifIFD, "0112"):    "int|1|3",
				keyEXIFTag.Key(whole, exifIFD, "9003"):    "string|1|2026:01:02 03:04:05",
				keyEXIFTag.Key(whole, exifIFD, "a005"):    fmt.Sprintf("int|1|%d", offsets[4]),
				keyEXIFTag.Key(whole, exifIFD, "8825"):    fmt.Sprintf("int|1|%d", offsets[3]),
				keyEXIFTag.Key(whole, exifIFDGPS, "0001"): "string|1|N",
				keyEXIFTag.Key(whole, exifIFDGPS, "0002"): "rat|3|12/1|30/1|0/1",
				keyEXIFTag.Key(whole, exifIFDGPS, "0003"): "string|1|E",
				keyEXIFTag.Key(whole, exifIFDGPS, "0004"): "rat|3|42/1|15/1|0/1",
				keyEXIFGPS.Key(whole):                     "12.5000000|42.2500000",
			}
			for range 20 {
				mm := &mutationMap{}
				if err := newLoggingIndex(t).indexEXIF(whole, bytes.NewReader(data), mm); err != nil {
					t.Fatal(err)
				}
				// Exact equality also checks that no legacy keys,
				// Interoperability fields, or unsupported subdirectory
				// fields were written.
				if !reflect.DeepEqual(mm.kv, want) {
					t.Fatalf("mutations = %v; want %v", mm.kv, want)
				}
			}
			t.Run("brokenExifWithValidGPS", func(t *testing.T) {
				broken := bytes.Clone(data)
				// Point the Exif DateTimeOriginal value past EOF, without
				// affecting the independent GPS directory.
				order.PutUint32(broken[offsets[2]+2+12+8:], uint32(len(broken)+8))
				brokenRef := blob.RefFromBytes(broken)
				mm := &mutationMap{}
				if err := newLoggingIndex(t).indexEXIF(brokenRef, bytes.NewReader(broken), mm); err != nil {
					t.Fatalf("noncritical Exif subdirectory error: %v", err)
				}
				for _, tag := range []string{"0001", "0002", "0003", "0004"} {
					got := mm.kv[keyEXIFTag.Key(brokenRef, exifIFDGPS, tag)]
					expected := want[keyEXIFTag.Key(whole, exifIFDGPS, tag)]
					if got != expected {
						t.Errorf("valid GPS tag %s = %q; want %q", tag, got, expected)
					}
				}
			})
			t.Run("brokenGPSWithValidExif", func(t *testing.T) {
				broken := bytes.Clone(data)
				// Break GPSLatitude without affecting the Exif directory.
				order.PutUint32(broken[offsets[3]+2+12+8:], uint32(len(broken)+8))
				whole := blob.RefFromBytes(broken)
				mm := &mutationMap{}
				if err := newLoggingIndex(t).indexEXIF(whole, bytes.NewReader(broken), mm); err != nil {
					t.Fatalf("noncritical GPS subdirectory error: %v", err)
				}
				if got := mm.kv[keyEXIFTag.Key(whole, exifIFD, "9003")]; got != "string|1|2026:01:02 03:04:05" {
					t.Errorf("valid Exif date = %q; want string|1|2026:01:02 03:04:05", got)
				}
				if got := mm.kv[keyEXIFTag.Key(whole, exifIFD0, "0100")]; got != "int|1|640" {
					t.Errorf("valid IFD0 width = %q; want int|1|640", got)
				}
			})
		})
	}
}

func TestIndexEXIFSubdirectoryShortRead(t *testing.T) {
	for _, tt := range []struct {
		pointer uint16
		dirID   int
		tag     uint16
	}{
		{0x8769, exifIFD, 0x9003},
		{0x8825, exifIFDGPS, 0x0001},
	} {
		t.Run(fmt.Sprintf("%04x", tt.pointer), func(t *testing.T) {
			// IFD0 points to a subdirectory whose out-of-line string value
			// falls beyond the retained prefix. goexif reports this as a
			// noncritical error. Preserve the original behavior: retain
			// independent metadata without requesting another file read.
			const payloadOffset = 44
			data := make([]byte, payloadOffset)
			copy(data, "II")
			o := binary.LittleEndian
			o.PutUint16(data[2:], 42)
			o.PutUint32(data[4:], 8)
			o.PutUint16(data[8:], 1)
			o.PutUint16(data[10:], tt.pointer)
			o.PutUint16(data[12:], uint16(tiff.DTLong))
			o.PutUint32(data[14:], 1)
			o.PutUint32(data[18:], 26)
			o.PutUint16(data[26:], 1)
			o.PutUint16(data[28:], tt.tag)
			o.PutUint16(data[30:], uint16(tiff.DTAscii))
			o.PutUint32(data[32:], 8)
			o.PutUint32(data[36:], payloadOffset)
			data = append(data, []byte("fixture\x00")...)
			whole := blob.RefFromBytes(data)
			partial := &mutationMap{}
			if err := newLoggingIndex(t).indexEXIF(whole, bytes.NewReader(data[:payloadOffset]), partial); err != nil {
				t.Fatalf("noncritical truncated subdirectory error: %v", err)
			}
			if got := partial.kv[keyEXIFTag.Key(whole, tt.dirID, fmt.Sprintf("%04x", tt.tag))]; got != "" {
				t.Errorf("truncated subdirectory tag = %q; want no row", got)
			}
			mm := &mutationMap{}
			if err := newLoggingIndex(t).indexEXIF(whole, bytes.NewReader(data), mm); err != nil {
				t.Fatal(err)
			}
			if got := mm.kv[keyEXIFTag.Key(whole, tt.dirID, fmt.Sprintf("%04x", tt.tag))]; got != "string|1|fixture" {
				t.Errorf("full-file tag = %q; want string|1|fixture", got)
			}
		})
	}
}

func BenchmarkIndexEXIF(b *testing.B) {
	for _, tt := range []struct {
		name string
		path string
	}{
		{"GPS", "indextest/testdata/dude-exif.jpg"},
		{"NoGPS", "../../internal/images/testdata/f6-exif.jpg"},
	} {
		b.Run(tt.name, func(b *testing.B) {
			data, err := os.ReadFile(tt.path)
			if err != nil {
				b.Fatal(err)
			}
			whole := blob.RefFromBytes(data)
			ix := newLoggingIndex(b)
			b.ReportAllocs()
			for b.Loop() {
				if err := ix.indexEXIF(whole, bytes.NewReader(data), &mutationMap{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

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
	if err := newLoggingIndex(t).populateFile(context.Background(), &missTrackFetcher{fetcher: sto}, file, mm); err != nil {
		t.Fatal(err)
	}
	whole := blob.RefFromBytes(data)
	want := map[string]string{
		keyFileInfo.Key(file.BlobRef()):              keyFileInfo.Val(len(data), "no-exif.jpg", "image/jpeg", whole),
		keyFileTimes.Key(file.BlobRef()):             keyFileTimes.Val("2023-11-14T22:13:20Z"),
		keyWholeToFileRef.Key(whole, file.BlobRef()): "1",
		keyImageSize.Key(file.BlobRef()):             "1024|768",
	}
	addWantDigestRows(want, data)
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

	// The fixture has no EXIF timezone, and its GPS coordinates do not
	// resolve to a timezone, so FileTime interprets its timestamp in time.Local.
	exifFileTimes := time.Date(2013, time.February, 18, 1, 11, 20, 0, time.Local).
		UTC().Format(time.RFC3339) + ",2023-11-14T22:13:20Z"

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
		{"early", orig, exifFileTimes, "-0.0000010|-120.0000000", false},
		{"sniffBoundary", atSniffBoundary, exifFileTimes, "-0.0000010|-120.0000000", false},
		{"afterEOI", append(bytes.Clone(large), app1...), "2023-11-14T22:13:20Z", "-0.0000010|-120.0000000", true},
		{"falseCandidate", append(bytes.Clone(large), []byte("\xff\xe1\x00\x08notEXF")...), "2023-11-14T22:13:20Z", "", true},
		{"truncatedCandidate", append(bytes.Clone(large), []byte("\xff\xe1\x00")...), "2023-11-14T22:13:20Z", "", true},
		{"rawExifNamedJPEG", app1[4:], exifFileTimes, "-0.0000010|-120.0000000", false},
		{"tiffNamedJPEG", app1[10:], exifFileTimes, "-0.0000010|-120.0000000", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sto, file := exifTestFile(t, tt.data, "image.jpg")
			mm := &mutationMap{kv: make(map[string]string)}
			if err := newLoggingIndex(t).populateFile(context.Background(), &missTrackFetcher{fetcher: sto}, file, mm); err != nil {
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
	if err := newLoggingIndex(t).populateFile(context.Background(), &missTrackFetcher{fetcher: sto}, file, mm); err != nil {
		t.Fatal(err)
	}
	if got := mm.kv[keyImageSize.Key(file.BlobRef())]; got != "40|80" {
		t.Errorf("visible dimensions = %q; want 40|80 (stored 80x40, orientation 6)", got)
	}
	if got := mm.kv[keyEXIFTag.Key(blob.RefFromBytes(data), exifIFD0, "0112")]; got != "int|1|6" {
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
	err := newLoggingIndex(t).populateFile(context.Background(), &missTrackFetcher{fetcher: sto}, bb.Blob(), mm)
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
