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

// Package avi reads the headers of AVI (RIFF) video files, as written by
// older digital cameras.
package avi // import "perkeep.org/internal/media/avi"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// Header is what ReadHeader reads of an AVI file.
type Header struct {
	// Width and Height are the video's frame size, or zero if unknown.
	Width, Height int

	// FrameDuration is the duration of each video frame, or zero if
	// unknown.
	FrameDuration time.Duration

	// Frames is the number of video frames in the main header.
	Frames int

	// Codec is the four-character code of the first video stream's
	// compression, e.g. "MJPG", or empty if there's no video stream.
	Codec string

	// VideoStream is the stream number of the first video stream, which
	// prefixes its chunk IDs (e.g. "00dc" for stream 0), or -1 if
	// there's no video stream.
	VideoStream int
}

// Duration returns the video's duration, or zero if unknown.
func (h *Header) Duration() time.Duration {
	if h.FrameDuration <= 0 || h.Frames <= 0 || int64(h.Frames) > math.MaxInt64/int64(h.FrameDuration) {
		return 0
	}
	return time.Duration(h.Frames) * h.FrameDuration
}

// ErrNotAVI is returned by ReadHeader for files that aren't AVI files.
var ErrNotAVI = errors.New("avi: not an AVI file")

// maxHeaderSize is the largest header list that ReadHeader reads. They're
// typically a few kilobytes.
const maxHeaderSize = 1 << 20

// ReadHeader reads the header of the AVI file r of the given size.
func ReadHeader(r io.ReaderAt, size int64) (*Header, error) {
	var head [12]byte
	if _, err := r.ReadAt(head[:], 0); err != nil {
		return nil, ErrNotAVI
	}
	if string(head[0:4]) != "RIFF" || string(head[8:12]) != "AVI " {
		return nil, ErrNotAVI
	}
	// The header list ("LIST" ... "hdrl") is the first chunk.
	var list [12]byte
	if _, err := r.ReadAt(list[:], 12); err != nil {
		return nil, fmt.Errorf("avi: reading header list: %w", err)
	}
	if string(list[0:4]) != "LIST" || string(list[8:12]) != "hdrl" {
		return nil, errors.New("avi: missing header list")
	}
	n := int64(binary.LittleEndian.Uint32(list[4:8])) - 4
	if n < 0 || n > maxHeaderSize || 24+n > size {
		return nil, fmt.Errorf("avi: bad header list size %d", n)
	}
	hdrl := make([]byte, n)
	if _, err := r.ReadAt(hdrl, 24); err != nil {
		return nil, fmt.Errorf("avi: reading header list: %w", err)
	}
	h := &Header{VideoStream: -1}
	stream := 0 // number of the next stream list
	err := forEachChunk(hdrl, func(id string, data []byte) error {
		switch id {
		case "avih":
			h.parseMainHeader(data)
		case "LIST":
			if len(data) >= 4 && string(data[:4]) == "strl" {
				if h.VideoStream < 0 && h.parseStreamList(data[4:]) {
					h.VideoStream = stream
				}
				stream++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return h, nil
}

// parseMainHeader parses an "avih" chunk, an AVIMAINHEADER.
func (h *Header) parseMainHeader(b []byte) {
	if len(b) < 40 {
		return
	}
	le := binary.LittleEndian
	h.FrameDuration = time.Duration(le.Uint32(b[0:])) * time.Microsecond
	h.Frames = int(le.Uint32(b[16:]))
	h.Width = int(le.Uint32(b[32:]))
	h.Height = int(le.Uint32(b[36:]))
}

// parseStreamList parses the chunks of a stream list ("strl"), reporting
// whether it's a video stream and if so, setting h.Codec.
func (h *Header) parseStreamList(b []byte) (video bool) {
	forEachChunk(b, func(id string, data []byte) error {
		switch id {
		case "strh":
			// AVISTREAMHEADER: type, then handler.
			if len(data) >= 8 && string(data[0:4]) == "vids" {
				video = true
				h.Codec = string(data[4:8])
			}
		case "strf":
			// BITMAPINFOHEADER: its compression is more reliable
			// than the stream header's handler.
			if video && len(data) >= 20 {
				if c := string(data[16:20]); c != "\x00\x00\x00\x00" {
					h.Codec = c
				}
			}
		}
		return nil
	})
	return video
}

// forEachChunk calls fn for each RIFF chunk in b, with its ID and data.
func forEachChunk(b []byte, fn func(id string, data []byte) error) error {
	for len(b) >= 8 {
		id := string(b[0:4])
		n := binary.LittleEndian.Uint32(b[4:8])
		b = b[8:]
		if uint64(n) > uint64(len(b)) {
			return fmt.Errorf("avi: chunk %q of %d bytes exceeds its list", id, n)
		}
		if err := fn(id, b[:n]); err != nil {
			return err
		}
		b = b[n:]
		if n%2 == 1 && len(b) > 0 { // chunks are padded to even sizes
			b = b[1:]
		}
	}
	return nil
}
