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
	"errors"
	"testing"
	"time"
)

func chunk(id string, data ...[]byte) []byte {
	b := bytes.Join(data, nil)
	out := binary.LittleEndian.AppendUint32([]byte(id), uint32(len(b)))
	out = append(out, b...)
	if len(b)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

func u32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

// testAVI returns the headers of an AVI file with an audio stream (with
// an odd-sized, padded chunk) followed by a Motion JPEG video stream.
func testAVI() []byte {
	avih := chunk("avih", u32(40000), u32(0), u32(0), u32(0x10), u32(250), u32(0), u32(2), u32(0),
		u32(640), u32(480), make([]byte, 16))
	audio := chunk("LIST", []byte("strl"), chunk("strh", []byte("auds\x00\x00\x00\x00"), make([]byte, 48)), chunk("strf", make([]byte, 17)))
	video := chunk("LIST", []byte("strl"),
		chunk("strh", []byte("vidsmjpg"), make([]byte, 48)),
		chunk("strf", u32(40), u32(640), u32(480), []byte{1, 0, 24, 0}, []byte("MJPG"), make([]byte, 20)))
	hdrl := chunk("LIST", []byte("hdrl"), avih, audio, video)
	return chunk("RIFF", []byte("AVI "), hdrl, chunk("LIST", []byte("movi")))
}

func TestReadHeader(t *testing.T) {
	b := testAVI()
	h, err := ReadHeader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	want := Header{Width: 640, Height: 480, FrameDuration: 40 * time.Millisecond, Frames: 250, Codec: "MJPG", VideoStream: 1}
	if *h != want {
		t.Errorf("ReadHeader = %+v; want %+v", *h, want)
	}
	if d := h.Duration(); d != 10*time.Second {
		t.Errorf("Duration = %v; want 10s", d)
	}
}

func TestReadHeaderErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"wav", chunk("RIFF", []byte("WAVE"), chunk("fmt ", make([]byte, 16)))},
		{"mp4", append([]byte("\x00\x00\x00\x18ftypmp42"), make([]byte, 16)...)},
	} {
		if _, err := ReadHeader(bytes.NewReader(tt.in), int64(len(tt.in))); !errors.Is(err, ErrNotAVI) {
			t.Errorf("%s: ReadHeader error = %v; want ErrNotAVI", tt.name, err)
		}
	}

	// A chunk claiming more data than its list holds.
	b := testAVI()
	i := bytes.Index(b, []byte("avih"))
	binary.LittleEndian.PutUint32(b[i+4:], 1<<20)
	if _, err := ReadHeader(bytes.NewReader(b), int64(len(b))); err == nil {
		t.Error("ReadHeader of corrupt chunk size succeeded; want error")
	}
}

func FuzzReadHeader(f *testing.F) {
	f.Add(testAVI())
	f.Fuzz(func(t *testing.T, in []byte) {
		h, err := ReadHeader(bytes.NewReader(in), int64(len(in)))
		if err == nil {
			h.Duration()
		}
	})
}
