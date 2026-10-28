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
	"time"

	"go4.org/media/heif/bmff"
)

// maxTableBoxSize is the largest sample table box that is read. A sample
// size table has 4 bytes per frame, so this allows 4 million frames, over
// a day at 30 frames per second.
const maxTableBoxSize = 16 << 20

// sampleTables are the bodies of a track's sample table boxes, each nil
// if absent.
type sampleTables struct {
	stts       []byte // decoding time to sample
	ctts       []byte // composition (presentation) time offsets; optional
	stss       []byte // sync samples; if absent, all samples are
	stsz, stz2 []byte // sample sizes, in one of two forms
	stsc       []byte // sample to chunk
	stco, co64 []byte // chunk offsets, 32 or 64 bit
}

// box returns a pointer to the field for a sample table box of type typ,
// or nil if it isn't one.
func (st *sampleTables) box(typ string) *[]byte {
	switch typ {
	case "stts":
		return &st.stts
	case "ctts":
		return &st.ctts
	case "stss":
		return &st.stss
	case "stsz":
		return &st.stsz
	case "stz2":
		return &st.stz2
	case "stsc":
		return &st.stsc
	case "stco":
		return &st.stco
	case "co64":
		return &st.co64
	}
	return nil
}

// Sample is a sample (a coded frame) of a video track.
type Sample struct {
	// Time is when the sample is presented, from the start of the
	// track's presentation per its edit list. Decoders may decode
	// samples in a different order.
	Time time.Duration

	Offset int64 // in the file
	Size   int64

	// NextOffset and NextSize are those of the next sample in decoding
	// order, or zero if it's the last. Decoders of interlaced video,
	// whose samples are each one field, need it for a whole frame.
	NextOffset, NextSize int64
}

// VideoTrack is a movie's main video track, as read by ReadVideoTrack.
type VideoTrack struct {
	// Codec is the format of the track's first sample entry, such as
	// "hvc1" or "hev1" for HEVC, or "avc1" for H.264.
	Codec string

	// Width and Height are the coded frames' size in pixels, before
	// Rotation.
	Width, Height int

	// Rotation is how many degrees clockwise the track is rotated for
	// display: 0, 90, 180, or 270.
	Rotation int

	// Config is the first sample entry's decoder configuration box
	// (such as "hvcC", "avcC", or for MPEG-4 Part 2 video, "esds"),
	// including its header, or nil if it has none.
	Config []byte

	// Color is the first sample entry's ISO color information box
	// ("colr" of type "nclx"), including its header, or nil if it has
	// none. Unlike the bitstream, it says whether the video uses the full
	// range of sample values, which HEIF decoders read from it.
	Color []byte

	// ColorInfo is the first sample entry's color information, from an
	// ISO ("nclx") or QuickTime ("nclc") box, which says how to convert
	// its frames to RGB for display.
	ColorInfo ColorInfo

	// CleanAperture is the first sample entry's clean aperture box
	// ("clap"), including its header, or nil if it has none. It crops
	// the coded frames for display, as in iPhone Live Photo videos.
	CleanAperture []byte

	// Duration is the track's duration, or zero if unknown.
	Duration time.Duration

	// SyncSamples are the track's sync samples, which can be decoded
	// without other samples, in decoding order.
	SyncSamples []Sample
}

// DisplaySize returns the size of the track's frames as displayed: cropped
// to their clean aperture, if any, and rotated.
func (vt *VideoTrack) DisplaySize() (width, height int) {
	width, height = vt.Width, vt.Height
	if c := vt.CleanAperture; len(c) == 40 {
		// After the box header, the width and height as rationals.
		be := binary.BigEndian
		ratio := func(off int) int {
			n, d := be.Uint32(c[off:]), be.Uint32(c[off+4:])
			if d == 0 {
				return 0
			}
			return int((uint64(n) + uint64(d)/2) / uint64(d))
		}
		if w, h := ratio(8), ratio(16); w > 0 && h > 0 && w <= width && h <= height {
			width, height = w, h
		}
	}
	if vt.Rotation == 90 || vt.Rotation == 270 {
		width, height = height, width
	}
	return width, height
}

