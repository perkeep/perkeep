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

// Package still decodes still images of videos, for thumbnails. In
// process, it decodes HEVC keyframes of QuickTime, MP4, and 3GPP videos
// with the HEIF decoder of github.com/gen2brain/heic, and Motion JPEG
// frames of AVI videos.
//
// If ffmpeg is installed, it also decodes keyframes of other codecs in
// QuickTime, MP4, and 3GPP videos (H.264, H.263, MPEG-4 Part 2, and
// Motion JPEG) with it, giving it just the keyframe, and videos of other
// formats, such as WMV and WebM, which it reads whole.
package still // import "perkeep.org/internal/video/still"

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"strings"
	"time"

	"github.com/gen2brain/heic"
	"go4.org/media/heif"
	"perkeep.org/internal/media/avi"
	"perkeep.org/internal/media/quicktime"
)

// ErrUnsupported is returned by Open for videos of unsupported formats
// or codecs.
var ErrUnsupported = errors.New("still: unsupported video")

// maxFrameSize is the largest coded frame that is read.
const maxFrameSize = 64 << 20

// tries is how many frames Decode decodes at most, looking for one that
// isn't nearly black or uniform.
const tries = 3

// Video is a video opened by Open.
type Video struct {
	// Width and Height are the size of the decoded still, after any
	// rotation, or for videos that ffmpeg reads whole, its maximum.
	Width, Height int

	r        io.ReaderAt
	duration time.Duration
	frames   []frame // candidates, in order
	decode   func(data []byte) (image.Image, error)
	color    *colorConversion // for decoded frames, or nil

	// decodeAt, if non-nil, replaces decodeFrame's reading and decoding
	// of frames: for tests, and for videos that ffmpeg decodes.
	decodeAt func(frame) (image.Image, error)
}

// frame is a frame that can be decoded on its own.
type frame struct {
	time   time.Duration
	offset int64
	size   int64

	// nextOffset and nextSize are of the frame's next sample, if
	// known, for interlaced video whose samples are fields.
	nextOffset, nextSize int64
}

// Supported reports whether Open supports videos of mimeType, at least
// for some codecs.
func Supported(mimeType string) bool {
	if isQuickTime(mimeType) || isAVI(mimeType) {
		return true
	}
	_, ok := ffmpegDemuxers[mimeType]
	return ok && ffmpegPath() != ""
}

func isQuickTime(mimeType string) bool {
	switch mimeType {
	case "video/quicktime", "video/mp4", "video/3gpp", "video/3gpp2":
		return true
	}
	return false
}

func isAVI(mimeType string) bool {
	switch mimeType {
	case "video/x-msvideo", "video/vnd.avi", "video/avi", "video/msvideo":
		return true
	}
	return false
}

