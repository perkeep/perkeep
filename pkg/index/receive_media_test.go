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
	"os"
	"reflect"
	"testing"

	"github.com/rwcarlsen/goexif/tiff"
	"perkeep.org/pkg/blob"
)

// appleEXIF returns big-endian TIFF EXIF data whose Exif directory has
// the given MakerNote.
func appleEXIF(makerNote []byte) []byte {
	be := binary.BigEndian
	// Header, IFD0 at 8 with one entry (the Exif IFD pointer), Exif IFD
	// at 26 with one entry (the MakerNote), MakerNote value at 44.
	b := []byte("MM\x00\x2a")
	b = be.AppendUint32(b, 8)
	b = be.AppendUint16(b, 1)
	b = be.AppendUint16(b, 0x8769)
	b = be.AppendUint16(b, uint16(tiff.DTLong))
	b = be.AppendUint32(b, 1)
	b = be.AppendUint32(b, 26)
	b = be.AppendUint32(b, 0) // no IFD1
	b = be.AppendUint16(b, 1)
	b = be.AppendUint16(b, 0x927c)
	b = be.AppendUint16(b, uint16(tiff.DTUndefined))
	b = be.AppendUint32(b, uint32(len(makerNote)))
	b = be.AppendUint32(b, 44)
	b = be.AppendUint32(b, 0)
	return append(b, makerNote...)
}

func TestIndexAppleMakerNote(t *testing.T) {
	mn, err := os.ReadFile("../../internal/media/applemakernote/testdata/iphone16pro.makernote")
	if err != nil {
		t.Fatal(err)
	}
	data := appleEXIF(mn)
	whole := blob.RefFromBytes(data)
	mm := &mutationMap{}
	if err := newLoggingIndex(t).indexEXIF(whole, bytes.NewReader(data), mm); err != nil {
		t.Fatal(err)
	}
	for tag, want := range map[string]string{
		MediaTagContentIdentifier: keyMediaTag.Val("7C16BD72-9110-4AB6-80F6-6BF098D5F43E"),
		MediaTagPhotoIdentifier:   keyMediaTag.Val("ADBDBDE4-A3F3-4F20-9503-E2307A84E3C2"),
		MediaTagBurstUUID:         "", // not a burst
	} {
		if got := mm.kv[keyMediaTag.Key(whole, tag)]; got != want {
			t.Errorf("media tag %s row = %q; want %q", tag, got, want)
		}
	}

	// A non-Apple MakerNote is ignored.
	data = appleEXIF([]byte("Nikon\x00\x02\x10\x00\x00MM\x00\x2a\x00\x00\x00\x08"))
	whole = blob.RefFromBytes(data)
	mm = &mutationMap{}
	if err := newLoggingIndex(t).indexEXIF(whole, bytes.NewReader(data), mm); err != nil {
		t.Fatal(err)
	}
	for k := range mm.kv {
		if typeOfKey(k) == keyMediaTag.name {
			t.Errorf("unexpected media tag row %q", k)
		}
	}
}

func qtBox(typ string, body ...[]byte) []byte {
	b := bytes.Join(body, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(b)))
	out = append(out, typ...)
	return append(out, b...)
}

// liveVideo returns a QuickTime movie with Apple Live Photo metadata,
// with string values.
func liveVideo(items map[string]string) []byte {
	be := binary.BigEndian
	var keys, ilst [][]byte
	i := uint32(0)
	for k, v := range items {
		i++
		keys = append(keys, be.AppendUint32(nil, uint32(8+len(k))), []byte("mdta"+k))
		ilst = append(ilst, qtBox(string(be.AppendUint32(nil, i)), qtBox("data", be.AppendUint32(nil, 1), make([]byte, 4), []byte(v))))
	}
	return bytes.Join([][]byte{
		qtBox("ftyp", []byte("qt  "), make([]byte, 4), []byte("qt  ")),
		qtBox("mdat", make([]byte, 1000)),
		qtBox("moov",
			qtBox("mvhd", make([]byte, 12), be.AppendUint32(nil, 600), be.AppendUint32(nil, 444), make([]byte, 80)),
			qtBox("meta",
				qtBox("hdlr", make([]byte, 8), []byte("mdta"), make([]byte, 12)),
				qtBox("keys", make([]byte, 4), be.AppendUint32(nil, i), bytes.Join(keys, nil)),
				qtBox("ilst", ilst...))),
	}, nil)
}

