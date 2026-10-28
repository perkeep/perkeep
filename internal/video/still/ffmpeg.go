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

package still

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"perkeep.org/internal/media/quicktime"
)

// ffmpegPath returns the path of the ffmpeg program, or the empty string
// if it isn't installed. It's a variable for tests.
var ffmpegPath = sync.OnceValue(func() string {
	p, _ := exec.LookPath("ffmpeg")
	return p
})

// ffmpegGate limits how many ffmpeg processes run at once.
var ffmpegGate = make(chan struct{}, max(1, min(4, runtime.NumCPU()/2)))

const (
	// ffmpegTimeout is how long ffmpeg may run, including waiting for
	// ffmpegGate.
	ffmpegTimeout = 30 * time.Second

	// maxFFmpegOutput is the most output read from ffmpeg: a PPM image
	// of 8192x4096 pixels, and its header.
	maxFFmpegOutput = 8192*4096*3 + 64

	// maxStreamSide is the largest width and height of stills of videos
	// that ffmpeg reads whole, whose sizes aren't known beforehand.
	maxStreamSide = 1920
)

// ffmpegDemuxers are the ffmpeg formats (demuxers) of videos that ffmpeg
// reads whole, by MIME type.
var ffmpegDemuxers = map[string]string{
	"video/x-msvideo":  "avi", // with codecs other than Motion JPEG
	"video/vnd.avi":    "avi",
	"video/avi":        "avi",
	"video/msvideo":    "avi",
	"video/mpeg":       "mpeg",
	"video/x-ms-wmv":   "asf",
	"video/x-ms-asf":   "asf",
	"video/webm":       "matroska",
	"video/x-matroska": "matroska",
	"video/x-flv":      "flv",
}

// sniffDemuxer returns the ffmpeg format (demuxer) of videos that ffmpeg
// reads whole, from the start of the file r, or "" if it's not one. It's
// for files that aren't of the formats their MIME types say, as those
// indexed by their names, such as some cameras' WMV files named ".AVI".
func sniffDemuxer(r io.ReaderAt) string {
	var b [16]byte
	if n, _ := r.ReadAt(b[:], 0); n < len(b) {
		return ""
	}
	switch {
	case bytes.Equal(b[:], []byte{0x30, 0x26, 0xb2, 0x75, 0x8e, 0x66, 0xcf, 0x11, 0xa6, 0xd9, 0x00, 0xaa, 0x00, 0x62, 0xce, 0x6c}):
		return "asf"
	case bytes.HasPrefix(b[:], []byte{0x1a, 0x45, 0xdf, 0xa3}): // EBML
		return "matroska"
	case bytes.HasPrefix(b[:], []byte("FLV\x01")):
		return "flv"
	case bytes.HasPrefix(b[:], []byte{0, 0, 1, 0xba}): // MPEG program stream pack
		return "mpeg"
	case string(b[:4]) == "RIFF" && string(b[8:12]) == "AVI ":
		return "avi"
	}
	return ""
}

// runFFmpeg returns the first video frame that ffmpeg decodes from input,
// of the given ffmpeg format, applying the ffmpeg video filters vf, if
// any. Its format is always given, so that ffmpeg doesn't guess, and it
// may only read input, so that files like playlists can't make it open
// others.
func runFFmpeg(format string, input io.Reader, vf string) (*image.NRGBA, error) {
	prog := ffmpegPath()
	if prog == "" {
		return nil, ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), ffmpegTimeout)
	defer cancel()
	select {
	case ffmpegGate <- struct{}{}:
		defer func() { <-ffmpegGate }()
	case <-ctx.Done():
		return nil, errors.New("still: timeout waiting to run ffmpeg")
	}
	// Convert to RGB as the frame's color information (from its
	// bitstream or container) says: its range and matrix.
	vf = strings.TrimPrefix(vf+",format=rgb24", ",")
	cmd := exec.CommandContext(ctx, prog,
		"-hide_banner", "-loglevel", "error", "-nostats",
		"-protocol_whitelist", "pipe",
		"-f", format, "-i", "pipe:0",
		"-map", "0:v:0", "-frames:v", "1",
		"-vf", vf,
		"-f", "image2pipe", "-c:v", "ppm", "pipe:1")
	cmd.Stdin = input
	stdout := &cappedBuffer{max: maxFFmpegOutput}
	stderr := &cappedBuffer{max: 4 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("still: ffmpeg timed out")
		}
		if stdout.overflow {
			return nil, errors.New("still: ffmpeg's frame is too big")
		}
		// Corrupt videos, or of codecs or formats this ffmpeg lacks, or
		// that aren't what their MIME types say.
		return nil, fmt.Errorf("%w: ffmpeg: %v: %s", ErrUnsupported, err, bytes.TrimSpace(stderr.Bytes()))
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("%w: %s", errNoFrame, bytes.TrimSpace(stderr.Bytes()))
	}
	return decodePPM(stdout.Bytes())
}