// Open reads the metadata of the video r of the given size and MIME type.
// It returns an error wrapping ErrUnsupported if it can't decode stills
// of the video, including if r isn't the kind of file its MIME type says.
func Open(r io.ReaderAt, size int64, mimeType string) (*Video, error) {
	var v *Video
	var err error
	switch {
	case isQuickTime(mimeType):
		v, err = openQuickTime(r, size)
	case isAVI(mimeType):
		v, err = openAVI(r, size)
	case ffmpegDemuxers[mimeType] != "":
		v, err = openStream(r, size, ffmpegDemuxers[mimeType])
	default:
		return nil, ErrUnsupported
	}
	if err != nil && ffmpegPath() != "" {
		if format := sniffDemuxer(r); format != "" && format != ffmpegDemuxers[mimeType] {
			return openStream(r, size, format)
		}
	}
	if err != nil && !errors.Is(err, ErrUnsupported) {
		// Files of video MIME types that can't be parsed, such as
		// WMV files named ".avi", or partial or corrupt files.
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	return v, err
}

func openQuickTime(r io.ReaderAt, size int64) (*Video, error) {
	vt, err := quicktime.ReadVideoTrack(r, size)
	if errors.Is(err, quicktime.ErrNoVideo) || errors.Is(err, quicktime.ErrNoMovie) {
		return nil, ErrUnsupported
	}
	if err != nil {
		return nil, err
	}
	if vt.Width <= 0 || vt.Height <= 0 {
		return nil, ErrUnsupported
	}
	// The decoders convert the frames' Y'CbCr, but not from HDR or wide
	// color primaries, which is done here.
	v := &Video{r: r, duration: vt.Duration, color: newColorConversion(vt.ColorInfo)}
	v.Width, v.Height = vt.DisplaySize()
	for _, s := range vt.SyncSamples {
		v.frames = append(v.frames, frame{time: s.Time, offset: s.Offset, size: s.Size, nextOffset: s.NextOffset, nextSize: s.NextSize})
	}
	// HEVC with its parameter sets in its decoder configuration, as
	// HEIF decoders require: "hvc1", but not "hev1".
	if vt.Codec != "hvc1" || vt.Config == nil {
		es, ok := trackStream(vt)
		if !ok || ffmpegPath() == "" {
			return nil, ErrUnsupported
		}
		filters := trackFilters(vt)
		v.decodeAt = func(f frame) (image.Image, error) {
			input := es.header[:len(es.header):len(es.header)]
			add := func(offset, size int64) error {
				data, err := v.read(offset, size)
				if err != nil {
					return err
				}
				sample, err := es.sample(data)
				input = append(input, sample...)
				return err
			}
			if err := add(f.offset, f.size); err != nil {
				return nil, err
			}
			im, err := runFFmpeg(es.format, bytes.NewReader(input), filters)
			if errors.Is(err, errNoFrame) && f.nextSize > 0 {
				// Interlaced video, whose keyframe is just its first
				// field.
				if err := add(f.nextOffset, f.nextSize); err != nil {
					return nil, err
				}
				im, err = runFFmpeg(es.format, bytes.NewReader(input), filters)
			}
			return im, err
		}
		return v, nil
	}
	props := []heif.Property{
		{Box: vt.Config, Essential: true},
		heif.SpatialExtents(uint32(vt.Width), uint32(vt.Height)),
	}
	if vt.Color != nil {
		// For its full range flag; iPhone Live Photo videos are full
		// range, for instance.
		props = append(props, heif.Property{Box: vt.Color})
	}
	// Transformations, in the order HEIF applies them: crop, then rotate.
	if vt.CleanAperture != nil {
		props = append(props, heif.Property{Box: vt.CleanAperture, Essential: true})
	}
	if vt.Rotation != 0 {
		// HEIF rotates counterclockwise.
		props = append(props, heif.Rotation(-vt.Rotation/90))
	}
	v.decode = func(data []byte) (image.Image, error) {
		b, err := heif.EncodeImage("heic", vt.Codec, props, data)
		if err != nil {
			return nil, err
		}
		return heic.Decode(bytes.NewReader(b))
	}
	return v, nil
}

func openAVI(r io.ReaderAt, size int64) (*Video, error) {
	h, err := avi.ReadHeader(r, size)
	if errors.Is(err, avi.ErrNotAVI) {
		return nil, ErrUnsupported
	}
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(h.Codec, "MJPG") {
		return openStream(r, size, "avi")
	}
	if h.Width <= 0 || h.Height <= 0 {
		return nil, ErrUnsupported
	}
	frames, err := avi.Frames(r, size, h.VideoStream)
	if err != nil {
		return nil, err
	}
	v := &Video{Width: h.Width, Height: h.Height, r: r, duration: h.Duration(), decode: avi.DecodeMJPEG}
	for i, f := range frames {
		v.frames = append(v.frames, frame{time: time.Duration(i) * h.FrameDuration, offset: f.Offset, size: f.Size})
	}
	return v, nil
}

// openStream opens a video that ffmpeg reads whole, from its start, of
// the given ffmpeg format. Its still is its first frame at or after a
// second, or else its first frame, of at most maxStreamSide pixels wide
// and high.
func openStream(r io.ReaderAt, size int64, format string) (*Video, error) {
	if ffmpegPath() == "" {
		return nil, ErrUnsupported
	}
	scale := fmt.Sprintf(`scale=w=min(iw\,%d):h=min(ih\,%[1]d):force_original_aspect_ratio=decrease`, maxStreamSide)
	v := &Video{Width: maxStreamSide, Height: maxStreamSide, frames: []frame{{}}}
	v.decodeAt = func(frame) (image.Image, error) {
		im, err := runFFmpeg(format, io.NewSectionReader(r, 0, size), `select=gte(t\,1),`+scale)
		if err != nil && !errors.Is(err, errNoFrame) {
			return nil, err
		}
		if err != nil {
			// Shorter than a second.
			im, err = runFFmpeg(format, io.NewSectionReader(r, 0, size), scale)
		}
		return im, err
	}
	return v, nil
}

// Decode decodes a still of the video: its first decodable frame at or
// after the lesser of a second and a tenth of its duration, or failing
// that, its first decodable frame. If that frame is nearly black or of
// nearly one color, as at the start of fades, Decode tries later frames,
// spaced as far apart, returning the first that isn't, or else the most
// varied one.
func (v *Video) Decode() (image.Image, error) {
	if len(v.frames) == 0 {
		return nil, fmt.Errorf("%w: video has no decodable frames", ErrUnsupported)
	}
	step := time.Second
	if v.duration > 0 {
		step = min(step, v.duration/10)
	}
	var best image.Image
	bestSpread := -1.0
	var firstErr error
	i := v.frameAt(step)
	for range tries {
		im, err := v.decodeFrame(v.frames[i])
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
		} else {
			mean, spread := luma(im)
			if !blank(mean, spread) {
				return v.convert(im), nil
			}
			if spread > bestSpread {
				best, bestSpread = im, spread
			}
		}
		next := v.frameAt(v.frames[i].time + max(step, time.Millisecond))
		if next <= i {
			break
		}
		i = next
	}
	if best != nil {
		return v.convert(best), nil
	}
	return nil, firstErr
}

