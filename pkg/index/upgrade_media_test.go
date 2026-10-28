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
	"image"
	"image/jpeg"
	"maps"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rwcarlsen/goexif/tiff"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

// jpegWithMakeEXIF returns a small JPEG whose EXIF has the given IFD0 Make
// and Exif IFD MakerNote.
func jpegWithMakeEXIF(t *testing.T, cameraMake string, makerNote []byte) []byte {
	be := binary.BigEndian
	entry := func(b []byte, tag uint16, typ tiff.DataType, count, val uint32) []byte {
		b = be.AppendUint16(b, tag)
		b = be.AppendUint16(b, uint16(typ))
		b = be.AppendUint32(b, count)
		return be.AppendUint32(b, val)
	}
	// Header; IFD0 at 8 with two entries (Make and the Exif IFD
	// pointer); the Make value at 38; the Exif IFD at 46 with one entry
	// (the MakerNote); and the MakerNote value at 64.
	makeVal := cameraMake + "\x00"
	if len(makeVal) > 8 {
		t.Fatalf("Make %q too long", cameraMake)
	}
	b := []byte("MM\x00\x2a")
	b = be.AppendUint32(b, 8)
	b = be.AppendUint16(b, 2)
	b = entry(b, 0x010f, tiff.DTAscii, uint32(len(makeVal)), 38)
	b = entry(b, 0x8769, tiff.DTLong, 1, 46)
	b = be.AppendUint32(b, 0) // no IFD1
	b = append(b, makeVal...)
	b = append(b, make([]byte, 8-len(makeVal))...)
	b = be.AppendUint16(b, 1)
	b = entry(b, 0x927c, tiff.DTUndefined, uint32(len(makerNote)), 64)
	b = be.AppendUint32(b, 0)
	b = append(b, makerNote...)

	var img bytes.Buffer
	if err := jpeg.Encode(&img, image.NewGray(image.Rect(0, 0, 16, 16)), nil); err != nil {
		t.Fatal(err)
	}
	app1 := append([]byte("Exif\x00\x00"), b...)
	out := []byte{0xff, 0xd8, 0xff, 0xe1}
	out = be.AppendUint16(out, uint16(2+len(app1)))
	out = append(out, app1...)
	return append(out, img.Bytes()[2:]...) // after the image's SOI
}

// TestAddMediaTags checks that upgrading a version 7 index to version 8
// adds the same rows as indexing the files at version 8.
func TestAddMediaTags(t *testing.T) {
	ctx := context.Background()
	appleMakerNote, err := os.ReadFile("../../internal/media/applemakernote/testdata/iphone16pro.makernote")
	if err != nil {
		t.Fatal(err)
	}
	heic, err := os.ReadFile("../../internal/images/testdata/ski-throne.heic")
	if err != nil {
		t.Fatal(err)
	}
	video := liveVideo(map[string]string{
		"com.apple.quicktime.content.identifier": "7C16BD72-9110-4AB6-80F6-6BF098D5F43E",
		"com.apple.quicktime.make":               "Apple",
		"com.apple.quicktime.location.ISO6709":   "+48.8584+002.2945+035.000/",
	})
	files := []struct {
		name string
		data []byte
	}{
		{"apple.jpg", jpegWithMakeEXIF(t, "Apple", appleMakerNote)},
		{"canon.jpg", jpegWithMakeEXIF(t, "Canon", []byte("Nikon\x00\x02\x10\x00\x00MM\x00\x2a\x00\x00\x00\x08"))},
		{"ski-throne.heic", heic},
		{"IMG_3204.MOV", video},
		{"notes.txt", []byte("not media")},
	}

	// Index the files at version 8.
	sto := new(memory.Storage)
	ix := newLoggingIndex(t)
	mm := &mutationMap{kv: make(map[string]string)}
	videoWhole := blob.RefFromBytes(video)
	for _, f := range files {
		if _, err := sto.ReceiveBlob(ctx, blob.RefFromBytes(f.data), bytes.NewReader(f.data)); err != nil {
			t.Fatal(err)
		}
		bb := schema.NewFileMap(f.name).SetModTime(time.Unix(1700000000, 0).UTC())
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
	}
	want := maps.Clone(mm.kv)
	want[keySchemaVersion.name] = "8"
	// The test needs files with version 8 rows to upgrade: both Apple
	// images (including the HEIC, with its EXIF item) and the video.
	for _, f := range files[:4] {
		if f.name == "canon.jpg" {
			continue
		}
		if _, ok := want[keyMediaTag.Key(blob.RefFromBytes(f.data), MediaTagPhotoIdentifier)]; !ok && f.name != "IMG_3204.MOV" {
			t.Fatalf("version 8 indexing of %s added no photo identifier", f.name)
		}
	}
	if _, ok := want[keyMediaTag.Key(videoWhole, MediaTagContentIdentifier)]; !ok {
		t.Fatal("version 8 indexing of the video added no content identifier")
	}
	if _, ok := want[keyEXIFGPS.Key(videoWhole)]; !ok {
		t.Fatal("version 8 indexing of the video added no location")
	}

	// Make it a version 7 index: without media tags (indexed at version
	// 7 only for audio files) or video locations.
	kv := sorted.NewMemoryKeyValue()
	t.Cleanup(func() { kv.Close() })
	for k, v := range want {
		if strings.HasPrefix(k, keyMediaTag.name+"|") || k == keyEXIFGPS.Key(videoWhole) {
			continue
		}
		if k == keySchemaVersion.name {
			v = "7"
		}
		if err := kv.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}

	old, err := New(kv)
	if !errors.Is(err, errMissingMediaTags) {
		t.Fatalf("New of version 7 index: error %v; want errMissingMediaTags", err)
	}
	old.Logger = test.NewLogger(t, "")
	if err := old.addMediaTags(ctx, sto); err != nil {
		t.Fatalf("addMediaTags: %v", err)
	}
	if _, err := New(kv); err != nil {
		t.Fatalf("New after upgrade: %v", err)
	}

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
