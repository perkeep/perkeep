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

package applemakernote

import (
	"encoding/binary"
	"errors"
	"os"
	"testing"
)

// iphone16pro.makernote is the MakerNote of a Live Photo still taken
// with an iPhone 16 Pro on iOS 26.6.
func TestParseIPhone(t *testing.T) {
	b, err := os.ReadFile("testdata/iphone16pro.makernote")
	if err != nil {
		t.Fatal(err)
	}
	mn, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if mn.Version != 1 {
		t.Errorf("Version = %d; want 1", mn.Version)
	}
	if got, want := mn.ContentIdentifier, "7C16BD72-9110-4AB6-80F6-6BF098D5F43E"; got != want {
		t.Errorf("ContentIdentifier = %q; want %q", got, want)
	}
	if got, want := mn.PhotoIdentifier, "ADBDBDE4-A3F3-4F20-9503-E2307A84E3C2"; got != want {
		t.Errorf("PhotoIdentifier = %q; want %q", got, want)
	}
	if mn.BurstUUID != "" {
		t.Errorf("BurstUUID = %q; want empty", mn.BurstUUID)
	}
	if len(mn.Tags) != 60 {
		t.Errorf("got %d tags; want 60", len(mn.Tags))
	}
}

type entry struct {
	id, typ uint16
	count   uint32
	value   []byte // inline if ≤ 4 bytes
}

// makerNote builds an Apple MakerNote with the given entries.
func makerNote(entries ...entry) []byte {
	be := binary.BigEndian
	b := append([]byte("Apple iOS\x00\x00\x01MM"), 0, byte(len(entries)))
	dataOff := len(b) + 12*len(entries)
	var data []byte
	for _, e := range entries {
		b = be.AppendUint16(b, e.id)
		b = be.AppendUint16(b, e.typ)
		b = be.AppendUint32(b, e.count)
		if len(e.value) <= 4 {
			b = append(b, e.value...)
			b = append(b, make([]byte, 4-len(e.value))...)
		} else {
			b = be.AppendUint32(b, uint32(dataOff+len(data)))
			data = append(data, e.value...)
		}
	}
	return append(b, data...)
}

func TestParseSynthetic(t *testing.T) {
	burst := "1B2C3D4E-0000-4000-8000-000000000001\x00"
	b := makerNote(
		entry{0x0001, 9, 1, []byte{0, 0, 0, 16}},
		entry{TagBurstUUID, 2, uint32(len(burst)), []byte(burst)},
		entry{0x0099, 99, 1, []byte{1}},                         // unknown type: skipped
		entry{TagContentIdentifier, 2, 1000, []byte("ignored")}, // out of bounds: skipped
		entry{TagPhotoIdentifier, 2, 3, []byte("ab\x00")},       // inline
	)
	mn, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if mn.BurstUUID != burst[:len(burst)-1] || mn.ContentIdentifier != "" || mn.PhotoIdentifier != "ab" {
		t.Errorf("got %+v", mn)
	}
	if len(mn.Tags) != 3 {
		t.Errorf("got %d tags; want 3", len(mn.Tags))
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse([]byte("Nikon\x00\x02\x10\x00\x00")); !errors.Is(err, ErrNotApple) {
		t.Errorf("non-Apple: err = %v; want ErrNotApple", err)
	}
	for _, b := range [][]byte{
		[]byte("Apple iOS\x00"),
		[]byte("Apple iOS\x00\x00\x01II\x00\x01"),
		makerNote(entry{1, 9, 1, nil})[:20], // truncated entries
	} {
		if _, err := Parse(b); err == nil {
			t.Errorf("Parse(%q) succeeded; want error", b)
		}
	}
}

func FuzzParse(f *testing.F) {
	if b, err := os.ReadFile("testdata/iphone16pro.makernote"); err == nil {
		f.Add(b)
	}
	f.Add(makerNote(entry{TagContentIdentifier, 2, 5, []byte("abcd\x00")}))
	f.Fuzz(func(t *testing.T, b []byte) {
		Parse(b)
	})
}