// trackedVideo returns an ISO BMFF movie of the given major brand (e.g.
// "qt  " or "3gp5") with an HEVC video track of the given stored size,
// rotated the given degrees clockwise for display.
func trackedVideo(brand string, width, height, rotation int) []byte {
	be := binary.BigEndian
	// The matrix's scale and rotation terms a, b, c, and d, in 16.16
	// fixed point, for each clockwise rotation.
	one := int32(1 << 16)
	m := map[int][4]int32{
		0:   {one, 0, 0, one},
		90:  {0, one, -one, 0},
		180: {-one, 0, 0, -one},
		270: {0, -one, one, 0},
	}[rotation]
	var matrix []byte
	for _, v := range []int32{m[0], m[1], 0, m[2], m[3], 0, 0, 0, 1 << 30} {
		matrix = be.AppendUint32(matrix, uint32(v))
	}
	// Enabled; times, track ID, reserved, duration; reserved; layer,
	// alternate group, volume, reserved; matrix; 16.16 size.
	tkhd := qtBox("tkhd", []byte{0, 0, 0, 3}, make([]byte, 20), make([]byte, 8), make([]byte, 8), matrix,
		be.AppendUint32(nil, uint32(width)<<16), be.AppendUint32(nil, uint32(height)<<16))
	// A VisualSampleEntry, with the pixel size at offset 32.
	entry := qtBox("hvc1", make([]byte, 24), be.AppendUint16(nil, uint16(width)), be.AppendUint16(nil, uint16(height)), make([]byte, 50))
	return bytes.Join([][]byte{
		qtBox("ftyp", []byte(brand), make([]byte, 4), []byte(brand)),
		qtBox("mdat", make([]byte, 100)),
		qtBox("moov",
			qtBox("mvhd", make([]byte, 12), be.AppendUint32(nil, 600), be.AppendUint32(nil, 1200), make([]byte, 80)),
			qtBox("trak", tkhd,
				qtBox("mdia",
					qtBox("hdlr", make([]byte, 8), []byte("vide"), make([]byte, 13)),
					qtBox("minf",
						// QuickTime's data handler, which isn't the media type.
						qtBox("hdlr", make([]byte, 8), []byte("alis"), make([]byte, 13)),
						qtBox("stbl", qtBox("stsd", make([]byte, 4), be.AppendUint32(nil, 1), entry)))))),
	}, nil)
}

// aviVideo returns the headers of a Motion JPEG AVI video with 30 frames
// of the given size at 30 frames per second, without frames.
func aviVideo(width, height int) []byte {
	le := binary.LittleEndian
	chunk := func(id string, data ...[]byte) []byte {
		b := bytes.Join(data, nil)
		return append(le.AppendUint32([]byte(id), uint32(len(b))), b...)
	}
	u32 := func(v uint32) []byte { return le.AppendUint32(nil, v) }
	avih := chunk("avih", u32(33333), u32(0), u32(0), u32(0x10), u32(30), u32(0), u32(1), u32(0),
		u32(uint32(width)), u32(uint32(height)), make([]byte, 16))
	strh := chunk("strh", []byte("vidsMJPG"), make([]byte, 48))
	strf := chunk("strf", u32(40), u32(uint32(width)), u32(uint32(height)), []byte{1, 0, 24, 0}, []byte("MJPG"), make([]byte, 20))
	hdrl := chunk("LIST", []byte("hdrl"), avih, chunk("LIST", []byte("strl"), strh, strf))
	movi := chunk("LIST", []byte("movi"))
	return chunk("RIFF", []byte("AVI "), hdrl, movi)
}

