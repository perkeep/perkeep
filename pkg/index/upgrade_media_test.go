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
	"fmt"
	"image"
	"image/jpeg"
	"maps"
	"net/url"
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

// TestAddMediaTags checks that upgrading a version 7, 8, or 9 index to
// version 10, and then by addDigests to version 11, adds the same rows as
// indexing the files at version 11.
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
	// A HEIC file with the major brand "heix", as of photos that iOS saves
	// after editing, which version 9 indexes didn't recognize.
	heix := bytes.Clone(heic)
	copy(heix[8:12], "heix")
	live := liveVideo(map[string]string{
		"com.apple.quicktime.content.identifier": "7C16BD72-9110-4AB6-80F6-6BF098D5F43E",
		"com.apple.quicktime.make":               "Apple",
		"com.apple.quicktime.location.ISO6709":   "+47.6305-122.3478+084.505/",
	})
	mov := trackedVideo("qt  ", 1920, 1080, 90)
	threeGP := trackedVideo("3gp5", 352, 288, 0)
	aviFile := aviVideo(640, 480)
	files := []struct {
		name string
		data []byte
	}{
		{"apple.jpg", jpegWithMakeEXIF(t, "Apple", appleMakerNote)},
		{"canon.jpg", jpegWithMakeEXIF(t, "Canon", []byte("Nikon\x00\x02\x10\x00\x00MM\x00\x2a\x00\x00\x00\x08"))},
		{"ski-throne.heic", heic},
		{"IMG_0002.heic", heix},
		{"IMG_3204.MOV", live},
		{"IMG_0001.MOV", mov},
		{"video.3gp", threeGP},
		{"MVI_0001.AVI", aviFile},
		{"notes.txt", []byte("not media")},
	}

	// Index the files at version 11.
	sto := new(memory.Storage)
	ix := newLoggingIndex(t)
	mm := &mutationMap{kv: make(map[string]string)}
	var heixFile blob.Ref
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
		if f.name == "IMG_0002.heic" {
			heixFile = file.BlobRef()
		}
	}
	want := maps.Clone(mm.kv)
	want[keySchemaVersion.name] = "11"

	// The test needs files with new rows to upgrade: both Apple images
	// (including the HEIC, with its EXIF item), the videos, and the heix
	// HEIC file.
	tagRow := func(data []byte, tag string) string { return keyMediaTag.Key(blob.RefFromBytes(data), tag) }
	for _, row := range []string{
		tagRow(files[0].data, MediaTagPhotoIdentifier),
		tagRow(heic, MediaTagPhotoIdentifier),
		tagRow(live, MediaTagContentIdentifier),
		keyEXIFGPS.Key(blob.RefFromBytes(live)),
		tagRow(mov, MediaTagWidth),
		tagRow(mov, MediaTagRotation),
		tagRow(threeGP, MediaTagHeight),
		tagRow(aviFile, MediaTagWidth),
		tagRow(aviFile, MediaTagDurationMS),
		tagRow(heix, MediaTagPhotoIdentifier),
		keyImageSize.Key(heixFile),
		keyEXIFGPS.Key(blob.RefFromBytes(heix)),
	} {
		if _, ok := want[row]; !ok {
			t.Fatalf("version 10 indexing added no row %q", row)
		}
	}
	heixInfo := keyFileInfo.Key(heixFile)
	if !strings.Contains(want[heixInfo], "|image%2Fheic|") {
		t.Fatalf("heix file's %s row = %q; want image/heic", heixInfo, want[heixInfo])
	}
	if got, want := want[tagRow(mov, MediaTagWidth)]+"x"+want[tagRow(mov, MediaTagHeight)], "1080x1920"; got != want {
		t.Errorf("rotated video size = %s; want %s", got, want)
	}

	// Rows added in version 8: media tags, indexed at version 7 only for
	// audio files, and video locations.
	isV8Row := func(k string) bool {
		return strings.HasPrefix(k, keyMediaTag.name+"|") || k == keyEXIFGPS.Key(blob.RefFromBytes(live))
	}
	// Rows added in version 9: video sizes, and the media tags of 3GPP
	// and AVI videos.
	isV9Row := func(k string) bool {
		for _, tag := range []string{MediaTagWidth, MediaTagHeight, MediaTagRotation} {
			if strings.HasPrefix(k, keyMediaTag.name+"|") && strings.HasSuffix(k, "|"+tag) {
				return true
			}
		}
		for _, data := range [][]byte{threeGP, aviFile} {
			if strings.HasPrefix(k, keyMediaTag.name+"|"+blob.RefFromBytes(data).String()+"|") {
				return true
			}
		}
		return false
	}

	// Rows of the heix file that version 10 indexes and earlier ones don't:
	// its image size, and its contents' rows (such as EXIF), except the
	// one mapping them to the file. Earlier versions indexed it as
	// image/heif, by its name, and with only its modification time.
	heixWhole := blob.RefFromBytes(heix).String()
	isV10Row := func(k string) bool {
		return k == keyImageSize.Key(heixFile) ||
			strings.Contains(k, "|"+heixWhole) && !strings.HasPrefix(k, keyWholeToFileRef.name+"|")
	}
	// Rows added in version 11: the file digests.
	isV11Row := func(k string) bool {
		return strings.HasPrefix(k, keyFileDigests.name+"|") || strings.HasPrefix(k, keyDigestToWhole.name+"|")
	}
	heixTimes := keyFileTimes.Key(heixFile)
	v9Value := func(k, v string) string {
		switch k {
		case heixInfo:
			return strings.Replace(v, "|image%2Fheic|", "|image%2Fheif|", 1)
		case heixTimes:
			// Its modification time, without its EXIF ones.
			modTime, _, _ := strings.Cut(v, "%2C")
			if want := url.QueryEscape(time.Unix(1700000000, 0).UTC().Format(time.RFC3339)); modTime != want {
				t.Fatalf("heix file's %s row = %q; want it to start with %q", k, v, want)
			}
			return modTime
		}
		return v
	}

	for _, from := range []int{7, 8, 9} {
		t.Run(fmt.Sprintf("from%d", from), func(t *testing.T) {
			kv := sorted.NewMemoryKeyValue()
			t.Cleanup(func() { kv.Close() })
			for k, v := range want {
				if isV11Row(k) || isV10Row(k) || from < 9 && isV9Row(k) || from == 7 && isV8Row(k) {
					continue
				}
				v = v9Value(k, v)
				if k == keySchemaVersion.name {
					v = fmt.Sprint(from)
				}
				if err := kv.Set(k, v); err != nil {
					t.Fatal(err)
				}
			}

			old, err := New(kv)
			if !errors.Is(err, errMissingMediaTags) {
				t.Fatalf("New of version %d index: error %v; want errMissingMediaTags", from, err)
			}
			old.Logger = test.NewLogger(t, "")
			if err := old.addMediaTags(ctx, sto); err != nil {
				t.Fatalf("addMediaTags: %v", err)
			}
			old, err = New(kv)
			if !errors.Is(err, errMissingDigests) {
				t.Fatalf("New after addMediaTags: error %v; want errMissingDigests", err)
			}
			old.Logger = test.NewLogger(t, "")
			if err := old.addDigests(ctx, sto); err != nil {
				t.Fatalf("addDigests: %v", err)
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
		})
	}
}
