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
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func box(typ string, body ...[]byte) []byte {
	b := bytes.Join(body, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(b)))
	out = append(out, typ...)
	return append(out, b...)
}

func u32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

type item struct {
	key      string
	dataType uint32
	val      []byte
}

// movie returns a QuickTime file with the given media data size,
// movie duration, and mdta metadata items.
func movie(mdatSize int, timeScale, dur uint32, items ...item) []byte {
	var keys, ilst [][]byte
	for i, it := range items {
		keys = append(keys, u32(uint32(8+len(it.key))), []byte("mdta"+it.key))
		ilst = append(ilst, box(string(u32(uint32(i+1))), box("data", u32(it.dataType), u32(0), it.val)))
	}
	meta := box("meta",
		box("hdlr", make([]byte, 8), []byte("mdta"), make([]byte, 12)),
		box("keys", u32(0), u32(uint32(len(items))), bytes.Join(keys, nil)),
		box("ilst", ilst...),
	)
	mvhd := box("mvhd", u32(0), u32(0), u32(0), u32(timeScale), u32(dur), make([]byte, 80))
	trak := box("trak", box("tkhd", make([]byte, 84)))
	return bytes.Join([][]byte{
		box("ftyp", []byte("qt  "), u32(0), []byte("qt  ")),
		box("wide"),
		box("mdat", make([]byte, mdatSize)),
		box("moov", mvhd, trak, meta),
	}, nil)
}

func readMetadata(t *testing.T, file []byte) *Metadata {
	t.Helper()
	md, err := ReadMetadata(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	return md
}

func TestReadMetadata(t *testing.T) {
	file := movie(100000, 600, 444,
		item{KeyLocationISO6709, 1, []byte("+48.8584+002.2945+035.000/")},
		item{KeyLivePhotoAuto, 22, []byte{1}},
		item{"com.example.signed", 21, []byte{0xff, 0xfe}},
		item{"com.example.uint64", 22, binary.BigEndian.AppendUint64(nil, math.MaxUint64)},
		item{KeyContentIdentifier, 1, []byte("7C16BD72-9110-4AB6-80F6-6BF098D5F43E")},
		item{KeyLivePhotoVitality, 23, binary.BigEndian.AppendUint32(nil, math.Float32bits(0.75))},
		item{"com.example.float64", 24, binary.BigEndian.AppendUint64(nil, math.Float64bits(13.251413))},
		item{"com.example.jpeg", 13, []byte{0xff, 0xd8}},
		item{"com.example.badint", 21, []byte{1, 2, 3, 4, 5}},
		item{KeyContentIdentifier, 1, []byte("duplicate")},
	)
	md := readMetadata(t, file)
	if want := 740 * time.Millisecond; md.Duration != want {
		t.Errorf("Duration = %v; want %v", md.Duration, want)
	}
	want := map[string]string{
		KeyLocationISO6709:    "+48.8584+002.2945+035.000/",
		KeyLivePhotoAuto:      "1",
		"com.example.signed":  "-2",
		"com.example.uint64":  "18446744073709551615",
		KeyContentIdentifier:  "7C16BD72-9110-4AB6-80F6-6BF098D5F43E",
		KeyLivePhotoVitality:  "0.75",
		"com.example.float64": "13.251413",
	}
	if !reflect.DeepEqual(md.Items, want) {
		t.Errorf("Items = %q; want %q", md.Items, want)
	}
}

func TestReadMetadataNoItems(t *testing.T) {
	md := readMetadata(t, movie(10, 1000, 0xffffffff))
	if md.Duration != 0 || md.Items != nil {
		t.Errorf("got %+v; want zero duration, no items", md)
	}
}

func TestReadMetadataNoMovie(t *testing.T) {
	file := box("ftyp", []byte("qt  "), u32(0))
	_, err := ReadMetadata(bytes.NewReader(file), int64(len(file)))
	if !errors.Is(err, ErrNoMovie) {
		t.Errorf("err = %v; want ErrNoMovie", err)
	}
}

func TestReadMetadataTruncated(t *testing.T) {
	file := movie(10, 600, 444, item{KeyContentIdentifier, 1, []byte("id")})
	for n := range len(file) {
		ReadMetadata(bytes.NewReader(file[:n]), int64(n)) // must not panic
	}
}

func TestParseISO6709(t *testing.T) {
	tests := []struct {
		in        string
		lat, long float64
		ok        bool
	}{
		{"+48.8584+002.2945+035.000/", 48.8584, 2.2945, true},
		{"+48.8584+002.2945/", 48.8584, 2.2945, true},
		{"-33.8688+151.2093", -33.8688, 151.2093, true},
		{"+4851.504+00217.670/", 48 + 51.504/60, 2 + 17.670/60, true},
		{"+485130+0021740/", 48 + 51.0/60 + 30.0/3600, 2 + 17.0/60 + 40.0/3600, true},
		{"+48.8584+002.2945CRSWGS_84/", 48.8584, 2.2945, true},
		{"", 0, 0, false},
		{"48.8584+002.2945/", 0, 0, false},
		{"+48.8584/", 0, 0, false},
		{"+91.0+002.0/", 0, 0, false},
		{"+48.0+181.0/", 0, 0, false},
		{"+4860.0+00217.0/", 0, 0, false}, // 60 minutes
		{"+4.8584+002.2945/", 0, 0, false},
		{"+48.+002.2945/", 0, 0, false},
		{"+48.8584+002.2945x/", 0, 0, false},
	}
	for _, tt := range tests {
		lat, long, ok := ParseISO6709(tt.in)
		if ok != tt.ok || math.Abs(lat-tt.lat) > 1e-9 || math.Abs(long-tt.long) > 1e-9 {
			t.Errorf("ParseISO6709(%q) = %v, %v, %v; want %v, %v, %v", tt.in, lat, long, ok, tt.lat, tt.long, tt.ok)
		}
	}
}

func FuzzReadMetadata(f *testing.F) {
	f.Add(movie(10, 600, 444, item{KeyContentIdentifier, 1, []byte("id")}, item{KeyLivePhotoAuto, 22, []byte{1}}))
	f.Fuzz(func(t *testing.T, in []byte) {
		ReadMetadata(bytes.NewReader(in), int64(len(in)))
	})
}

func FuzzParseISO6709(f *testing.F) {
	f.Add("+48.8584+002.2945+035.000/")
	f.Add("+485130+0021740/")
	f.Fuzz(func(t *testing.T, s string) {
		lat, long, ok := ParseISO6709(s)
		if ok && (math.Abs(lat) > 90 || math.Abs(long) > 180) {
			t.Errorf("ParseISO6709(%q) = %v, %v out of range", s, lat, long)
		}
	})
}
