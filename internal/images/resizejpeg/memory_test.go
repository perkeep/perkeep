//go:build jpegmemory

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

package resizejpeg

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestThumbnailMemoryProbe is an opt-in, one-thumbnail peak-memory probe, not
// an allocation benchmark. Build the test binary once with -tags=jpegmemory,
// then invoke only this test in a fresh process for each input or baseline.
// Do not generate the fixture or run other tests in a measured process.
//
// RESIZEJPEG_MEM_INPUT names an existing JPEG for mode "decode" (the default),
// or a fresh output path for mode "generate". With no path those modes skip.
// "baseline" (also accepted as "noinput") needs no input and performs no decode.
// Generation defaults to RESIZEJPEG_MEM_SIZE=20000x15000 and quality 80.
// Decode defaults to RESIZEJPEG_MEM_WIDTH=256 and RESIZEJPEG_MEM_WORKERS=2;
// target height is floor(sourceHeight*targetWidth/sourceWidth), at least one.
// The unlimited source-pixel budget is intentional for trusted giant fixtures,
// not a policy appropriate for untrusted input. Progressive input is explicitly
// permitted and its coefficient storage is included in the measurement.
//
// VmHWM is the Linux kernel's lifetime peak RSS for the WHOLE isolated test
// process, including the Go runtime, test machinery and sampling overhead. It
// is not incremental Perkeep memory and is not baseline-subtracted. Go heap
// peaks are sampled every millisecond, plus an endpoint snapshot; brief peaks
// between samples can be missed. HeapInuse and Sys are not RSS. No GOGC setting
// is changed. Neither compiler nor fixture-generator memory belongs in a
// measured invocation. There is no child process, native decoder, compressed
// input copy, full-size source image, or benchmark-fixture initialization here.
func TestThumbnailMemoryProbe(t *testing.T) {
	mode := os.Getenv("RESIZEJPEG_MEM_MODE")
	if mode == "" {
		mode = "decode"
	}
	if mode == "noinput" {
		mode = "baseline"
	}
	switch mode {
	case "baseline", "generate", "decode":
	default:
		t.Fatalf("invalid RESIZEJPEG_MEM_MODE %q: want baseline, noinput, generate, or decode", mode)
	}
	path := os.Getenv("RESIZEJPEG_MEM_INPUT")
	if mode != "baseline" && path == "" {
		t.Skip("set RESIZEJPEG_MEM_INPUT to opt in to the memory probe")
	}
	if mode == "generate" {
		size := os.Getenv("RESIZEJPEG_MEM_SIZE")
		if size == "" {
			size = "20000x15000"
		}
		w, h, err := memoryProbeParseSize(size)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		n, err := memoryProbeGenerate(path, w, h)
		if err != nil {
			t.Fatal(err)
		}
		// Generation is deliberately not reported as a memory measurement.
		memoryProbeLog(t, struct {
			Mode         string `json:"mode"`
			Input        string `json:"input"`
			SourceWidth  int    `json:"sourceWidth"`
			SourceHeight int    `json:"sourceHeight"`
			SourceBytes  int64  `json:"sourceBytes"`
			Quality      int    `json:"quality"`
			ElapsedNS    int64  `json:"elapsedNs"`
		}{mode, path, w, h, n, 80, time.Since(start).Nanoseconds()})
		return
	}
	if runtime.GOOS != "linux" {
		t.Skip("memory probe needs Linux /proc/self/status for process peak RSS")
	}

	record := memoryProbeRecord{
		Mode: mode, GoVersion: runtime.Version(), GOOS: runtime.GOOS,
		GOARCH: runtime.GOARCH, GOMAXPROCS: runtime.GOMAXPROCS(0),
		GOGCEnv: os.Getenv("GOGC"), GOMEMLIMITEnv: os.Getenv("GOMEMLIMIT"),
	}
	var f *os.File
	if mode == "decode" {
		var err error
		f, err = os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := DecodeConfig(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := memoryProbeDimensions(cfg.Width, cfg.Height); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		width, err := memoryProbeEnvInt("RESIZEJPEG_MEM_WIDTH", 256, 65535)
		if err != nil {
			t.Fatal(err)
		}
		if width > cfg.Width {
			t.Fatalf("target width %d exceeds source width %d", width, cfg.Width)
		}
		workers, err := memoryProbeEnvInt("RESIZEJPEG_MEM_WORKERS", 2, 64)
		if err != nil {
			t.Fatal(err)
		}
		record.Input, record.SourceBytes = path, fi.Size()
		record.SourceWidth, record.SourceHeight = cfg.Width, cfg.Height
		record.TargetWidth = width
		record.TargetHeight = max(1, int(uint64(cfg.Height)*uint64(width)/uint64(cfg.Width)))
		record.WorkersRequested = workers
		record.AllowFullDecode = true
		// MaxSourcePixels remains zero: these are intentionally trusted giants.
	}

	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	record.Baseline = memoryProbeHeapSnapshot(&baseline)
	var err error
	record.BaselineRSSKiB, record.BaselineHWMKiB, err = memoryProbeReadRSS()
	if err != nil {
		t.Fatal(err)
	}
	if mode == "baseline" {
		record.RSSKiB, record.PeakRSSKiB = record.BaselineRSSKiB, record.BaselineHWMKiB
		memoryProbeLog(t, record)
		return
	}

	stop := memoryProbeStartSampler(baseline)
	defer stop() // Always join, including if later assertions fail.
	record.SampleIntervalNS = int64(time.Millisecond)
	start := time.Now()
	im, d, decodeErr := decodeThumbnail(f, record.TargetWidth, record.TargetHeight, &ResizeOptions{
		Workers: record.WorkersRequested, AllowFullDecode: true, MaxSourcePixels: 0,
	})
	record.ElapsedNS = time.Since(start).Nanoseconds()
	samples := stop()
	record.Endpoint, record.SampledPeak, record.HeapSamples = samples.endpoint, samples.peak, samples.count
	record.RSSKiB, record.PeakRSSKiB, err = memoryProbeReadRSS()
	// Retain all owned worker/filter/progressive buffers through the endpoint
	// snapshot and RSS read, not merely the small returned thumbnail.
	runtime.KeepAlive(d)
	runtime.KeepAlive(im)
	if err != nil {
		t.Fatal(err)
	}
	if d != nil {
		record.Factor, record.Progressive, record.Components = d.factor, d.progressive, d.nComp
		if r := d.resize; r != nil {
			record.Streaming, record.Deferred, record.Fallback = r.streaming, r.deferred, r.fallback
			if r.pipeline != nil {
				record.WorkerCount = len(r.pipeline.workers)
			}
		}
	}
	if decodeErr != nil {
		record.Error = decodeErr.Error()
	}
	memoryProbeLog(t, record)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if im == nil || im.Bounds().Size() != image.Pt(record.TargetWidth, record.TargetHeight) {
		t.Fatalf("wrong thumbnail bounds: %v", im)
	}
}

type memoryProbeRecord struct {
	Mode             string          `json:"mode"`
	Input            string          `json:"input,omitempty"`
	GoVersion        string          `json:"goVersion"`
	GOOS             string          `json:"goos"`
	GOARCH           string          `json:"goarch"`
	GOMAXPROCS       int             `json:"gomaxprocs"`
	GOGCEnv          string          `json:"gogcEnv"`
	GOMEMLIMITEnv    string          `json:"gomemlimitEnv"`
	SourceWidth      int             `json:"sourceWidth"`
	SourceHeight     int             `json:"sourceHeight"`
	SourceBytes      int64           `json:"sourceBytes"`
	TargetWidth      int             `json:"targetWidth"`
	TargetHeight     int             `json:"targetHeight"`
	Factor           int             `json:"factor"`
	Progressive      bool            `json:"progressive"`
	Components       int             `json:"components"`
	WorkersRequested int             `json:"workersRequested"`
	WorkerCount      int             `json:"workerCount"`
	AllowFullDecode  bool            `json:"allowFullDecode"`
	MaxSourcePixels  uint64          `json:"maxSourcePixels"`
	Streaming        bool            `json:"streaming"`
	Deferred         bool            `json:"deferred"`
	Fallback         bool            `json:"fallback"`
	ElapsedNS        int64           `json:"elapsedNs"`
	SampleIntervalNS int64           `json:"sampleIntervalNs"`
	HeapSamples      uint64          `json:"heapSamples"`
	BaselineRSSKiB   uint64          `json:"baselineRSSKiB"`
	BaselineHWMKiB   uint64          `json:"baselineHWMKiB"`
	RSSKiB           uint64          `json:"rssKiB"`
	PeakRSSKiB       uint64          `json:"peakRSSKiB"`
	Baseline         memoryProbeHeap `json:"baseline"`
	Endpoint         memoryProbeHeap `json:"endpoint"`
	SampledPeak      memoryProbeHeap `json:"sampledPeak"`
	Error            string          `json:"error,omitempty"`
}

// All sizes in these Go snapshots are bytes; RSS fields explicitly use KiB.
// sampledPeak takes the independent maximum of each field, not one instant's
// mutually consistent snapshot. TotalAlloc is cumulative, NOT peak live heap.
type memoryProbeHeap struct {
	HeapAlloc   uint64 `json:"heapAlloc"`
	HeapInuse   uint64 `json:"heapInuse"`
	HeapSys     uint64 `json:"heapSys"`
	HeapObjects uint64 `json:"heapObjects"`
	StackInuse  uint64 `json:"stackInuse"`
	Sys         uint64 `json:"sys"`
	TotalAlloc  uint64 `json:"totalAlloc"`
	NumGC       uint32 `json:"numGC"`
}

func memoryProbeHeapSnapshot(m *runtime.MemStats) memoryProbeHeap {
	return memoryProbeHeap{
		HeapAlloc: m.HeapAlloc, HeapInuse: m.HeapInuse, HeapSys: m.HeapSys,
		HeapObjects: m.HeapObjects, StackInuse: m.StackInuse, Sys: m.Sys,
		TotalAlloc: m.TotalAlloc, NumGC: m.NumGC,
	}
}

type memoryProbeSamples struct {
	endpoint, peak memoryProbeHeap
	count          uint64
}

// The goroutine exclusively owns the maxima. The returned stop function is
// idempotent, takes a final sample, and joins before exposing those values.
func memoryProbeStartSampler(baseline runtime.MemStats) func() memoryProbeSamples {
	stop := make(chan struct{})
	done := make(chan struct{})
	ready := make(chan struct{})
	var result memoryProbeSamples
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		result.peak = memoryProbeHeapSnapshot(&baseline)
		sample := func() {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			h := memoryProbeHeapSnapshot(&m)
			result.endpoint = h
			p := &result.peak
			p.HeapAlloc = max(p.HeapAlloc, h.HeapAlloc)
			p.HeapInuse = max(p.HeapInuse, h.HeapInuse)
			p.HeapSys = max(p.HeapSys, h.HeapSys)
			p.HeapObjects = max(p.HeapObjects, h.HeapObjects)
			p.StackInuse = max(p.StackInuse, h.StackInuse)
			p.Sys = max(p.Sys, h.Sys)
			p.TotalAlloc = max(p.TotalAlloc, h.TotalAlloc)
			p.NumGC = max(p.NumGC, h.NumGC)
			result.count++
		}
		close(ready)
		for {
			select {
			case <-ticker.C:
				sample()
			case <-stop:
				sample()
				return
			}
		}
	}()
	<-ready // Install the ticker before the calling goroutine starts decoding.
	var once sync.Once
	return func() memoryProbeSamples {
		once.Do(func() {
			close(stop)
			<-done
		})
		return result
	}
}

