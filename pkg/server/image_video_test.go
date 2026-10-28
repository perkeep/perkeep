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

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"testing"

	"perkeep.org/internal/video/still"
)

func TestScaleImageVideo(t *testing.T) {
	// See internal/video/still/testdata/gen.sh.
	for _, tt := range []struct {
		name          string
		width, height int // of the thumbnail
	}{
		{"fade.mov", 24, 32}, // 64x48, rotated 90 degrees
		{"mjpeg.avi", 32, 24},
	} {
		data, err := os.ReadFile("../../internal/video/still/testdata/" + tt.name)
		if err != nil {
			t.Fatal(err)
		}
		const capacity = 1 << 20
		ih, ref := jpegScaleTestFile(t, data, capacity, 32, 32)
		ok := videoStillOK.Value()
		thumb, format, err := ih.ScaleImage(context.Background(), ref)
		if err != nil {
			t.Fatalf("%s: ScaleImage: %v", tt.name, err)
		}
		if format != "jpeg" {
			t.Errorf("%s: format = %q; want jpeg", tt.name, format)
		}
		conf, err := jpeg.DecodeConfig(bytes.NewReader(thumb))
		if err != nil {
			t.Fatal(err)
		}
		if conf.Width != tt.width || conf.Height != tt.height {
			t.Errorf("%s: thumbnail is %dx%d; want %dx%d", tt.name, conf.Width, conf.Height, tt.width, tt.height)
		}
		if got := videoStillOK.Value() - ok; got != 1 {
			t.Errorf("%s: video stills = %d; want 1", tt.name, got)
		}
		checkImageMemoryReleased(t, ih, capacity)
	}

	data, err := os.ReadFile("../../internal/video/still/testdata/h264.mp4")
	if err != nil {
		t.Fatal(err)
	}
	ih, ref := jpegScaleTestFile(t, data, 1<<20, 32, 32)
	unsupported := videoStillUnsupported.Value()
	if _, _, err := ih.ScaleImage(context.Background(), ref); !errors.Is(err, still.ErrUnsupported) {
		t.Errorf("ScaleImage of H.264 video: %v; want still.ErrUnsupported", err)
	}
	if got := videoStillUnsupported.Value() - unsupported; got != 1 {
		t.Errorf("unsupported videos = %d; want 1", got)
	}

	// Files indexed as videos by their names, but that aren't, are
	// unsupported rather than decoded as images.
	ih, ref = jpegScaleTestFile(t, bytes.Repeat([]byte("not a video "), 100), 1<<20, 32, 32)
	if _, err := ih.scaleImage(context.Background(), ref, "video/3gpp", false); !errors.Is(err, still.ErrUnsupported) {
		t.Errorf("scaleImage of a non-video indexed as a video: %v; want still.ErrUnsupported", err)
	}
	if _, err := ih.scaleImage(context.Background(), ref, "video/x-ms-wmv", false); !errors.Is(err, still.ErrUnsupported) {
		t.Errorf("scaleImage of a WMV video: %v; want still.ErrUnsupported", err)
	}

	// A video whose type isn't sniffed, as for QuickTime files whose
	// "ftyp" box isn't the size internal/magic expects, is decoded by
	// its indexed type.
	data, err = os.ReadFile("../../internal/video/still/testdata/fade.mov")
	if err != nil {
		t.Fatal(err)
	}
	copy(data[4:8], "free") // so it's not sniffed as QuickTime
	ih, ref = jpegScaleTestFile(t, data, 1<<20, 32, 32)
	if _, err := ih.scaleImage(context.Background(), ref, "", false); err == nil {
		t.Error("scaleImage of an unsniffable video succeeded without its type")
	}
	if _, err := ih.scaleImage(context.Background(), ref, "video/quicktime", false); err != nil {
		t.Errorf("scaleImage of an unsniffable video indexed as QuickTime: %v", err)
	}
}

func TestVideoMaterialIcon(t *testing.T) {
	// Pin the unmodified official file, as for the fitness icons. Source:
	// google/material-design-icons at 49d4db35df873165d6bd6ba09b063c7dafbac2f4,
	// src/av/videocam/materialiconsround/24px.svg.
	data, err := materialIconFiles.ReadFile("materialicons/videocam.svg")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprintf("%x", sha256.Sum256(data)), "5d05d32594a6f275301f7b5a84aa86927ef2508ab3d461fcf0a0c3299f330839"; got != want {
		t.Fatalf("upstream videocam.svg bytes changed: %s", got)
	}
	icon, err := videoMaterialIcon()
	if err != nil {
		t.Fatal(err)
	}
	if len(icon.shapes) != 1 || len(icon.shapes[0].path) == 0 {
		t.Errorf("videocam icon has %d shapes; want 1 path", len(icon.shapes))
	}
}

func TestVideoBadge(t *testing.T) {
	data, err := os.ReadFile("../../internal/video/still/testdata/mjpeg.avi")
	if err != nil {
		t.Fatal(err)
	}
	ih, ref := jpegScaleTestFile(t, data, 1<<20, 256, 256)
	decode := func(badge bool) image.Image {
		t.Helper()
		fi, err := ih.scaleImage(context.Background(), ref, "video/x-msvideo", badge)
		if err != nil {
			t.Fatal(err)
		}
		im, err := jpeg.Decode(bytes.NewReader(fi.image))
		if err != nil {
			t.Fatal(err)
		}
		return im
	}
	plain, badged := decode(false), decode(true)
	if plain.Bounds() != badged.Bounds() {
		t.Fatalf("badged thumbnail is %v; plain one %v", badged.Bounds(), plain.Bounds())
	}
	// The badge's white disc is at the bottom left; the top right is
	// unchanged, but for JPEG noise.
	b := badged.Bounds()
	size := min(46, float64(min(b.Dx(), b.Dy()))*.27)
	margin := max(2, float64(min(b.Dx(), b.Dy()))*.055)
	// A point on the disc, inside its edge but outside the icon.
	x, y := int(margin+size*.15), b.Max.Y-int(margin+size/2)
	if r, g, bl, _ := badged.At(x, y).RGBA(); r>>8 < 230 || g>>8 < 230 || bl>>8 < 230 {
		t.Errorf("badged thumbnail at (%d, %d) = %v; want the badge's white", x, y, badged.At(x, y))
	}
	if d := colorDistance(plain.At(b.Max.X-3, 2), badged.At(b.Max.X-3, 2)); d > 8 {
		t.Errorf("badge changed the top right corner by %.0f", d)
	}

	// ScaleImage, as used by the Immich API, adds no badge.
	thumb, _, err := ih.ScaleImage(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	im, err := jpeg.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatal(err)
	}
	if d := colorDistance(plain.At(x, y), im.At(x, y)); d > 8 {
		t.Errorf("ScaleImage thumbnail differs where the badge would be by %.0f", d)
	}

	if videoCacheKey("sha224-x", 1, 2) == cacheKey("sha224-x", 1, 2) {
		t.Error("video thumbnails have the same cache keys as images")
	}
}

func colorDistance(a, b color.Color) float64 {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	d := func(x, y uint32) float64 { return max(float64(x>>8)-float64(y>>8), float64(y>>8)-float64(x>>8)) }
	return (d(ar, br) + d(ag, bg) + d(ab, bb)) / 3
}
