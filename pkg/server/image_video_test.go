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
	"errors"
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
	if _, err := ih.scaleImage(context.Background(), ref, "video/3gpp"); !errors.Is(err, still.ErrUnsupported) {
		t.Errorf("scaleImage of a non-video indexed as a video: %v; want still.ErrUnsupported", err)
	}
	if _, err := ih.scaleImage(context.Background(), ref, "video/x-ms-wmv"); !errors.Is(err, still.ErrUnsupported) {
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
	if _, err := ih.scaleImage(context.Background(), ref, ""); err == nil {
		t.Error("scaleImage of an unsniffable video succeeded without its type")
	}
	if _, err := ih.scaleImage(context.Background(), ref, "video/quicktime"); err != nil {
		t.Errorf("scaleImage of an unsniffable video indexed as QuickTime: %v", err)
	}
}