func TestPopulateFileVideoSize(t *testing.T) {
	for _, tt := range []struct {
		name          string
		data          []byte
		mime          string
		width, height string
		rotation      string
		durationMS    string
	}{
		{"IMG_0001.MOV", trackedVideo("qt  ", 1920, 1440, 90), "video/quicktime", "1440", "1920", "90", "2000"},
		{"upside-down.MOV", trackedVideo("qt  ", 1920, 1080, 180), "video/quicktime", "1920", "1080", "180", "2000"},
		{"video.3gp", trackedVideo("3gp5", 352, 288, 0), "video/3gpp", "352", "288", "", "2000"},
		{"MVI_0001.AVI", aviVideo(640, 480), "video/x-msvideo", "640", "480", "", "999"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sto, file := exifTestFile(t, tt.data, tt.name)
			mm := &mutationMap{kv: make(map[string]string)}
			if err := newLoggingIndex(t).populateFile(context.Background(), &missTrackFetcher{fetcher: sto}, file, mm); err != nil {
				t.Fatal(err)
			}
			whole := blob.RefFromBytes(tt.data)
			if got, want := mm.kv[keyFileInfo.Key(file.BlobRef())], keyFileInfo.Val(len(tt.data), tt.name, tt.mime, whole); got != want {
				t.Errorf("fileinfo = %q; want %q", got, want)
			}
			for tag, want := range map[string]string{
				MediaTagWidth:      tt.width,
				MediaTagHeight:     tt.height,
				MediaTagRotation:   tt.rotation,
				MediaTagDurationMS: tt.durationMS,
			} {
				got, ok := mm.kv[keyMediaTag.Key(whole, tag)]
				if want == "" && ok {
					t.Errorf("media tag %s = %q; want none", tag, got)
				} else if want != "" && got != keyMediaTag.Val(want) {
					t.Errorf("media tag %s = %q; want %q", tag, got, want)
				}
			}
		})
	}
}

func TestPopulateFileQuickTime(t *testing.T) {
	data := liveVideo(map[string]string{
		"com.apple.quicktime.content.identifier":           "7C16BD72-9110-4AB6-80F6-6BF098D5F43E",
		"com.apple.quicktime.make":                         "Apple",
		"com.apple.quicktime.model":                        "iPhone 16 Pro",
		"com.apple.quicktime.software":                     "26.6",
		"com.apple.quicktime.creationdate":                 "2026-10-18T17:49:20-0700",
		"com.apple.quicktime.location.ISO6709":             "+47.6305-122.3478+084.505/",
		"com.apple.quicktime.live-photo.vitality-score":    "1.000000",
		"com.apple.quicktime.smartstyle.rendering-version": "1",
	})
	sto, file := exifTestFile(t, data, "IMG_3204.MOV")
	mm := &mutationMap{kv: make(map[string]string)}
	if err := newLoggingIndex(t).populateFile(context.Background(), &missTrackFetcher{fetcher: sto}, file, mm); err != nil {
		t.Fatal(err)
	}
	whole := blob.RefFromBytes(data)
	tag := func(tag, val string) (string, string) { return keyMediaTag.Key(whole, tag), keyMediaTag.Val(val) }
	want := map[string]string{
		keyFileInfo.Key(file.BlobRef()):              keyFileInfo.Val(len(data), "IMG_3204.MOV", "video/quicktime", whole),
		keyFileTimes.Key(file.BlobRef()):             keyFileTimes.Val("2023-11-14T22:13:20Z"),
		keyWholeToFileRef.Key(whole, file.BlobRef()): "1",
		keyEXIFGPS.Key(whole):                        "47.6305000|-122.3478000",
	}
	for _, kv := range [][2]string{
		{MediaTagContentIdentifier, "7C16BD72-9110-4AB6-80F6-6BF098D5F43E"},
		{MediaTagDurationMS, "740"},
		{MediaTagMake, "Apple"},
		{MediaTagModel, "iPhone 16 Pro"},
		{MediaTagSoftware, "26.6"},
		{MediaTagCreationDate, "2026-10-18T17:49:20-0700"},
		{MediaTagLivePhotoVitalityScore, "1.000000"},
	} {
		k, v := tag(kv[0], kv[1])
		want[k] = v
	}
	addWantDigestRows(want, data)
	if !reflect.DeepEqual(mm.kv, want) {
		t.Errorf("mutations = %v; want %v", mm.kv, want)
	}
}