// errNoFrame is returned by runFFmpeg when ffmpeg runs successfully but
// outputs no frame, as when its filters select none.
var errNoFrame = errors.New("still: ffmpeg decoded no frame")

// cappedBuffer is a bytes.Buffer that fails writes past max bytes.
type cappedBuffer struct {
	bytes.Buffer
	max      int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		b.overflow = true
		return 0, errors.New("too much output")
	}
	return b.Buffer.Write(p)
}

// decodePPM decodes a binary PPM image ("P6") of 8-bit samples, as ffmpeg
// writes them.
func decodePPM(b []byte) (*image.NRGBA, error) {
	// The magic number, width, height, and maximum value, separated by
	// whitespace, then a single whitespace character and the pixels.
	var fields [4]string
	rest := b
	for i := range fields {
		rest = bytes.TrimLeft(rest, " \t\r\n")
		end := bytes.IndexAny(rest, " \t\r\n")
		if end < 0 {
			return nil, errors.New("still: bad PPM header")
		}
		fields[i], rest = string(rest[:end]), rest[end+1:]
	}
	w, errw := strconv.Atoi(fields[1])
	h, errh := strconv.Atoi(fields[2])
	if fields[0] != "P6" || errw != nil || errh != nil || fields[3] != "255" ||
		w <= 0 || h <= 0 || w > 8192 || h > 8192 || len(rest) != w*h*3 {
		return nil, fmt.Errorf("still: bad PPM image %q %q %q %q of %d bytes", fields[0], fields[1], fields[2], fields[3], len(rest))
	}
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i, j := 0, 0; i < len(rest); i, j = i+3, j+4 {
		im.Pix[j], im.Pix[j+1], im.Pix[j+2], im.Pix[j+3] = rest[i], rest[i+1], rest[i+2], 0xff
	}
	return im, nil
}

// elementaryStream says how ffmpeg can decode a sample of a QuickTime or
// ISO BMFF video track on its own: as a raw stream of the given ffmpeg
// format, of header (the decoder configuration, if any) followed by the
// sample converted by sample.
type elementaryStream struct {
	format string
	header []byte
	sample func([]byte) ([]byte, error)
}

// trackStream returns how ffmpeg can decode samples of vt, or false if it
// can't.
func trackStream(vt *quicktime.VideoTrack) (elementaryStream, bool) {
	var config []byte // decoder configuration box contents
	if len(vt.Config) > 8 {
		config = vt.Config[8:]
	}
	raw := func(b []byte) ([]byte, error) { return b, nil }
	switch vt.Codec {
	case "avc1", "avc2", "avc3", "avc4":
		// H.264, with the decoder configuration's parameter sets ("avc1")
		// or with them in its samples ("avc3").
		header, lengthSize, err := avcConfigNALs(config)
		if err != nil {
			return elementaryStream{}, false
		}
		return elementaryStream{"h264", header, annexB(lengthSize)}, true
	case "hvc1", "hev1":
		header, lengthSize, err := hevcConfigNALs(config)
		if err != nil {
			return elementaryStream{}, false
		}
		return elementaryStream{"hevc", header, annexB(lengthSize)}, true
	case "mp4v":
		// MPEG-4 Part 2, whose configuration is its video object
		// sequence and video object layer headers.
		return elementaryStream{"m4v", esdsDecoderSpecificInfo(config), raw}, true
	case "s263", "h263":
		return elementaryStream{"h263", nil, raw}, true
	case "jpeg", "mjpa":
		return elementaryStream{"mjpeg", nil, raw}, true
	}
	return elementaryStream{}, false
}

// annexB returns a function converting a sample of NAL units preceded by
// their lengths, of lengthSize bytes, to an Annex B byte stream of them,
// each preceded by a start code.
func annexB(lengthSize int) func([]byte) ([]byte, error) {
	return func(b []byte) ([]byte, error) {
		out := make([]byte, 0, len(b)+16)
		for len(b) > 0 {
			if len(b) < lengthSize {
				return nil, errors.New("still: truncated NAL unit length")
			}
			var n uint64
			for _, c := range b[:lengthSize] {
				n = n<<8 | uint64(c)
			}
			b = b[lengthSize:]
			if n > uint64(len(b)) {
				return nil, errors.New("still: truncated NAL unit")
			}
			out = append(out, 0, 0, 0, 1)
			out = append(out, b[:n]...)
			b = b[n:]
		}
		return out, nil
	}
}

