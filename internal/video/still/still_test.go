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
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gen2brain/heic"
	"go4.org/media/heif"
	"perkeep.org/internal/media/quicktime"
)

// forEachHEICDecoder runs f as a subtest with each of the heic package's
// decoders: the system's libheif, if it can be loaded, and the embedded
// WebAssembly one.
func forEachHEICDecoder(t *testing.T, f func(t *testing.T)) {
	t.Run("libheif", func(t *testing.T) {
		if err := heic.Dynamic(); err != nil {
			t.Skipf("libheif not available: %v", err)
		}
		f(t)
	})
	t.Run("wasm", func(t *testing.T) {
		heic.ForceWasmMode = true
		defer func() { heic.ForceWasmMode = false }()
		f(t)
	})
}

// openTestdata opens the test video name (see testdata/gen.sh).
func openTestdata(t *testing.T, name, mimeType string) (*Video, error) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return Open(bytes.NewReader(b), int64(len(b)), mimeType)
}

func mustDecode(t *testing.T, name, mimeType string) (*Video, image.Image) {
	t.Helper()
	v, err := openTestdata(t, name, mimeType)
	if err != nil {
		t.Fatalf("Open(%s): %v", name, err)
	}
	im, err := v.Decode()
	if err != nil {
		t.Fatalf("Decode(%s): %v", name, err)
	}
	if got, want := im.Bounds().Size(), image.Pt(v.Width, v.Height); got != want {
		t.Errorf("%s decoded to %v; Open reported %v", name, got, want)
	}
	if mean, spread := luma(im); blank(mean, spread) {
		t.Errorf("%s decoded to a blank image: luma %.0f±%.0f", name, mean, spread)
	}
	return v, im
}

func TestDecodeHEVC(t *testing.T) {
	forEachHEICDecoder(t, func(t *testing.T) {
		// fade.mov's keyframes at 0.5 and 1 seconds are black, so
		// Decode moves on to the one at 1.5 seconds. It's rotated
		// 90 degrees clockwise for display.
		v, im := mustDecode(t, "fade.mov", "video/quicktime")
		if v.Width != 48 || v.Height != 64 {
			t.Errorf("fade.mov size = %dx%d; want 48x64", v.Width, v.Height)
		}
		// Compare with the unrotated frame, rotated here.
		f := v.frames[v.frameAt(1500*time.Millisecond)]
		raw := decodeUnrotated(t, "fade.mov", f)
		rb := raw.Bounds()
		if rb.Dx() != 64 || rb.Dy() != 48 {
			t.Fatalf("unrotated frame is %v; want 64x48", rb.Size())
		}
		var cw, ccw float64
		for y := range 64 {
			for x := range 48 {
				// Rotating clockwise, (x, y) comes from (y, 47-x).
				cw += colorDistance(im.At(x, y), raw.At(rb.Min.X+y, rb.Min.Y+47-x))
				ccw += colorDistance(im.At(x, y), raw.At(rb.Min.X+63-y, rb.Min.Y+x))
			}
		}
		// The decoder's rotation of the subsampled chroma of so small a
		// test pattern differs slightly from rotating its RGB here.
		cw, ccw = cw/(48*64), ccw/(48*64)
		if cw > 20 || cw*4 > ccw {
			t.Errorf("still differs from the keyframe rotated clockwise by %.1f per pixel, counterclockwise by %.1f", cw, ccw)
		}

		mustDecode(t, "hdr.mov", "video/quicktime")    // 10-bit
		mustDecode(t, "onekey.mov", "video/quicktime") // its only keyframe is first
	})
}