// ColorInfo is a video's color information, with the code points of ITU-T
// H.273 (as in HEVC and H.264 video usability information).
type ColorInfo struct {
	Known     bool   // whether the video has color information
	Primaries uint16 // e.g. 1 for BT.709, 9 for BT.2020, 12 for Display P3
	Transfer  uint16 // e.g. 1 for BT.709, 16 for PQ, 18 for HLG
	Matrix    uint16 // e.g. 1 for BT.709, 6 for BT.601, 9 for BT.2020
}

// Color code points of ITU-T H.273.
const (
	PrimariesBT709     = 1
	PrimariesBT2020    = 9
	PrimariesDisplayP3 = 12 // SMPTE EG 432-1

	TransferPQ  = 16 // SMPTE ST 2084
	TransferHLG = 18 // ARIB STD-B67
)

// ErrNoVideo is returned by ReadVideoTrack for movies without an enabled
// video track.
var ErrNoVideo = errors.New("quicktime: no video track")

// ReadVideoTrack reads the main video track of the QuickTime or MP4 file
// r of the given size: its enabled video track with the largest size.
// It reads the movie's metadata, but not its media data.
func ReadVideoTrack(r io.ReaderAt, size int64) (*VideoTrack, error) {
	moov, err := findMovie(r, size)
	if err != nil {
		return nil, err
	}
	var tracks []*track
	err = forEachBox(moov, func(b bmff.Box) error {
		if !b.Type().EqualString("trak") {
			return nil
		}
		t, err := readTrack(b.Body(), true)
		if err != nil {
			return err
		}
		tracks = append(tracks, t)
		return nil
	})
	if err != nil {
		return nil, err
	}
	t := mainVideoTrack(tracks)
	if t == nil {
		return nil, ErrNoVideo
	}
	samples, err := t.tables.syncSamples(t.timescale, t.mediaStart)
	if err != nil {
		return nil, err
	}
	vt := &VideoTrack{
		Codec:         t.codec,
		Width:         t.entryWidth,
		Height:        t.entryHeight,
		Rotation:      t.rotation,
		Config:        t.config,
		Color:         t.color,
		ColorInfo:     t.colorInfo,
		CleanAperture: t.clap,
		Duration:      mediaDuration(t.duration, t.timescale),
		SyncSamples:   samples,
	}
	return vt, nil
}

