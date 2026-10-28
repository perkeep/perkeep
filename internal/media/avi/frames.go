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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frame is the location of a video frame's data in an AVI file.
type Frame struct {
	Offset int64
	Size   int64
}

// maxFrames is the most frames that Frames returns.
const maxFrames = 1 << 20

// maxIndexSize is the largest "idx1" index that Frames reads, with 16
// bytes per chunk.
const maxIndexSize = 16 * 4 * maxFrames

// Frames returns the frames of the video stream videoStream (see
// Header.VideoStream) of the AVI file r of the given size, in order. It
// uses the file's index if it has one, and otherwise scans its movie
// data list for the frames' chunks.
func Frames(r io.ReaderAt, size int64, videoStream int) ([]Frame, error) {
	if videoStream < 0 || videoStream > 99 {
		return nil, fmt.Errorf("avi: bad video stream %d", videoStream)
	}
	movi, moviEnd, idx1, idx1Size, err := findMovieData(r, size)
	if err != nil {
		return nil, err
	}
	prefix := fmt.Sprintf("%02d", videoStream)
	isFrame := func(id []byte) bool {
		return string(id[0:2]) == prefix && (string(id[2:4]) == "dc" || string(id[2:4]) == "db")
	}
	if idx1 >= 0 {
		if frames, err := indexFrames(r, size, movi, idx1, idx1Size, isFrame); err == nil {
			return frames, nil
		}
		// Fall back to scanning.
	}
	return scanFrames(r, movi+4, moviEnd, isFrame)
}

// findMovieData returns the offsets of the movie data list's type
// ("movi") and its end, and of the index's data and its size, or -1 if
// there's no index.
func findMovieData(r io.ReaderAt, size int64) (movi, moviEnd, idx1, idx1Size int64, err error) {
	var head [12]byte
	if _, err := r.ReadAt(head[:], 0); err != nil || string(head[0:4]) != "RIFF" || string(head[8:12]) != "AVI " {
		return 0, 0, 0, 0, ErrNotAVI
	}
	end := min(size, 8+int64(binary.LittleEndian.Uint32(head[4:8])))
	movi, idx1 = -1, -1
	for off := int64(12); off+8 <= end; {
		var ch [12]byte
		n, _ := r.ReadAt(ch[:], off)
		if n < 8 {
			break
		}
		cs := int64(binary.LittleEndian.Uint32(ch[4:8]))
		switch {
		case string(ch[0:4]) == "LIST" && n == 12 && string(ch[8:12]) == "movi":
			movi, moviEnd = off+8, min(end, off+8+cs)
		case string(ch[0:4]) == "idx1":
			idx1, idx1Size = off+8, cs
		}
		off += 8 + cs + cs&1
	}
	if movi < 0 {
		return 0, 0, 0, 0, errors.New("avi: no movie data list")
	}
	return movi, moviEnd, idx1, idx1Size, nil
}

// indexFrames returns the frames listed in an "idx1" index of idx1Size
// bytes at offset idx1. Each entry's offset is of its chunk's header,
// relative to the movie data list's type ("movi") at offset movi, or in
// some files, to the start of the file.
func indexFrames(r io.ReaderAt, size, movi, idx1, idx1Size int64, isFrame func([]byte) bool) ([]Frame, error) {
	if idx1Size > maxIndexSize || idx1+idx1Size > size {
		return nil, errors.New("avi: bad index size")
	}
	idx := make([]byte, idx1Size)
	if _, err := r.ReadAt(idx, idx1); err != nil {
		return nil, err
	}
	le := binary.LittleEndian
	var frames []Frame
	base := int64(-1)
	for e := idx; len(e) >= 16; e = e[16:] {
		if !isFrame(e[0:4]) {
			continue
		}
		off, n := int64(le.Uint32(e[8:])), int64(le.Uint32(e[12:]))
		if base < 0 {
			// Find which base puts the entry's chunk ID there.
			for _, b := range []int64{movi, 0} {
				var id [4]byte
				if _, err := r.ReadAt(id[:], b+off); err == nil && string(id[:]) == string(e[0:4]) {
					base = b
					break
				}
			}
			if base < 0 {
				return nil, errors.New("avi: index offsets don't match chunks")
			}
		}
		data := base + off + 8 // after the chunk's header
		if data < 0 || data+n > size {
			return nil, errors.New("avi: index entry out of range")
		}
		if len(frames) >= maxFrames {
			break
		}
		frames = append(frames, Frame{Offset: data, Size: n})
	}
	if len(frames) == 0 {
		return nil, errors.New("avi: no frames in index")
	}
	return frames, nil
}

// scanFrames returns the frames in the chunks between offsets start and
// end of the movie data list, descending into "rec " lists.
func scanFrames(r io.ReaderAt, start, end int64, isFrame func([]byte) bool) ([]Frame, error) {
	var frames []Frame
	for off := start; off+8 <= end && len(frames) < maxFrames; {
		var ch [12]byte
		n, _ := r.ReadAt(ch[:], off)
		if n < 8 {
			break
		}
		cs := int64(binary.LittleEndian.Uint32(ch[4:8]))
		if string(ch[0:4]) == "LIST" {
			off += 12 // into the list's chunks
			continue
		}
		if isFrame(ch[0:4]) && off+8+cs <= end {
			frames = append(frames, Frame{Offset: off + 8, Size: cs})
		}
		off += 8 + cs + cs&1
	}
	if len(frames) == 0 {
		return nil, errors.New("avi: no video frames")
	}
	return frames, nil
}