func memoryProbeLog(t *testing.T, record any) {
	t.Helper()
	b, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MEMORY_JSON %s", b)
}

func memoryProbeEnvInt(name string, fallback, limit int) (int, error) {
	s := os.Getenv(name)
	if s == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > limit {
		return 0, fmt.Errorf("%s must be an integer in [1, %d], got %q", name, limit, s)
	}
	return n, nil
}

func memoryProbeDimensions(w, h int) error {
	if w < 1 || w > 65535 || h < 1 || h > 65535 {
		return fmt.Errorf("JPEG dimensions must be in [1, 65535], got %dx%d", w, h)
	}
	return nil
}

func memoryProbeParseSize(s string) (int, int, error) {
	a, b, ok := strings.Cut(s, "x")
	w, ew := strconv.Atoi(a)
	h, eh := strconv.Atoi(b)
	if !ok || ew != nil || eh != nil {
		return 0, 0, fmt.Errorf("RESIZEJPEG_MEM_SIZE must be WIDTHxHEIGHT, got %q", s)
	}
	if err := memoryProbeDimensions(w, h); err != nil {
		return 0, 0, err
	}
	return w, h, nil
}

// The source has constant storage independent of both dimensions. Each palette
// entry is boxed once at construction, never in At's hot per-pixel path.
type memoryProbeImage struct {
	rect        image.Rectangle
	palette     [256]color.Color
	transparent color.Color
}

