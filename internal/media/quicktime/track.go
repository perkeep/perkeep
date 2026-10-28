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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"go4.org/media/heif/bmff"
)

// maxLeafBoxSize is the largest track header or sample description box
// that is read. They're typically tens to hundreds of bytes.
const maxLeafBoxSize = 1 << 20

// track is what readTrack reads of a "trak" box.
type track struct {
	enabled bool
	handler string // media handler type, e.g. "vide" for video

	// width and height are the track's presentation size from its
	// header, before rotation, or zero if unset.
	width, height int

	// rotation is how many degrees clockwise the track is rotated for
	// display: 0, 90, 180, or 270.
	rotation int

	codec string // first sample entry's format, e.g. "hvc1" or "avc1"

	// entryWidth and entryHeight are the first visual sample entry's
	// pixel size, before rotation, or zero if unset.
	entryWidth, entryHeight int

	// config is the first visual sample entry's decoder configuration
	// box (such as "hvcC" or "avcC"), including its header, or nil.
	config []byte

	// color is the first visual sample entry's ISO color information
	// box ("colr" of type "nclx"), including its header, or nil.
	color []byte

	// colorInfo is the first visual sample entry's color information,
	// ISO or QuickTime.
	colorInfo ColorInfo

	// clap is the first visual sample entry's clean aperture box, which
	// crops the coded frames for display, including its header, or nil.
	clap []byte

	// timescale is the media's time units per second, from its "mdhd"
	// box, and duration its duration in those units.
	timescale uint32
	duration  uint64

	// mediaStart is the media time, in timescale units, at which the
	// track's edit list starts presenting it.
	mediaStart int64

	// tables are the sample table boxes' bodies, if readTrack was asked
	// for them.
	tables *sampleTables
}

// displaySize returns the track's size as displayed, after rotation.
func (t *track) displaySize() (width, height int) {
	width, height = t.width, t.height
	if width <= 0 || height <= 0 {
		width, height = t.entryWidth, t.entryHeight
	}
	if t.rotation == 90 || t.rotation == 270 {
		width, height = height, width
	}
	return width, height
}

// mainVideoTrack returns the enabled video track with the largest size,
// or nil if there is none. iPhone Live Photo movies, for instance, have
// small extra video tracks besides the main one.
func mainVideoTrack(tracks []*track) *track {
	var best *track
	var bestArea int64
	for _, t := range tracks {
		if t.handler != "vide" || !t.enabled {
			continue
		}
		w, h := t.displaySize()
		if area := int64(w) * int64(h); best == nil || area > bestArea {
			best, bestArea = t, area
		}
	}
	return best
}

