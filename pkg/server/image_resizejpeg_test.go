/*
Copyright 2026 The Perkeep Authors

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
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"strings"
	"testing"
	"time"

	"go4.org/syncutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/schema"
)

func jpegScaleTestFile(t *testing.T, data []byte, capacity int64, width, height int) (*ImageHandler, blob.Ref) {
	t.Helper()
	store := new(memory.Storage)
	ref, err := schema.WriteFileFromReader(context.Background(), store, "image", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return &ImageHandler{
		Fetcher: store, ResizeSem: syncutil.NewSem(capacity),
		MaxWidth: width, MaxHeight: height,
	}, ref
}

func jpegScaleTestJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	im := image.NewYCbCr(image.Rect(0, 0, width, height), image.YCbCrSubsampleRatio420)
	for y := range height {
		for x := range width {
			im.Y[im.YOffset(x, y)] = uint8((x + y) % 256)
		}
	}
	for i := range im.Cb {
		im.Cb[i], im.Cr[i] = 96, 160
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, im, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// jpegScaleTestProgressive makes a constant-gray progressive JPEG with one DC
// scan. Each block's DC difference is zero, encoded by a one-bit Huffman code;
// omitted AC coefficients remain zero. This needs no external encoder/fixture.
func jpegScaleTestProgressive(width, height int) []byte {
	b := []byte{0xff, 0xd8, 0xff, 0xdb, 0, 67, 0}
	b = append(b, bytes.Repeat([]byte{1}, 64)...)
	b = append(b, 0xff, 0xc2, 0, 11, 8, byte(height>>8), byte(height),
		byte(width>>8), byte(width), 1, 1, 0x11, 0)
	b = append(b, 0xff, 0xc4, 0, 20, 0, 1)
	b = append(b, make([]byte, 15)...)
	b = append(b, 0, 0xff, 0xda, 0, 8, 1, 1, 0, 0, 0, 0)
	blocks := ((width + 7) / 8) * ((height + 7) / 8)
	b = append(b, make([]byte, blocks/8)...)
	if remainder := blocks % 8; remainder != 0 {
		b = append(b, byte((1<<(8-remainder))-1))
	}
	return append(b, 0xff, 0xd9)
}

func waitImageMemorySlots(t *testing.T, want int) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for len(imageMemorySlots) != want {
		select {
		case <-timer.C:
			t.Fatalf("image memory slots = %d; want %d", len(imageMemorySlots), want)
		case <-tick.C:
		}
	}
}

// Check both missing permits and excess permits (e.g. a double release).
func checkImageMemoryReleased(t *testing.T, ih *ImageHandler, capacity int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := ih.acquireImageMemory(ctx, capacity)
	if err != nil {
		t.Fatalf("reacquiring full memory budget: %v", err)
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	probeRelease, err := ih.acquireImageMemory(probeCtx, 1)
	probeCancel()
	if probeRelease != nil {
		probeRelease()
	}
	release()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquiring beyond full budget: %v; want deadline exceeded", err)
	}
	waitImageMemorySlots(t, 0)
}

func TestScaleImageJPEGMemoryPlan(t *testing.T) {
	const capacity = 4 << 20
	const sourceSize = 2000
	if sourceSize*sourceSize*3 <= capacity {
		t.Fatal("test source must exceed the old full-image estimate")
	}
	ih, ref := jpegScaleTestFile(t, jpegScaleTestJPEG(t, sourceSize, sourceSize), capacity, 20, 20)
	data, format, err := ih.ScaleImage(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" {
		t.Fatalf("format = %q; want jpeg", format)
	}
	im, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if got := im.Bounds().Size(); got != image.Pt(20, 20) {
		t.Fatalf("thumbnail bounds = %v; want 20x20", got)
	}
	checkImageMemoryReleased(t, ih, capacity)
}

func TestScaleImageJPEGNoScaleMemoryAdmission(t *testing.T) {
	data := jpegScaleTestJPEG(t, 40, 20)
	// The old source-pixel estimate fits, but the actual JPEG plan does not.
	ih, ref := jpegScaleTestFile(t, data, 40*20*3, 40, 20)
	if _, _, err := ih.ScaleImage(context.Background(), ref); err == nil {
		t.Fatal("unscaled JPEG bypassed memory admission")
	}
	checkImageMemoryReleased(t, ih, 40*20*3)
	ih.ResizeSem = syncutil.NewSem(4 << 20)
	got, format, err := ih.ScaleImage(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	conf, err := jpeg.DecodeConfig(bytes.NewReader(got))
	if err != nil || format != "jpeg" || conf.Width != 40 || conf.Height != 20 {
		t.Fatalf("unscaled JPEG: config %+v, format %q, error %v", conf, format, err)
	}
	checkImageMemoryReleased(t, ih, 4<<20)
}

func TestScaleImageJPEGProgressiveMemoryPlan(t *testing.T) {
	const capacity = 4 << 20
	t.Run("admitted", func(t *testing.T) {
		data := jpegScaleTestProgressive(512, 384)
		// Ensure the hand-built fixture is also a valid standard JPEG.
		if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		ih, ref := jpegScaleTestFile(t, data, capacity, 20, 20)
		got, format, err := ih.ScaleImage(context.Background(), ref)
		if err != nil {
			t.Fatal(err)
		}
		conf, err := jpeg.DecodeConfig(bytes.NewReader(got))
		if err != nil || format != "jpeg" || conf.Width != 20 || conf.Height != 15 {
			t.Fatalf("progressive JPEG: config %+v, format %q, error %v", conf, format, err)
		}
		checkImageMemoryReleased(t, ih, capacity)
	})
	t.Run("oversized-state", func(t *testing.T) {
		// Its compact progressive coefficients exceed the budget even at
		// the largest reduction factor. Omit entropy entirely: admission
		// must fail at the scan header, before decoding image-wide state.
		data := jpegScaleTestProgressive(16000, 16000)
		sos := bytes.Index(data, []byte{0xff, 0xda})
		data = data[:sos+10]
		ih, ref := jpegScaleTestFile(t, data, capacity, 20, 20)
		_, _, err := ih.ScaleImage(context.Background(), ref)
		if err == nil || !strings.Contains(err.Error(), "semaphore size") {
			t.Fatalf("oversized progressive error = %v; want memory admission failure", err)
		}
		checkImageMemoryReleased(t, ih, capacity)
	})
}

func TestScaleImageJPEGDecodeErrorReleasesMemory(t *testing.T) {
	for _, progressive := range []bool{false, true} {
		name := "baseline"
		data := jpegScaleTestJPEG(t, 512, 384)
		if progressive {
			name, data = "progressive", jpegScaleTestProgressive(512, 384)
		}
		t.Run(name, func(t *testing.T) {
			sos := bytes.Index(data, []byte{0xff, 0xda})
			sosLength := int(data[sos+2])<<8 | int(data[sos+3])
			data = data[:sos+2+sosLength]
			ih, ref := jpegScaleTestFile(t, data, 4<<20, 20, 20)
			if _, _, err := ih.ScaleImage(context.Background(), ref); err == nil {
				t.Fatal("JPEG with no entropy unexpectedly decoded")
			}
			checkImageMemoryReleased(t, ih, 4<<20)
		})
	}
}

func TestScaleImageJPEGMemoryCancellation(t *testing.T) {
	const capacity = 4 << 20
	ih, ref := jpegScaleTestFile(t, jpegScaleTestJPEG(t, 512, 384), capacity, 20, 20)
	if err := ih.ResizeSem.Acquire(capacity); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := ih.ScaleImage(ctx, ref)
		done <- err
	}()
	waitImageMemorySlots(t, 1)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ScaleImage error = %v; want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ScaleImage did not cancel while waiting for memory")
	}
	ih.ResizeSem.Release(capacity)
	waitImageMemorySlots(t, 0)
	checkImageMemoryReleased(t, ih, capacity)
	// An already canceled context cannot even start reading the source.
	ih.Fetcher = nil
	if _, _, err := ih.ScaleImage(ctx, ref); !errors.Is(err, context.Canceled) {
		t.Fatalf("already canceled ScaleImage error = %v", err)
	}
}

func TestImageMemoryCancellationBoundsWorkers(t *testing.T) {
	ih := &ImageHandler{ResizeSem: syncutil.NewSem(1)}
	if err := ih.ResizeSem.Acquire(1); err != nil {
		t.Fatal(err)
	}
	// Leave one canceled worker per slot blocked in the legacy semaphore.
	for n := 1; n <= cap(imageMemorySlots); n++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := ih.acquireImageMemory(ctx, 1)
			done <- err
		}()
		waitImageMemorySlots(t, n)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("acquire error = %v; want canceled", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := ih.acquireImageMemory(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire with all worker slots occupied = %v", err)
	}
	ih.ResizeSem.Release(1)
	waitImageMemorySlots(t, 0)
	checkImageMemoryReleased(t, ih, 1)
}

func TestScaleImagePNGMemoryEstimateUnchanged(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 40, 20))); err != nil {
		t.Fatal(err)
	}
	const capacity = 40 * 20 * 3
	ih, ref := jpegScaleTestFile(t, buf.Bytes(), capacity, 20, 20)
	data, format, err := ih.ScaleImage(context.Background(), ref)
	if err != nil || format != "png" {
		t.Fatalf("ScaleImage PNG: format %q, error %v", format, err)
	}
	conf, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || conf.Width != 20 || conf.Height != 10 {
		t.Fatalf("PNG thumbnail: config %+v, error %v", conf, err)
	}
	checkImageMemoryReleased(t, ih, capacity)
	ih.ResizeSem = syncutil.NewSem(capacity - 1)
	if _, _, err := ih.ScaleImage(context.Background(), ref); err == nil {
		t.Fatal("PNG bypassed source-pixel memory estimate")
	}
	checkImageMemoryReleased(t, ih, capacity-1)
}

func TestImageMemoryEstimateOverflow(t *testing.T) {
	if _, err := imageMemoryEstimate(math.MaxInt, math.MaxInt, 3); err == nil {
		t.Fatal("overflowing dimensions were accepted")
	}
	if _, err := imageMemoryEstimate(0, 1, 3); err == nil {
		t.Fatal("zero dimensions were accepted")
	}
	ih := &ImageHandler{MaxWidth: math.MaxInt, MaxHeight: math.MaxInt}
	got, err := ih.jpegPostDecodeMemory(image.Config{Width: 40, Height: 20})
	if err != nil || got != 40*20*16+(256<<10) {
		t.Fatalf("huge target memory estimate = %d, %v", got, err)
	}
}