var _ image.Image = (*memoryProbeImage)(nil)

func newMemoryProbeImage(w, h int) *memoryProbeImage {
	m := &memoryProbeImage{rect: image.Rect(0, 0, w, h), transparent: color.NRGBA{}}
	for i := range m.palette {
		m.palette[i] = color.YCbCr{
			Y: uint8(32 + (i&15)*13), Cb: uint8(64 + ((i>>4)&3)*42), Cr: uint8(64 + ((i>>6)&3)*42),
		}
	}
	return m
}

func (m *memoryProbeImage) ColorModel() color.Model { return color.YCbCrModel }
func (m *memoryProbeImage) Bounds() image.Rectangle { return m.rect }
func (m *memoryProbeImage) Opaque() bool            { return true }
func (m *memoryProbeImage) At(x, y int) color.Color {
	if !image.Pt(x, y).In(m.rect) {
		return m.transparent
	}
	// Coherent colored structure plus deterministic fine texture, neither a
	// constant/striped low-entropy fixture nor a full-resolution noise buffer.
	ux, uy := uint32(x), uint32(y)
	noise := ux*0x1f123bb5 ^ uy*0x5f356495
	noise ^= noise >> 15
	noise *= 0x85ebca6b
	index := ((ux/3 + uy/5 + ((ux ^ uy) >> 2) + (ux/16+uy/16)*67) & 255) ^ ((noise >> 24) & 31)
	return m.palette[index]
}