// mediaDuration returns d units of a timescale as a time.Duration, or
// zero if it's unknown or out of range.
func mediaDuration(d uint64, timescale uint32) time.Duration {
	if timescale == 0 {
		return 0
	}
	secs := float64(d) / float64(timescale)
	if secs >= math.MaxInt64/float64(time.Second) {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

// tableEntries returns the entries of a sample table FullBox body that
// has a 32-bit entry count after its version and flags, and entries of
// entrySize bytes.
func tableEntries(b []byte, name string, entrySize int) ([]byte, uint32, error) {
	if len(b) < 8 {
		return nil, 0, fmt.Errorf("quicktime: short %s box", name)
	}
	n := binary.BigEndian.Uint32(b[4:8])
	if uint64(n)*uint64(entrySize) > uint64(len(b)-8) {
		return nil, 0, fmt.Errorf("quicktime: %s box has %d entries but only %d bytes", name, n, len(b)-8)
	}
	return b[8:], n, nil
}

// sampleSizes returns the sizes of all of a track's samples.
func (st *sampleTables) sampleSizes() ([]uint32, error) {
	be := binary.BigEndian
	switch {
	case st.stsz != nil:
		b := st.stsz
		if len(b) < 12 {
			return nil, errors.New("quicktime: short stsz box")
		}
		uniform, n := be.Uint32(b[4:]), be.Uint32(b[8:])
		if n > maxTableBoxSize {
			return nil, fmt.Errorf("quicktime: too many samples (%d)", n)
		}
		sizes := make([]uint32, n)
		if uniform != 0 {
			for i := range sizes {
				sizes[i] = uniform
			}
			return sizes, nil
		}
		if uint64(n)*4 > uint64(len(b)-12) {
			return nil, errors.New("quicktime: short stsz box")
		}
		for i := range sizes {
			sizes[i] = be.Uint32(b[12+4*i:])
		}
		return sizes, nil
	case st.stz2 != nil:
		b := st.stz2
		if len(b) < 12 {
			return nil, errors.New("quicktime: short stz2 box")
		}
		field, n := b[7], be.Uint32(b[8:])
		if field != 4 && field != 8 && field != 16 {
			return nil, fmt.Errorf("quicktime: bad stz2 field size %d", field)
		}
		if uint64(n)*uint64(field) > 8*uint64(len(b)-12) {
			return nil, errors.New("quicktime: short stz2 box")
		}
		sizes := make([]uint32, n)
		for i := range sizes {
			switch field {
			case 4:
				v := b[12+i/2]
				if i%2 == 0 {
					v >>= 4
				}
				sizes[i] = uint32(v & 15)
			case 8:
				sizes[i] = uint32(b[12+i])
			case 16:
				sizes[i] = uint32(be.Uint16(b[12+2*i:]))
			}
		}
		return sizes, nil
	}
	return nil, errors.New("quicktime: no sample size table")
}

// chunkOffsets returns the file offsets of all of a track's chunks.
func (st *sampleTables) chunkOffsets() ([]int64, error) {
	be := binary.BigEndian
	if st.co64 != nil {
		b, n, err := tableEntries(st.co64, "co64", 8)
		if err != nil {
			return nil, err
		}
		offs := make([]int64, n)
		for i := range offs {
			v := be.Uint64(b[8*i:])
			if v > math.MaxInt64 {
				return nil, errors.New("quicktime: bad chunk offset")
			}
			offs[i] = int64(v)
		}
		return offs, nil
	}
	if st.stco == nil {
		return nil, errors.New("quicktime: no chunk offset table")
	}
	b, n, err := tableEntries(st.stco, "stco", 4)
	if err != nil {
		return nil, err
	}
	offs := make([]int64, n)
	for i := range offs {
		offs[i] = int64(be.Uint32(b[4*i:]))
	}
	return offs, nil
}

// compositionOffsets are the runs of a composition offset table ("ctts"):
// how much later than its decoding time each sample is presented.
type compositionOffsets struct {
	counts  []uint32
	offsets []int64
	// next is the run of the sample last asked about, and start that
	// run's first sample, since samples are asked about in order.
	next  int
	start int64
}

// compositionOffsets returns the track's composition offsets, which are
// all zero if it has no table.
func (st *sampleTables) compositionOffsets() (*compositionOffsets, error) {
	co := new(compositionOffsets)
	if st.ctts == nil {
		return co, nil
	}
	b, n, err := tableEntries(st.ctts, "ctts", 8)
	if err != nil {
		return nil, err
	}
	be := binary.BigEndian
	for i := range n {
		co.counts = append(co.counts, be.Uint32(b[8*i:]))
		v := int64(be.Uint32(b[8*i+4:]))
		if st.ctts[0] == 1 { // version 1 offsets are signed
			v = int64(int32(v))
		}
		co.offsets = append(co.offsets, v)
	}
	return co, nil
}

// at returns the composition offset of the 0-based sample, which must not
// be before that of the previous call.
func (co *compositionOffsets) at(sample int) int64 {
	for co.next < len(co.counts) {
		if int64(sample) < co.start+int64(co.counts[co.next]) {
			return co.offsets[co.next]
		}
		co.start += int64(co.counts[co.next])
		co.next++
	}
	return 0
}

// syncSamples returns the track's sync samples, with times in units of
// timescale. mediaStart is the media time at which the track's edit list
// starts presenting it, which is when its first frame is shown.
// syncSampleIndexes returns the 0-based indexes of the sync samples in
// st's sync sample table, of a track of n samples.
func (st *sampleTables) syncSampleIndexes(n int) ([]uint32, error) {
	b, count, err := tableEntries(st.stss, "stss", 4)
	if err != nil {
		return nil, err
	}
	var sync []uint32
	for i := range count {
		num := binary.BigEndian.Uint32(b[4*i:]) // 1-based
		if num == 0 || num > uint32(n) {
			return nil, fmt.Errorf("quicktime: bad sync sample number %d", num)
		}
		if len(sync) > 0 && num-1 <= sync[len(sync)-1] {
			return nil, errors.New("quicktime: sync samples out of order")
		}
		sync = append(sync, num-1)
	}
	return sync, nil
}

func (st *sampleTables) syncSamples(timescale uint32, mediaStart int64) ([]Sample, error) {
	be := binary.BigEndian
	if timescale == 0 {
		return nil, errors.New("quicktime: track has no timescale")
	}
	sizes, err := st.sampleSizes()
	if err != nil {
		return nil, err
	}
	chunks, err := st.chunkOffsets()
	if err != nil {
		return nil, err
	}

	// Which samples are sync samples, by 0-based index.
	var sync []uint32
	if st.stss != nil {
		sync, err = st.syncSampleIndexes(len(sizes))
		if err != nil {
			// As in some old phones' 3GPP videos. Their first
			// samples are sync samples, as video's must be.
			sync = []uint32{0}
		}
	} else {
		sync = make([]uint32, len(sizes))
		for i := range sync {
			sync[i] = uint32(i)
		}
	}

	// Each sample's offset: chunks hold consecutive samples, as many as
	// their sample-to-chunk run says.
	offsets := make([]int64, len(sizes))
	if st.stsc == nil {
		return nil, errors.New("quicktime: no sample-to-chunk table")
	}
	runs, nruns, err := tableEntries(st.stsc, "stsc", 12)
	if err != nil {
		return nil, err
	}
	sample := 0
	for r := range nruns {
		first := be.Uint32(runs[12*r:])      // 1-based chunk number
		perChunk := be.Uint32(runs[12*r+4:]) // samples per chunk
		last := uint32(len(chunks))          // through the last chunk...
		if r+1 < nruns {
			last = be.Uint32(runs[12*(r+1):]) - 1 // ...or the next run's first
		}
		if first == 0 || last > uint32(len(chunks)) || first > last+1 {
			return nil, errors.New("quicktime: bad sample-to-chunk table")
		}
		for c := first; c <= last && sample < len(sizes); c++ {
			off := chunks[c-1]
			for range perChunk {
				if sample >= len(sizes) {
					break
				}
				offsets[sample] = off
				off += int64(sizes[sample])
				sample++
			}
		}
	}
	if sample < len(sizes) {
		return nil, errors.New("quicktime: sample-to-chunk table covers too few samples")
	}

	// Each sample's decoding time.
	if st.stts == nil {
		return nil, errors.New("quicktime: no time-to-sample table")
	}
	deltas, ndeltas, err := tableEntries(st.stts, "stts", 8)
	if err != nil {
		return nil, err
	}
	compOffsets, err := st.compositionOffsets()
	if err != nil {
		return nil, err
	}
	out := make([]Sample, 0, len(sync))
	var t uint64 // in timescale units
	sample = 0
	next := 0 // index into sync
	for d := range ndeltas {
		count, delta := be.Uint32(deltas[8*d:]), uint64(be.Uint32(deltas[8*d+4:]))
		for range count {
			if next >= len(sync) || sample >= len(sizes) {
				return out, nil
			}
			if uint32(sample) == sync[next] {
				// The presentation time, from the decoding time.
				pt := int64(min(t, math.MaxInt64/2)) + compOffsets.at(sample) - mediaStart
				s := Sample{
					Time:   mediaDuration(uint64(max(pt, 0)), timescale),
					Offset: offsets[sample],
					Size:   int64(sizes[sample]),
				}
				if sample+1 < len(sizes) {
					s.NextOffset, s.NextSize = offsets[sample+1], int64(sizes[sample+1])
				}
				out = append(out, s)
				next++
			}
			t += delta
			sample++
		}
	}
	return out, nil
}