// avcConfigNALs returns the parameter sets of an AVC decoder configuration
// record ("avcC" contents) as an Annex B byte stream, and the size of its
// samples' NAL unit lengths.
func avcConfigNALs(c []byte) (header []byte, lengthSize int, err error) {
	if len(c) < 6 {
		return nil, 0, errors.New("still: short avcC")
	}
	lengthSize = int(c[4]&3) + 1
	p := c[5:]
	// Sequence parameter sets, then picture parameter sets.
	for _, countMask := range []byte{0x1f, 0xff} {
		if len(p) < 1 {
			return nil, 0, errors.New("still: short avcC")
		}
		n := int(p[0] & countMask)
		p = p[1:]
		for range n {
			if len(p) < 2 {
				return nil, 0, errors.New("still: short avcC")
			}
			size := int(binary.BigEndian.Uint16(p))
			if len(p) < 2+size {
				return nil, 0, errors.New("still: short avcC")
			}
			header = append(header, 0, 0, 0, 1)
			header = append(header, p[2:2+size]...)
			p = p[2+size:]
		}
	}
	return header, lengthSize, nil
}

// hevcConfigNALs is avcConfigNALs for HEVC decoder configuration records
// ("hvcC" contents), whose NAL units are in arrays by type.
func hevcConfigNALs(c []byte) (header []byte, lengthSize int, err error) {
	if len(c) < 23 {
		return nil, 0, errors.New("still: short hvcC")
	}
	lengthSize = int(c[21]&3) + 1
	p := c[23:]
	for range c[22] {
		if len(p) < 3 {
			return nil, 0, errors.New("still: short hvcC")
		}
		n := int(binary.BigEndian.Uint16(p[1:]))
		p = p[3:]
		for range n {
			if len(p) < 2 {
				return nil, 0, errors.New("still: short hvcC")
			}
			size := int(binary.BigEndian.Uint16(p))
			if len(p) < 2+size {
				return nil, 0, errors.New("still: short hvcC")
			}
			header = append(header, 0, 0, 0, 1)
			header = append(header, p[2:2+size]...)
			p = p[2+size:]
		}
	}
	return header, lengthSize, nil
}

// esdsDecoderSpecificInfo returns the decoder specific information of an
// elementary stream descriptor box's contents ("esds", ISO/IEC 14496-1),
// or nil if it has none.
func esdsDecoderSpecificInfo(b []byte) []byte {
	if len(b) < 4 {
		return nil
	}
	b = b[4:] // FullBox version and flags
	// descriptor returns the first descriptor of b: its tag, contents,
	// and the rest of b.
	descriptor := func(b []byte) (tag byte, body, rest []byte, ok bool) {
		if len(b) < 2 {
			return 0, nil, nil, false
		}
		tag, b = b[0], b[1:]
		// The size is in up to 4 bytes of 7 bits each, with the high
		// bit set on all but the last.
		var size int
		for i := 0; ; i++ {
			if i == 4 || len(b) == 0 {
				return 0, nil, nil, false
			}
			c := b[0]
			b = b[1:]
			size = size<<7 | int(c&0x7f)
			if c&0x80 == 0 {
				break
			}
		}
		if size > len(b) {
			return 0, nil, nil, false
		}
		return tag, b[:size], b[size:], true
	}
	const (
		esDescrTag            = 3
		decoderConfigDescrTag = 4
		decSpecificInfoTag    = 5
	)
	tag, es, _, ok := descriptor(b)
	if !ok || tag != esDescrTag || len(es) < 3 {
		return nil
	}
	// ES_ID, then flags, which say which optional fields follow.
	flags := es[2]
	es = es[3:]
	if flags&0x80 != 0 { // streamDependenceFlag
		es = es[min(2, len(es)):]
	}
	if flags&0x40 != 0 { // URL_Flag
		if len(es) == 0 {
			return nil
		}
		es = es[min(1+int(es[0]), len(es)):]
	}
	if flags&0x20 != 0 { // OCRstreamFlag
		es = es[min(2, len(es)):]
	}
	for len(es) > 0 {
		tag, dc, rest, ok := descriptor(es)
		if !ok {
			return nil
		}
		es = rest
		if tag != decoderConfigDescrTag || len(dc) < 13 {
			continue
		}
		// Object type, stream type, buffer size, and bit rates, then
		// descriptors.
		for dc = dc[13:]; len(dc) > 0; {
			tag, info, rest, ok := descriptor(dc)
			if !ok {
				return nil
			}
			if tag == decSpecificInfoTag {
				return info
			}
			dc = rest
		}
	}
	return nil
}

// trackFilters returns the ffmpeg video filters that crop frames of vt
// to its clean aperture and rotate them for display, or "".
func trackFilters(vt *quicktime.VideoTrack) string {
	var filters []string
	w, h := vt.DisplaySize()
	if vt.Rotation == 90 || vt.Rotation == 270 {
		w, h = h, w
	}
	if w != vt.Width || h != vt.Height {
		filters = append(filters, fmt.Sprintf("crop=%d:%d", w, h))
	}
	switch vt.Rotation {
	case 90:
		filters = append(filters, "transpose=clock")
	case 180:
		filters = append(filters, "hflip,vflip")
	case 270:
		filters = append(filters, "transpose=cclock")
	}
	return strings.Join(filters, ",")
}