func memoryProbeGenerate(path string, w, h int) (size int64, err error) {
	if err := memoryProbeDimensions(w, h); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	if err := jpeg.Encode(f, newMemoryProbeImage(w, h), &jpeg.Options{Quality: 80}); err != nil {
		return 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func memoryProbeReadRSS() (rss, hwm uint64, err error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	return memoryProbeParseStatus(f)
}

func memoryProbeParseStatus(r io.Reader) (rss, hwm uint64, err error) {
	scanner := bufio.NewScanner(r)
	var haveRSS, haveHWM bool
	for scanner.Scan() {
		line := scanner.Text()
		key, value, _ := strings.Cut(line, ":")
		if key != "VmRSS" && key != "VmHWM" {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) != 2 || fields[1] != "kB" {
			return 0, 0, fmt.Errorf("invalid /proc/self/status entry %q", line)
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid /proc/self/status entry %q: %w", line, err)
		}
		if key == "VmRSS" {
			rss, haveRSS = n, true
		} else {
			hwm, haveHWM = n, true
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	if !haveRSS || !haveHWM {
		return 0, 0, fmt.Errorf("/proc/self/status is missing VmRSS or VmHWM")
	}
	return rss, hwm, nil
}

func TestMemoryProbeHelpers(t *testing.T) {
	t.Run("lazyImage", func(t *testing.T) {
		m := newMemoryProbeImage(37, 29)
		if m.Bounds() != image.Rect(0, 0, 37, 29) || m.ColorModel() != color.YCbCrModel || !m.Opaque() {
			t.Fatal("incorrect image bounds, color model or opacity")
		}
		colors := make(map[color.Color]bool)
		for y := 0; y < 29; y++ {
			for x := 0; x < 37; x++ {
				c := m.At(x, y)
				_, _, _, a := c.RGBA()
				if a != 65535 {
					t.Fatal("non-opaque in-bounds pixel")
				}
				if _, ok := c.(color.YCbCr); !ok {
					t.Fatalf("pixel is %T, want color.YCbCr", c)
				}
				colors[c] = true
			}
		}
		if len(colors) < 64 {
			t.Fatalf("insufficient color/texture variety: %d colors", len(colors))
		}
		var pixel color.Color
		x := 0
		if n := testing.AllocsPerRun(1000, func() {
			pixel = m.At(x%37, (x/37)%29)
			x++
		}); n != 0 {
			t.Fatalf("At allocated %g times per pixel, want zero", n)
		}
		runtime.KeepAlive(pixel)
		_, _, _, a := m.At(-1, -1).RGBA()
		if a != 0 {
			t.Fatal("out-of-bounds pixel is not transparent")
		}
	})
	t.Run("size", func(t *testing.T) {
		for _, s := range []string{"1x1", "20000x15000", "65535x65535"} {
			if _, _, err := memoryProbeParseSize(s); err != nil {
				t.Errorf("%q: %v", s, err)
			}
		}
		for _, s := range []string{"", "1", "1X1", "1x1x1", "0x1", "1x0", "-1x2", "65536x1", "1x65536", "999999999999999999999x1"} {
			if _, _, err := memoryProbeParseSize(s); err == nil {
				t.Errorf("accepted invalid size %q", s)
			}
		}
	})
	t.Run("generation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "tiny.jpg")
		n, err := memoryProbeGenerate(path, 37, 29)
		if err != nil {
			t.Fatal(err)
		}
		if n <= 0 {
			t.Fatal("empty JPEG")
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		im, err := jpeg.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		ycc, ok := im.(*image.YCbCr)
		if !ok || ycc.Bounds() != image.Rect(0, 0, 37, 29) || ycc.SubsampleRatio != image.YCbCrSubsampleRatio420 {
			t.Fatalf("want three-component 4:2:0 37x29 JPEG, got %T", im)
		}
		if _, err := memoryProbeGenerate(path, 37, 29); !os.IsExist(err) {
			t.Fatalf("overwrite error = %v, want os.ErrExist", err)
		}
		fi, err := os.Stat(path)
		if err != nil || fi.Size() != n {
			t.Fatalf("original JPEG changed on overwrite attempt: %v", err)
		}
		if _, err := memoryProbeGenerate(filepath.Join(t.TempDir(), "bad.jpg"), 0, 1); err == nil {
			t.Fatal("accepted zero width")
		}
	})
	t.Run("procStatus", func(t *testing.T) {
		rss, hwm, err := memoryProbeParseStatus(strings.NewReader("Name:\ttest\nVmHWM:\t12345 kB\nVmRSS:\t6789 kB\nThreads:\t2\n"))
		if err != nil || rss != 6789 || hwm != 12345 {
			t.Fatalf("RSS=%d HWM=%d error=%v", rss, hwm, err)
		}
		for _, s := range []string{"", "VmHWM: 1 kB\n", "VmHWM: -1 kB\nVmRSS: 2 kB\n", "VmHWM: 1 MB\nVmRSS: 2 kB\n", "VmHWM: 1 kB\nVmRSS: bad kB\n"} {
			if _, _, err := memoryProbeParseStatus(strings.NewReader(s)); err == nil {
				t.Errorf("accepted invalid status %q", s)
			}
		}
	})
	t.Run("samplerJoin", func(t *testing.T) {
		var baseline runtime.MemStats
		runtime.ReadMemStats(&baseline)
		stop := memoryProbeStartSampler(baseline)
		result := stop()
		if result.count < 1 || result.peak.HeapAlloc < baseline.HeapAlloc || result.peak.HeapAlloc < result.endpoint.HeapAlloc {
			t.Fatalf("invalid samples: %+v", result)
		}
		if stop() != result {
			t.Fatal("repeated stop changed the joined result")
		}
	})
}