// convert returns the decoded frame im converted for display as sRGB.
func (v *Video) convert(im image.Image) image.Image {
	if v.color == nil {
		return im
	}
	return v.color.convert(im)
}

// frameAt returns the index of the first frame at or after t, or of the
// first frame if none is.
func (v *Video) frameAt(t time.Duration) int {
	for i, f := range v.frames {
		if f.time >= t {
			return i
		}
	}
	return 0
}

func (v *Video) decodeFrame(f frame) (image.Image, error) {
	if v.decodeAt != nil {
		return v.decodeAt(f)
	}
	data, err := v.read(f.offset, f.size)
	if err != nil {
		return nil, err
	}
	return v.decode(data)
}

// read returns the coded frame data at offset, of size bytes.
func (v *Video) read(offset, size int64) ([]byte, error) {
	if size <= 0 || size > maxFrameSize {
		return nil, fmt.Errorf("still: bad frame size %d", size)
	}
	data := make([]byte, size)
	if _, err := v.r.ReadAt(data, offset); err != nil {
		return nil, fmt.Errorf("still: reading frame: %w", err)
	}
	return data, nil
}

// blank reports whether an image of the given luma mean and standard
// deviation, out of 255, is nearly black or of nearly one color.
func blank(mean, spread float64) bool {
	return mean < 20 || spread < 6
}

// luma returns the mean and standard deviation of im's luma, out of 255,
// sampled on a grid.
func luma(im image.Image) (mean, spread float64) {
	const grid = 32
	b := im.Bounds()
	var sum, sumSq float64
	var n int
	for gy := range grid {
		for gx := range grid {
			x := b.Min.X + (2*gx+1)*b.Dx()/(2*grid)
			y := b.Min.Y + (2*gy+1)*b.Dy()/(2*grid)
			var l float64
			if yc, ok := im.(*image.YCbCr); ok {
				l = float64(yc.Y[yc.YOffset(x, y)])
			} else {
				r, g, bb, _ := im.At(x, y).RGBA()
				l = (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bb)) / 257
			}
			sum += l
			sumSq += l * l
			n++
		}
	}
	mean = sum / float64(n)
	variance := max(0, sumSq/float64(n)-mean*mean)
	return mean, math.Sqrt(variance)
}
