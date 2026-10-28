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
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	if ffmpegPath() == "" {
		t.Skip("ffmpeg not installed")
	}
}

// withoutFFmpeg runs f as if ffmpeg weren't installed.
func withoutFFmpeg(f func()) {
	old := ffmpegPath
	ffmpegPath = func() string { return "" }
	defer func() { ffmpegPath = old }()
	f()
}

func TestDecodeFFmpeg(t *testing.T) {
	needFFmpeg(t)
	for _, tt := range []struct {
		name, mimeType string
		w, h           int // of the still
		at             time.Duration
	}{
		{"h264.mp4", "video/mp4", 64, 48, 0},
		{"h264-rotated.mp4", "video/mp4", 48, 64, 500 * time.Millisecond},
		{"h263.3gp", "video/3gpp", 128, 96, 1200 * time.Millisecond},
		{"mpeg4.mp4", "video/mp4", 64, 48, 1200 * time.Millisecond},
		{"mjpeg.mov", "video/quicktime", 64, 48, 200 * time.Millisecond},
		{"wmv2.wmv", "video/x-ms-wmv", 64, 48, time.Second},
		// As some cameras' WMV files named ".AVI" are indexed.
		{"wmv2.wmv", "video/x-msvideo", 64, 48, time.Second},
		{"vp8.webm", "video/webm", 64, 48, time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v, err := openTestdata(t, tt.name, tt.mimeType)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			im, err := v.Decode()
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got := im.Bounds().Size(); got != image.Pt(tt.w, tt.h) {
				t.Fatalf("still is %v; want %dx%d", got, tt.w, tt.h)
			}
			// Compare with ffmpeg's decoding of the whole file, rotated
			// as its metadata says.
			want := ffmpegFrame(t, tt.name, tt.at)
			if d := meanDiff(im, want); d > 2 {
				t.Errorf("still differs from ffmpeg's frame at %v by %.1f per pixel", tt.at, d)
			}
		})
	}
}

// TestDecodeFFmpegColor tests the range and matrix of ffmpeg's conversion
// to RGB, of BT.709 limited range Y'CbCr.
func TestDecodeFFmpegColor(t *testing.T) {
	needFFmpeg(t)
	v, err := openTestdata(t, "sienna709.mp4", "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	im, err := v.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if d := maxDiff(im.At(32, 24), sienna); d > 2 {
		t.Errorf("decoded %v; want about %v", im.At(32, 24), sienna)
	}
}

func TestFFmpegUnsupported(t *testing.T) {
	needFFmpeg(t)
	// A QuickTime file named as WebM, which ffmpeg reads whole.
	v, err := openTestdata(t, "fade.mov", "video/webm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Decode(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Decode error = %v; want ErrUnsupported", err)
	}
}

// ffmpegFrame returns ffmpeg's decoding of the frame of test video name
// at t.
func ffmpegFrame(t *testing.T, name string, at time.Duration) image.Image {
	t.Helper()
	cmd := exec.Command(ffmpegPath(), "-v", "error",
		"-ss", fmt.Sprint(at.Seconds()), "-i", filepath.Join("testdata", name),
		"-frames:v", "1", "-vf", "format=rgb24", "-f", "image2pipe", "-c:v", "ppm", "pipe:1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, stderr.Bytes())
	}
	im, err := decodePPM(out)
	if err != nil {
		t.Fatal(err)
	}
	return im
}

// meanDiff returns the mean absolute difference of a and b's components.
func meanDiff(a, b image.Image) float64 {
	ab, bb := a.Bounds(), b.Bounds()
	if ab.Size() != bb.Size() {
		return 255
	}
	var sum float64
	for y := range ab.Dy() {
		for x := range ab.Dx() {
			sum += colorDistance(a.At(ab.Min.X+x, ab.Min.Y+y), b.At(bb.Min.X+x, bb.Min.Y+y))
		}
	}
	return sum / float64(ab.Dx()*ab.Dy())
}

func TestDecodePPM(t *testing.T) {
	im, err := decodePPM([]byte("P6\n2 1\n255\n\x01\x02\x03\x04\x05\x06"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{1, 2, 3, 255, 4, 5, 6, 255}; !bytes.Equal(im.Pix, want) {
		t.Errorf("pixels = %v; want %v", im.Pix, want)
	}
	for _, bad := range []string{
		"",
		"P6\n2 1\n255\n\x01\x02\x03", // short
		"P5\n2 1\n255\n\x01\x02",     // gray
		"P6\n2 1\n65535\n" + string(make([]byte, 12)), // 16-bit
		"P6\n-2 1\n255\n",
		"P6\n99999 99999\n255\n",
	} {
		if _, err := decodePPM([]byte(bad)); err == nil {
			t.Errorf("decodePPM(%q) succeeded", bad)
		}
	}
}

func TestESDS(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "mpeg4.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(b, []byte("esds"))
	if i < 4 {
		t.Fatal("no esds box")
	}
	// The decoder specific information starts with a visual object
	// sequence start code.
	info := esdsDecoderSpecificInfo(b[i+4:])
	if !bytes.HasPrefix(info, []byte{0, 0, 1, 0xb0}) {
		t.Errorf("decoder specific info starts %x; want 000001b0", info[:min(len(info), 4)])
	}
	if got := esdsDecoderSpecificInfo([]byte{0, 0, 0, 0, 3, 0x80}); got != nil {
		t.Errorf("truncated esds gave %x", got)
	}
}

func TestAnnexB(t *testing.T) {
	got, err := annexB(4)([]byte{0, 0, 0, 2, 0xa, 0xb, 0, 0, 0, 1, 0xc})
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{0, 0, 0, 1, 0xa, 0xb, 0, 0, 0, 1, 0xc}; !bytes.Equal(got, want) {
		t.Errorf("got %x; want %x", got, want)
	}
	if _, err := annexB(4)([]byte{0, 0, 0, 9, 0xa}); err == nil {
		t.Error("truncated NAL unit succeeded")
	}
}
