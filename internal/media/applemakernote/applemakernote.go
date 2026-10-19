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

// Package applemakernote decodes the EXIF MakerNote written by Apple
// devices' cameras.
//
// Tag numbers and names follow ExifTool's Apple tag table.
package applemakernote // import "perkeep.org/internal/media/applemakernote"

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
)

// Tag numbers.
const (
	TagBurstUUID         = 0x000b
	TagContentIdentifier = 0x0011
	TagPhotoIdentifier   = 0x002b
)

// MakerNote is a decoded Apple MakerNote.
type MakerNote struct {
	Version uint16

	// ContentIdentifier is set on the still image of a Live Photo, and
	// matches the content identifier metadata item of its video.
	ContentIdentifier string

	// BurstUUID is shared by all the images in a burst.
	BurstUUID string

	PhotoIdentifier string

	// Tags are all of the MakerNote's entries, in order.
	Tags []Tag
}

// Tag is a MakerNote entry.
type Tag struct {
	ID    uint16
	Type  uint16 // TIFF field type
	Count uint32
	Value []byte // raw big-endian bytes
}

// TIFF field types.
const (
	typeASCII = 2
)

// typeSizes are the sizes of TIFF field types, including BigTIFF's
// 8-byte integers (types 16-18), which Apple uses.
var typeSizes = map[uint16]uint32{
	1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 6: 1, 7: 1, 8: 2, 9: 4, 10: 8, 11: 4, 12: 8,
	13: 4, 16: 8, 17: 8, 18: 8,
}

var magic = []byte("Apple iOS\x00")

// ErrNotApple is returned by Parse for MakerNotes not written by Apple.
var ErrNotApple = errors.New("applemakernote: not an Apple MakerNote")

var errFormat = errors.New("applemakernote: invalid format")

// maxEntries bounds the number of IFD entries Parse accepts. Apple's
// have fewer than a hundred.
const maxEntries = 1000

// Parse decodes the Apple MakerNote b, the value of the EXIF MakerNote
// tag. Value offsets in an Apple MakerNote are relative to its start.
func Parse(b []byte) (*MakerNote, error) {
	if !bytes.HasPrefix(b, magic) {
		return nil, ErrNotApple
	}
	// Header: magic, version, byte order mark, IFD entry count.
	const hdrLen = 16
	if len(b) < hdrLen || string(b[12:14]) != "MM" {
		return nil, errFormat
	}
	be := binary.BigEndian
	mn := &MakerNote{Version: be.Uint16(b[10:12])}
	n := int(be.Uint16(b[14:16]))
	if n > maxEntries || hdrLen+12*n > len(b) {
		return nil, errFormat
	}
	for i := range n {
		e := b[hdrLen+12*i:][:12]
		t := Tag{ID: be.Uint16(e[0:2]), Type: be.Uint16(e[2:4]), Count: be.Uint32(e[4:8])}
		sz, ok := typeSizes[t.Type]
		if !ok {
			continue
		}
		size := uint64(sz) * uint64(t.Count)
		if size <= 4 {
			t.Value = e[8 : 8+size]
		} else {
			off := uint64(be.Uint32(e[8:12]))
			if off+size > uint64(len(b)) {
				continue
			}
			t.Value = b[off : off+size]
		}
		mn.Tags = append(mn.Tags, t)
		if t.Type != typeASCII {
			continue
		}
		s := strings.TrimRight(string(t.Value), "\x00")
		switch t.ID {
		case TagContentIdentifier:
			mn.ContentIdentifier = s
		case TagBurstUUID:
			mn.BurstUUID = s
		case TagPhotoIdentifier:
			mn.PhotoIdentifier = s
		}
	}
	return mn, nil
}