// readTrack reads the body of a "trak" box, including its sample tables
// if withSamples.
func readTrack(trak io.Reader, withSamples bool) (*track, error) {
	t := new(track)
	if withSamples {
		t.tables = new(sampleTables)
	}
	err := forEachBox(trak, func(b bmff.Box) error {
		switch b.Type().String() {
		case "tkhd":
			body, err := readLeaf(b)
			if err != nil {
				return err
			}
			return t.parseTrackHeader(body)
		case "mdia":
			return forEachBox(b.Body(), t.readMediaBox)
		case "edts":
			return forEachBox(b.Body(), func(b bmff.Box) error {
				if !b.Type().EqualString("elst") {
					return nil
				}
				body, err := readLeaf(b)
				if err != nil {
					return err
				}
				t.parseEditList(body)
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// readMediaBox reads a box within a track's "mdia" box.
func (t *track) readMediaBox(b bmff.Box) error {
	switch b.Type().String() {
	case "hdlr":
		// Only the "mdia" box's handler is the media type. QuickTime
		// "minf" boxes have a data handler too.
		body, err := readLeaf(b)
		if err != nil {
			return err
		}
		// FullBox header, pre_defined, then the handler type.
		if len(body) >= 12 {
			t.handler = string(body[8:12])
		}
	case "mdhd":
		body, err := readLeaf(b)
		if err != nil {
			return err
		}
		t.parseMediaHeader(body)
	case "minf":
		return forEachBox(b.Body(), func(b bmff.Box) error {
			if !b.Type().EqualString("stbl") {
				return nil
			}
			return forEachBox(b.Body(), t.readSampleTableBox)
		})
	}
	return nil
}

// readSampleTableBox reads a box within a track's "stbl" box.
func (t *track) readSampleTableBox(b bmff.Box) error {
	if b.Type().EqualString("stsd") {
		body, err := readLeaf(b)
		if err != nil {
			return err
		}
		t.parseSampleDescription(body)
		return nil
	}
	if t.tables == nil {
		return nil
	}
	dst := t.tables.box(b.Type().String())
	if dst == nil {
		return nil
	}
	n := b.Size() - b.HeaderSize()
	if b.Size() <= 0 || n > maxTableBoxSize {
		return fmt.Errorf("quicktime: unsupported %q box size %d", b.Type(), b.Size())
	}
	*dst = make([]byte, n)
	if _, err := io.ReadFull(b.Body(), *dst); err != nil {
		return fmt.Errorf("quicktime: reading %q box: %w", b.Type(), err)
	}
	return nil
}

// parseEditList parses an "elst" box, setting t.mediaStart to the media
// time of its first edit that isn't empty (a delay).
func (t *track) parseEditList(b []byte) {
	be := binary.BigEndian
	if len(b) < 8 {
		return
	}
	version, n := b[0], be.Uint32(b[4:])
	entry := 12 // 32-bit segment duration, media time, and media rate
	if version == 1 {
		entry = 20 // 64-bit duration and media time
	}
	for i := 0; i < int(min(n, 1000)) && 8+(i+1)*entry <= len(b); i++ {
		e := b[8+i*entry:]
		var mediaTime int64
		if version == 1 {
			mediaTime = int64(be.Uint64(e[8:]))
		} else {
			mediaTime = int64(int32(be.Uint32(e[4:])))
		}
		if mediaTime >= 0 { // -1 is an empty edit
			t.mediaStart = mediaTime
			return
		}
	}
}

// parseMediaHeader parses an "mdhd" box.
func (t *track) parseMediaHeader(b []byte) {
	if len(b) < 4 {
		return
	}
	be := binary.BigEndian
	// After the FullBox header, creation and modification times, then
	// the timescale and duration, all 32 bits, or 64 for times and
	// duration in version 1.
	switch {
	case b[0] == 0 && len(b) >= 20:
		t.timescale, t.duration = be.Uint32(b[12:]), uint64(be.Uint32(b[16:]))
		if t.duration == math.MaxUint32 { // unknown
			t.duration = 0
		}
	case b[0] == 1 && len(b) >= 32:
		t.timescale, t.duration = be.Uint32(b[20:]), be.Uint64(b[24:])
		if t.duration == math.MaxUint64 {
			t.duration = 0
		}
	}
}

func (t *track) parseTrackHeader(b []byte) error {
	if len(b) < 4 {
		return errors.New("quicktime: short tkhd box")
	}
	version, flags := b[0], binary.BigEndian.Uint32(b[0:4])&0xffffff
	t.enabled = flags&1 != 0
	// Times, track ID, reserved, and duration, then 8 reserved bytes,
	// layer, alternate group, volume, and 2 reserved bytes.
	off := 4 + 20 + 8 + 8
	if version == 1 {
		off = 4 + 32 + 8 + 8
	}
	if len(b) < off+36+8 {
		return errors.New("quicktime: short tkhd box")
	}
	m := func(i int) int32 { return int32(binary.BigEndian.Uint32(b[off+4*i:])) }
	t.rotation = matrixRotation(m(0), m(1), m(3), m(4))
	off += 36
	// Width and height are 16.16 fixed point.
	t.width = int(binary.BigEndian.Uint32(b[off:]) >> 16)
	t.height = int(binary.BigEndian.Uint32(b[off+4:]) >> 16)
	return nil
}

// matrixRotation returns the clockwise rotation in degrees of a track
// header's transformation matrix, of which a, b, c, and d are the
// scale and rotation terms, ignoring scaling and treating other
// transformations, such as flips, as unrotated.
func matrixRotation(a, b, c, d int32) int {
	switch {
	case a > 0 && d > 0 && b == 0 && c == 0:
		return 0
	case a == 0 && d == 0 && b > 0 && c < 0:
		return 90
	case a < 0 && d < 0 && b == 0 && c == 0:
		return 180
	case a == 0 && d == 0 && b < 0 && c > 0:
		return 270
	}
	return 0
}

// parseSampleDescription parses an "stsd" box's first sample entry.
func (t *track) parseSampleDescription(b []byte) {
	// FullBox header and entry count, then the first entry's size and
	// format.
	if len(b) < 16 {
		return
	}
	entry := b[8:]
	size := binary.BigEndian.Uint32(entry)
	if size < 8 || uint64(size) > uint64(len(entry)) {
		return
	}
	entry = entry[:size]
	t.codec = string(entry[4:8])
	if t.handler != "vide" {
		return
	}
	// A VisualSampleEntry: after its 8 byte header, 6 reserved bytes, a
	// data reference index, and 16 more reserved bytes come the width
	// and height.
	if len(entry) >= 36 {
		t.entryWidth = int(binary.BigEndian.Uint16(entry[32:]))
		t.entryHeight = int(binary.BigEndian.Uint16(entry[34:]))
	}
	// After the size: resolutions, reserved, frame count, compressor
	// name, depth, and pre_defined, 50 bytes, then child boxes, the
	// first of which with a type ending in "C" is the decoder
	// configuration ("hvcC", "avcC", "av1C", etc).
	for kids := entry[min(len(entry), 86):]; len(kids) >= 8; {
		n := binary.BigEndian.Uint32(kids)
		if n < 8 || uint64(n) > uint64(len(kids)) {
			break
		}
		switch {
		case kids[7] == 'C' && t.config == nil:
			t.config = kids[:n:n]
		case string(kids[4:8]) == "colr" && n >= 18 && !t.colorInfo.Known:
			// ISO ("nclx") or QuickTime ("nclc") color information.
			// Only ISO's includes whether the video uses the full
			// range, and is a HEIF property, to keep for decoders.
			kind := string(kids[8:12])
			if kind != "nclx" && kind != "nclc" {
				break
			}
			be := binary.BigEndian
			t.colorInfo = ColorInfo{
				Known:     true,
				Primaries: be.Uint16(kids[12:]),
				Transfer:  be.Uint16(kids[14:]),
				Matrix:    be.Uint16(kids[16:]),
			}
			if kind == "nclx" && n >= 19 {
				t.color = kids[:n:n]
			}
		case string(kids[4:8]) == "clap" && n == 40 && t.clap == nil:
			// Clean aperture: eight 32-bit values.
			t.clap = kids[:n:n]
		}
		kids = kids[n:]
	}
}

// forEachBox calls fn for each box read from r.
func forEachBox(r io.Reader, fn func(bmff.Box) error) error {
	br := bmff.NewReader(r)
	for {
		b, err := br.ReadBox()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("quicktime: %w", err)
		}
		if err := fn(b); err != nil {
			return err
		}
	}
}

// readLeaf returns the body of b, which must be at most maxLeafBoxSize.
func readLeaf(b bmff.Box) ([]byte, error) {
	n := b.Size() - b.HeaderSize()
	if b.Size() <= 0 || n > maxLeafBoxSize {
		return nil, fmt.Errorf("quicktime: unsupported %q box size %d", b.Type(), b.Size())
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(b.Body(), buf); err != nil {
		return nil, fmt.Errorf("quicktime: reading %q box: %w", b.Type(), err)
	}
	return buf, nil
}
