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

package quicktime

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// videoTrak returns a "trak" box of an enabled HEVC video track of the
// given stored size, rotated 90 degrees clockwise if rotate90.
func videoTrak(width, height int, rotate90 bool) []byte {
	const one = 1 << 16
	a, b, c, d := int32(one), int32(0), int32(0), int32(one)
	if rotate90 {
		a, b, c, d = 0, one, -one, 0
	}
	var matrix []byte
	for _, v := range []int32{a, b, 0, c, d, 0, 0, 0, 1 << 30} {
		matrix = binary.BigEndian.AppendUint32(matrix, uint32(v))
	}
	tkhd := box("tkhd", []byte{0, 0, 0, 1}, make([]byte, 20+8+8), matrix, u32(uint32(width)<<16), u32(uint32(height)<<16))
	entry := box("hvc1", make([]byte, 24), binary.BigEndian.AppendUint16(nil, uint16(width)),
		binary.BigEndian.AppendUint16(nil, uint16(height)), make([]byte, 50))
	return box("trak", tkhd,
		box("mdia",
			box("mdhd", make([]byte, 24)),
			box("hdlr", make([]byte, 8), []byte("vide"), make([]byte, 13)),
			box("minf",
				box("hdlr", make([]byte, 8), []byte("alis"), make([]byte, 13)),
				box("stbl", box("stsd", u32(0), u32(1), entry)))))
}

func TestReadMetadataVideoTrack(t *testing.T) {
	file := bytes.Join([][]byte{
		box("ftyp", []byte("qt  "), u32(0), []byte("qt  ")),
		box("mdat", make([]byte, 100)),
		box("moov",
			box("mvhd", u32(0), u32(0), u32(0), u32(600), u32(600), make([]byte, 80)),
			videoTrak(256, 192, true),
			videoTrak(1920, 1440, true),
			box("trak", box("tkhd", []byte{0, 0, 0, 1}, make([]byte, 80)),
				box("mdia", box("hdlr", make([]byte, 8), []byte("soun"), make([]byte, 13))))),
	}, nil)
	md := readMetadata(t, file)
	if md.Width != 1440 || md.Height != 1920 || md.Rotation != 90 {
		t.Errorf("video size = %dx%d rotated %d; want 1440x1920 rotated 90", md.Width, md.Height, md.Rotation)
	}
}

func FuzzReadTrack(f *testing.F) {
	trak := videoTrak(1920, 1080, true)
	f.Add(trak[8:]) // the box's body
	f.Fuzz(func(t *testing.T, in []byte) {
		if tr, err := readTrack(bytes.NewReader(in), true); err == nil {
			tr.displaySize()
		}
	})
}

func TestMatrixRotation(t *testing.T) {
	const one = 1 << 16
	for _, tt := range []struct {
		a, b, c, d int32
		want       int
	}{
		{one, 0, 0, one, 0},
		{0, one, -one, 0, 90},
		{-one, 0, 0, -one, 180},
		{0, -one, one, 0, 270},
		{2 * one, 0, 0, one, 0},   // scaled
		{-one, 0, 0, one, 0},      // flipped
		{0, 0, 0, 0, 0},           // degenerate
		{0, 2 * one, -one, 0, 90}, // rotated and scaled
	} {
		if got := matrixRotation(tt.a, tt.b, tt.c, tt.d); got != tt.want {
			t.Errorf("matrixRotation(%d, %d, %d, %d) = %d; want %d", tt.a, tt.b, tt.c, tt.d, got, tt.want)
		}
	}
}

func TestMainVideoTrack(t *testing.T) {
	audio := &track{enabled: true, handler: "soun", width: 4000, height: 3000}
	disabled := &track{handler: "vide", width: 3840, height: 2160}
	small := &track{enabled: true, handler: "vide", width: 256, height: 192}
	main := &track{enabled: true, handler: "vide", width: 1920, height: 1440, rotation: 90}
	if got := mainVideoTrack([]*track{audio, small, disabled, main}); got != main {
		t.Errorf("mainVideoTrack = %+v; want the largest enabled video track", got)
	}
	if got := mainVideoTrack([]*track{audio, disabled}); got != nil {
		t.Errorf("mainVideoTrack without enabled video tracks = %+v; want nil", got)
	}
	if w, h := main.displaySize(); w != 1440 || h != 1920 {
		t.Errorf("rotated displaySize = %dx%d; want 1440x1920", w, h)
	}
	// Without a track header size, the sample entry's size is used.
	entryOnly := &track{enabled: true, handler: "vide", entryWidth: 640, entryHeight: 480, rotation: 180}
	if w, h := entryOnly.displaySize(); w != 640 || h != 480 {
		t.Errorf("sample entry displaySize = %dx%d; want 640x480", w, h)
	}
}
