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

package avi

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"reflect"
	"testing"
)

// moviChunk is a chunk of a test AVI's movie data list.
type moviChunk struct {
	id   string
	data string
	rec  bool // in a "rec " list with the previous chunk marked rec
}

// testMovie returns an AVI file with the given movie data chunks and, if
// index is "relative" or "absolute", an index with offsets of that kind.
// It also returns the offsets and sizes of the "00dc" chunks' data.
func testMovie(chunks []moviChunk, index string) ([]byte, []Frame) {
	hdrl := chunk("LIST", []byte("hdrl"), chunk("avih", make([]byte, 56)))
	moviStart := int64(12 + len(hdrl) + 8) // the "movi" fourcc
	var body []byte
	var idx []byte
	var frames []Frame
	var rec []byte // a "rec " list's chunks in progress
	var recStart int64
	add := func(c moviChunk, off int64) {
		rel := off - moviStart
		if index == "absolute" {
			rel = off
		}
		idx = append(idx, c.id...)
		idx = append(idx, u32(0x10)...)
		idx = append(idx, u32(uint32(rel))...)
		idx = append(idx, u32(uint32(len(c.data)))...)
		if c.id == "00dc" {
			frames = append(frames, Frame{Offset: off + 8, Size: int64(len(c.data))})
		}
	}
	flushRec := func() {
		if rec != nil {
			body = append(body, chunk("LIST", []byte("rec "), rec)...)
			rec = nil
		}
	}
	for _, c := range chunks {
		ch := chunk(c.id, []byte(c.data))
		if c.rec {
			if rec == nil {
				recStart = moviStart + 4 + int64(len(body)) + 12 // past the list's header and type
			}
			add(c, recStart+int64(len(rec)))
			rec = append(rec, ch...)
			continue
		}
		flushRec()
		add(c, moviStart+4+int64(len(body)))
		body = append(body, ch...)
	}
	flushRec()
	parts := [][]byte{[]byte("AVI "), hdrl, chunk("LIST", []byte("movi"), body)}
	if index != "" {
		parts = append(parts, chunk("idx1", idx))
	}
	return chunk("RIFF", parts...), frames
}

func TestFrames(t *testing.T) {
	chunks := []moviChunk{
		{id: "01wb", data: "audio"},
		{id: "00dc", data: "frame 1"},
		{id: "00dc", data: "frame two"},
		{id: "01wb", data: "more audio", rec: true},
		{id: "00dc", data: "frame 3", rec: true},
		{id: "00db", data: "frame four"},
		{id: "00dc", data: ""}, // a dropped frame
		{id: "JUNK", data: "padding"},
	}
	for _, index := range []string{"relative", "absolute", ""} {
		b, want := testMovie(chunks, index)
		// Include the uncompressed "00db" frame.
		want = append(want[:3], append([]Frame{{Offset: 0, Size: 10}}, want[3:]...)...)
		i := bytes.Index(b, []byte("00dbframe four")[:4])
		want[3].Offset = int64(i + 8)
		got, err := Frames(bytes.NewReader(b), int64(len(b)), 0)
		if err != nil {
			t.Errorf("%q index: %v", index, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q index: Frames = %+v; want %+v", index, got, want)
		}
		for _, f := range got {
			if f.Size > 0 && !bytes.HasPrefix(b[f.Offset:], []byte("frame")) {
				t.Errorf("%q index: frame at %d isn't a frame", index, f.Offset)
			}
		}
	}
}

func TestFramesOtherStream(t *testing.T) {
	b, _ := testMovie([]moviChunk{{id: "00wb", data: "audio"}, {id: "01dc", data: "frame"}}, "relative")
	got, err := Frames(bytes.NewReader(b), int64(len(b)), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !bytes.Equal(b[got[0].Offset:got[0].Offset+got[0].Size], []byte("frame")) {
		t.Errorf("Frames of stream 1 = %+v", got)
	}
}

func TestFramesErrors(t *testing.T) {
	b, _ := testMovie([]moviChunk{{id: "01wb", data: "audio"}}, "relative")
	if _, err := Frames(bytes.NewReader(b), int64(len(b)), 0); err == nil {
		t.Error("Frames of a video without frames succeeded; want error")
	}
	if _, err := Frames(bytes.NewReader(b), int64(len(b)), 100); err == nil {
		t.Error("Frames of stream 100 succeeded; want error")
	}
	if _, err := Frames(bytes.NewReader([]byte("RIFF....WAVE")), 12, 0); err != ErrNotAVI {
		t.Errorf("Frames of a WAVE file: %v; want ErrNotAVI", err)
	}

	// An index whose offsets don't match the chunks: Frames scans instead.
	b, want := testMovie([]moviChunk{{id: "00dc", data: "frame"}}, "relative")
	i := bytes.LastIndex(b, []byte("00dc"))
	binary.LittleEndian.PutUint32(b[i+8:], 999)
	got, err := Frames(bytes.NewReader(b), int64(len(b)), 0)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Frames with a bad index = %+v, %v; want %+v", got, err, want)
	}
}

func TestDecodeMJPEG(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for y := range 16 {
		for x := range 32 {
			im.Set(x, y, color.RGBA{uint8(x * 8), uint8(y * 16), 100, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, im, nil); err != nil {
		t.Fatal(err)
	}
	full := buf.Bytes()
	want, err := jpeg.Decode(bytes.NewReader(full))
	if err != nil {
		t.Fatal(err)
	}

	// Remove the DHT segment, as Motion JPEG frames do.
	i := bytes.Index(full, []byte{0xff, 0xc4})
	n := int(binary.BigEndian.Uint16(full[i+2:]))
	stripped := append(bytes.Clone(full[:i]), full[i+2+n:]...)
	if _, err := jpeg.Decode(bytes.NewReader(stripped)); err == nil {
		t.Fatal("image/jpeg decoded a JPEG without Huffman tables; the test needs another way to make one")
	}

	for name, frame := range map[string][]byte{"with tables": full, "without tables": stripped} {
		got, err := DecodeMJPEG(frame)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: decoded image differs", name)
		}
	}
}

func FuzzFrames(f *testing.F) {
	for _, index := range []string{"relative", "absolute", ""} {
		b, _ := testMovie([]moviChunk{{id: "00dc", data: "frame"}, {id: "01wb", data: "x", rec: true}}, index)
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		frames, err := Frames(bytes.NewReader(in), int64(len(in)), 0)
		if err != nil {
			return
		}
		for _, fr := range frames {
			if fr.Offset < 0 || fr.Size < 0 || fr.Offset+fr.Size > int64(len(in)) {
				t.Fatalf("frame %+v outside the %d byte input", fr, len(in))
			}
		}
	})
}