// decodeUnrotated decodes frame f of the QuickTime test video name
// without its rotation.
func decodeUnrotated(t *testing.T, name string, f frame) image.Image {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	vt, err := quicktime.ReadVideoTrack(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	props := []heif.Property{{Box: vt.Config, Essential: true}, heif.SpatialExtents(uint32(vt.Width), uint32(vt.Height))}
	h, err := heif.EncodeImage("heic", vt.Codec, props, b[f.offset:f.offset+f.size])
	if err != nil {
		t.Fatal(err)
	}
	im, err := heic.Decode(bytes.NewReader(h))
	if err != nil {
		t.Fatal(err)
	}
	return im
}

func colorDistance(a, b color.Color) float64 {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	d := func(x, y uint32) float64 { return max(float64(x>>8)-float64(y>>8), float64(y>>8)-float64(x>>8)) }
	return (d(ar, br) + d(ag, bg) + d(ab, bb)) / 3
}

func TestDecodeMJPEG(t *testing.T) {
	v, _ := mustDecode(t, "mjpeg.avi", "video/x-msvideo")
	if v.Width != 64 || v.Height != 48 {
		t.Errorf("mjpeg.avi size = %dx%d; want 64x48", v.Width, v.Height)
	}
}

func TestUnsupported(t *testing.T) {
	for _, tt := range []struct{ name, mimeType string }{
		{"gen.sh", "video/quicktime"},
		{"gen.sh", "video/x-msvideo"},
		{"fade.mov", "video/x-unknown"},
	} {
		if _, err := openTestdata(t, tt.name, tt.mimeType); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Open(%s as %s) error = %v; want ErrUnsupported", tt.name, tt.mimeType, err)
		}
	}
	withoutFFmpeg(func() {
		for _, tt := range []struct{ name, mimeType string }{
			{"h264.mp4", "video/mp4"},
			{"vp8.webm", "video/webm"},
		} {
			if _, err := openTestdata(t, tt.name, tt.mimeType); !errors.Is(err, ErrUnsupported) {
				t.Errorf("without ffmpeg, Open(%s as %s) error = %v; want ErrUnsupported", tt.name, tt.mimeType, err)
			}
		}
		if !Supported("video/quicktime") || !Supported("video/x-msvideo") || Supported("video/webm") {
			t.Error("without ffmpeg, Supported is wrong")
		}
	})

	// Files that aren't what their MIME types say, as indexed by their
	// names: garbage, and an ASF (WMV) header.
	for _, tt := range []struct {
		data     []byte
		mimeType string
	}{
		{bytes.Repeat([]byte{0x7e, 0xb3, 0x75, 0x73}, 100), "video/3gpp"},
		{append([]byte{0x30, 0x26, 0xb2, 0x75, 0x8e, 0x66, 0xcf, 0x11}, make([]byte, 100)...), "video/vnd.avi"},
		{[]byte("\x00\x00\x00\x08moov"), "video/quicktime"}, // an empty movie
	} {
		// With ffmpeg, the ASF one is found unsupported only when
		// ffmpeg reads it.
		v, err := Open(bytes.NewReader(tt.data), int64(len(tt.data)), tt.mimeType)
		if err == nil {
			_, err = v.Decode()
		}
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("Open and Decode(%q as %s) error = %v; want ErrUnsupported", tt.data[:8], tt.mimeType, err)
		}
	}
}

// TestFrameChoice tests Decode's choice of frames with a fake decoder.
func TestFrameChoice(t *testing.T) {
	gray := func(c uint8) image.Image {
		im := image.NewGray(image.Rect(0, 0, 16, 16))
		for i := range im.Pix {
			im.Pix[i] = c
		}
		return im
	}
	varied := func(c uint8) image.Image {
		im := image.NewGray(image.Rect(0, 0, 16, 16))
		for i := range im.Pix {
			im.Pix[i] = c + uint8(i%2)*60
		}
		return im
	}
	// Frames are identified by their offsets, set to their times in
	// milliseconds, to find their images.
	video := func(dur time.Duration, imgs map[int64]image.Image, times ...int64) *Video {
		v := &Video{duration: dur, r: bytes.NewReader(make([]byte, 100<<10))}
		for _, ms := range times {
			v.frames = append(v.frames, frame{time: time.Duration(ms) * time.Millisecond, offset: ms, size: 1})
		}
		v.decode = func([]byte) (image.Image, error) { return nil, errors.New("unused") }
		v.decodeAt = func(f frame) (image.Image, error) {
			if im, ok := imgs[f.offset]; ok {
				return im, nil
			}
			return nil, errors.New("decode failed")
		}
		return v
	}
	for _, tt := range []struct {
		name string
		v    *Video
		want image.Image // nil for an error
	}{
		{
			"first after a second",
			video(10*time.Second, map[int64]image.Image{0: varied(1), 1000: varied(2), 2000: varied(3)}, 0, 1000, 2000),
			varied(2),
		},
		{
			"a tenth of a short video",
			video(2*time.Second, map[int64]image.Image{0: varied(1), 200: varied(2), 1000: varied(3)}, 0, 200, 1000),
			varied(2),
		},
		{
			"skips black",
			video(10*time.Second, map[int64]image.Image{1000: gray(0), 2000: gray(10), 3000: varied(80)}, 0, 1000, 2000, 3000),
			varied(80),
		},
		{
			"skips uniform",
			video(10*time.Second, map[int64]image.Image{1000: gray(200), 2000: varied(80)}, 0, 1000, 2000),
			varied(80),
		},
		{
			"most varied if all blank",
			video(10*time.Second, map[int64]image.Image{1000: gray(0), 2000: varied(0), 3000: gray(5)}, 1000, 2000, 3000, 4000),
			varied(0),
		},
		{
			"first if none after",
			video(10*time.Second, map[int64]image.Image{0: varied(1)}, 0),
			varied(1),
		},
		{
			"after a failure",
			video(10*time.Second, map[int64]image.Image{2000: varied(7)}, 1000, 2000),
			varied(7),
		},
		{
			"all fail",
			video(10*time.Second, nil, 1000, 2000),
			nil,
		},
	} {
		got, err := tt.v.Decode()
		switch {
		case tt.want == nil && err == nil:
			t.Errorf("%s: Decode succeeded; want error", tt.name)
		case tt.want != nil && err != nil:
			t.Errorf("%s: Decode: %v", tt.name, err)
		case tt.want != nil && !bytes.Equal(got.(*image.Gray).Pix, tt.want.(*image.Gray).Pix):
			t.Errorf("%s: Decode chose the wrong frame", tt.name)
		}
	}
	if _, err := (&Video{}).Decode(); err == nil {
		t.Error("Decode of a video without frames succeeded; want error")
	}
}
