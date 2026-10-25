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
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"
)

type memoryOptionsFixture struct {
	name   string
	data   []byte
	target image.Point
	full   bool
}

func memoryOptionsFixtures(t testing.TB) []memoryOptionsFixture {
	t.Helper()
	// Retain valid separate scans but select unequal chroma factors. For this
	// 8x8 frame each separate scan still contains exactly one visible block.
	flex := bytes.Clone(resizeStreamSeparateScans())
	_, sof, _ := resizeStreamSegment(t, flex, 0xc0)
	flex[sof+11], flex[sof+14] = 0x22, 0x12
	// No Adobe marker at the first SOS forces full-buffer CMYK fallback;
	// metadata before EOI supplies the color interpretation in time to return.
	cmyk := resizeStreamConstantCMYK(0, true)
	_, app, appEnd := resizeStreamSegment(t, cmyk, 0xee)
	lateAdobe := append(bytes.Clone(cmyk[:app]), cmyk[appEnd:len(cmyk)-2]...)
	lateAdobe = append(lateAdobe, cmyk[app:appEnd]...)
	lateAdobe = append(lateAdobe, 0xff, 0xd9)
	return []memoryOptionsFixture{
		{"baseline", resizeStreamEncode(t, resizeStreamPattern(513, 385, false)), image.Pt(32, 24), false},
		{"gray", readJPEG(t, "testdata/video-005.gray.jpeg"), image.Pt(17, 11), false},
		{"rgb", readJPEG(t, "testdata/video-001.rgb.jpeg"), image.Pt(17, 11), false},
		{"flex-stream", readJPEG(t, "testdata/video-001.221212.jpeg"), image.Pt(17, 11), false},
		{"CMYK", resizeStreamConstantCMYK(0, true), image.Pt(5, 3), false},
		{"YCCK", resizeStreamConstantCMYK(2, true), image.Pt(5, 3), false},
		{"progressive", readJPEG(t, "testdata/video-001.progressive.jpeg"), image.Pt(17, 11), true},
		{"separate", resizeStreamSeparateScans(), image.Pt(1, 1), true},
		{"flex-fallback", flex, image.Pt(1, 1), true},
		{"CMYK-fallback", lateAdobe, image.Pt(5, 3), true},
	}
}

// Count the major payloads actually retained by a no-hook decode. This is a
// lower bound, not another copy of the admission formula: temporary conversion
// images, allocation headers, channels, structs, and spans are not included.
func memoryOptionsOwnedPayload(d *decoder, output *image.RGBA) int64 {
	planes := func(d *decoder) int64 {
		n := int64(len(d.blackPix))
		if d.img1 != nil {
			n += int64(len(d.img1.Pix))
		}
		if d.img3 != nil {
			n += int64(len(d.img3.Y) + len(d.img3.Cb) + len(d.img3.Cr))
		}
		for ci := range d.progCoeffs {
			n += int64(len(d.progCoeffs[ci]))*4 + int64(len(d.progNonzero[ci]))*8
		}
		return n
	}
	n := int64(len(output.Pix)) + planes(d)
	if f := d.resize.filter; f != nil {
		n += int64(len(f.xWeights)+len(f.sums)) * 8
	}
	if p := d.resize.pipeline; p != nil {
		for _, w := range p.workers {
			n += planes(&w.d) + int64(len(w.rgba.Pix))
		}
		for _, b := range p.bands {
			n += int64(len(b.rows)) * 8
			for _, coeffs := range b.coeffs {
				n += int64(len(coeffs)) * 256
			}
		}
	}
	return n
}

func memoryOptionsAssertDropped(t testing.TB, d *decoder) {
	t.Helper()
	resizeStreamAssertNoSourceBuffers(t, d)
	if d.resize.filter != nil || d.resize.pipeline != nil {
		t.Fatal("reservation released while decoder retained filter or worker scratch")
	}
}

func TestResizeMemoryOptionsInvalidBeforeRead(t *testing.T) {
	for _, api := range resizeOptionsAPIs {
		for _, limit := range []int64{-1, -1 << 63} {
			t.Run(fmt.Sprintf("%s/%d", api.name, limit), func(t *testing.T) {
				r := new(resizeStreamNoRead)
				calls := 0
				opts := &ResizeOptions{MaxMemoryBytes: limit, MemAllocSemaphore: func(int64) (func(), error) {
					calls++
					return nil, nil
				}}
				got, err := api.public(r, 1, 1, opts)
				var unsupported UnsupportedError
				if got != nil || !errors.As(err, &unsupported) || r.reads != 0 || calls != 0 {
					t.Fatalf("image %v error %v reads %d hook calls %d; want rejection before Read", got, err, r.reads, calls)
				}
				if opts.MaxMemoryBytes != limit {
					t.Fatal("modified caller memory limit")
				}
			})
		}
	}
}

func TestResizeMemoryOptionsAdmissionMatrix(t *testing.T) {
	denied := errors.New("memory reservation denied")
	for _, tc := range memoryOptionsFixtures(t) {
		for _, api := range resizeOptionsAPIs {
			for _, filter := range resizeOptionsFilters {
				for _, workers := range []int{1, 4} {
					t.Run(fmt.Sprintf("%s/%s/%s/workers=%d", tc.name, api.name, filter, workers), func(t *testing.T) {
						opts := ResizeOptions{Workers: workers, Filter: filter, ThumbnailHeadroom: 2, AllowFullDecode: tc.full}
						want, d, err := api.decode(bytes.NewReader(tc.data), tc.target.X, tc.target.Y, &opts)
						if err != nil {
							t.Fatal(err)
						}
						resizeStreamAssertJoined(t, d)
						payload := memoryOptionsOwnedPayload(d, want)
						_, _, sosEnd := resizeStreamSegment(t, tc.data, 0xda)
						r := &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(tc.data))}
						var plan int64
						calls, releases := 0, 0
						opts.MemAllocSemaphore = func(n int64) (func(), error) {
							calls++
							plan = n
							if r.n != sosEnd {
								t.Errorf("hook after %d input bytes, want first SOS end %d", r.n, sosEnd)
							}
							// An error never grants a lease, even if a release is returned.
							return func() { releases++ }, denied
						}
						got, rejected, err := api.decode(r, tc.target.X, tc.target.Y, &opts)
						if got != nil || err != denied || calls != 1 || releases != 0 || r.n != sosEnd {
							t.Fatalf("denied: image %v error %v calls/releases %d/%d input %d/%d", got, err, calls, releases, r.n, sosEnd)
						}
						memoryOptionsAssertDropped(t, rejected)
						if plan < payload {
							t.Fatalf("plan %d does not cover retained payload %d", plan, payload)
						}
						if plan <= int64(len(want.Pix)) {
							t.Fatalf("plan %d excludes scratch", plan)
						}
						// The local cap is checked before the external semaphore.
						opts.MaxMemoryBytes = plan - 1
						r = &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(tc.data))}
						got, rejected, err = api.decode(r, tc.target.X, tc.target.Y, &opts)
						var unsupported UnsupportedError
						if got != nil || !errors.As(err, &unsupported) || calls != 1 || r.n != sosEnd {
							t.Fatalf("cap-1: image %v error %v hook calls %d input %d/%d", got, err, calls, r.n, sosEnd)
						}
						memoryOptionsAssertDropped(t, rejected)
						// Equality is admitted, including a nil/no-op release.
						for _, limit := range []int64{0, plan, plan + 1} {
							calls, releases = 0, 0
							opts.MaxMemoryBytes = limit
							opts.MemAllocSemaphore = func(n int64) (func(), error) {
								calls++
								if n != plan {
									t.Errorf("plan changed from %d to %d", plan, n)
								}
								if limit == plan+1 {
									return nil, nil
								}
								return func() { releases++; runtime.GC() }, nil
							}
							got, d, err = api.decode(bytes.NewReader(tc.data), tc.target.X, tc.target.Y, &opts)
							wantReleases := 1
							if limit == plan+1 {
								wantReleases = 0
							}
							if err != nil || calls != 1 || releases != wantReleases {
								t.Fatalf("cap %d: error %v calls/releases %d/%d", limit, err, calls, releases)
							}
							if got == nil || !samePixels(got, want) {
								t.Fatalf("cap %d or release GC changed pixels", limit)
							}
							if opts.MemAllocSemaphore == nil || opts.MaxMemoryBytes != limit {
								t.Fatal("decoder modified caller memory options")
							}
							memoryOptionsAssertDropped(t, d)
						}
						// A local cap also works with no semaphore at all.
						opts.MemAllocSemaphore, opts.MaxMemoryBytes = nil, plan
						got, err = api.public(bytes.NewReader(tc.data), tc.target.X, tc.target.Y, &opts)
						if err != nil || !samePixels(got, want) {
							t.Fatalf("cap without semaphore: %v", err)
						}
					})
				}
			}
		}
	}
}

func TestResizeMemoryOptionsDefaultsAndPlanChoices(t *testing.T) {
	data := resizeStreamEncode(t, resizeStreamPattern(513, 385, false))
	for _, api := range resizeOptionsAPIs {
		t.Run(api.name, func(t *testing.T) {
			want, err := api.public(bytes.NewReader(data), 32, 24, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, opts := range []ResizeOptions{{}, {ThumbnailHeadroom: 4}, {Workers: 1}, {Workers: 4}} {
				opts.MemAllocSemaphore = func(int64) (func(), error) { return func() { runtime.GC() }, nil }
				got, err := api.public(bytes.NewReader(data), 32, 24, &opts)
				if err != nil || !samePixels(got, want) {
					t.Fatalf("memory hook changed default pixels: %v", err)
				}
			}
			plan := func(filter Filter, workers, headroom int) int64 {
				var estimate int64
				stop := errors.New("estimate only")
				_, err := api.public(bytes.NewReader(data), 32, 24, &ResizeOptions{
					Filter: filter, Workers: workers, ThumbnailHeadroom: headroom,
					MemAllocSemaphore: func(n int64) (func(), error) { estimate = n; return nil, stop },
				})
				if err != stop || estimate <= 0 {
					t.Fatalf("estimate-only error %v, plan %d", err, estimate)
				}
				return estimate
			}
			one, four := plan(Lanczos3, 1, 4), plan(Lanczos3, 4, 4)
			if four <= one {
				t.Fatalf("workers 1/4 plans %d/%d do not account for extra workers", one, four)
			}
			linear, cubic := plan(Bilinear, 1, 4), plan(CatmullRom, 1, 4)
			if !(linear < cubic && cubic < one) {
				t.Fatalf("filter plans Bilinear/CatmullRom/Lanczos3 %d/%d/%d", linear, cubic, one)
			}
			two := plan(Lanczos3, 1, 2)
			if api.thumbnail && two >= one {
				t.Fatalf("headroom 2/4 plans %d/%d do not reflect reduced buffers", two, one)
			}
			if !api.thumbnail && two != one {
				t.Fatalf("DecodeResize headroom changed plan %d/%d", two, one)
			}
		})
	}
}

func TestResizeMemoryOptionsUnsupportedBeforeHook(t *testing.T) {
	baseline := resizeStreamEncode(t, resizeStreamPattern(33, 25, false))
	progressive := readJPEG(t, "testdata/video-001.progressive.jpeg")
	separate := resizeStreamSeparateScans()
	badSOS := bytes.Clone(baseline)
	_, sos, _ := resizeStreamSegment(t, badSOS, 0xda)
	badSOS[sos+5] = 0xfe // Component selector absent from the frame.
	badRGB := bytes.Clone(readJPEG(t, "testdata/video-001.rgb.jpeg"))
	_, sof, _ := resizeStreamSegment(t, badRGB, 0xc0)
	badRGB[sof+14] = 0x12 // RGB cannot use unequal chroma sampling factors.
	huge := bytes.Clone(baseline)
	_, sof, _ = resizeStreamSegment(t, huge, 0xc0)
	for _, offset := range []int{5, 6, 7, 8} {
		huge[sof+offset] = 0xff
	}
	for _, api := range resizeOptionsAPIs {
		for _, tc := range []struct {
			name   string
			data   []byte
			target image.Point
			opts   ResizeOptions
		}{
			{"dimensions", baseline, image.Pt(0, 1), ResizeOptions{}},
			{"workers", baseline, image.Pt(1, 1), ResizeOptions{Workers: 65}},
			{"filter", baseline, image.Pt(1, 1), ResizeOptions{Filter: Filter(-1)}},
			{"headroom", baseline, image.Pt(1, 1), ResizeOptions{ThumbnailHeadroom: -1}},
			{"upsampling", baseline, image.Pt(34, 25), ResizeOptions{}},
			{"source-budget", baseline, image.Pt(1, 1), ResizeOptions{MaxSourcePixels: 1}},
			{"progressive", progressive, image.Pt(1, 1), ResizeOptions{}},
			{"noninterleaved", separate, image.Pt(1, 1), ResizeOptions{}},
			{"missing-SOI", []byte("not a JPEG"), image.Pt(1, 1), ResizeOptions{}},
			{"invalid-SOS", badSOS, image.Pt(1, 1), ResizeOptions{}},
			{"unsupported-RGB", badRGB, image.Pt(1, 1), ResizeOptions{}},
			{"huge-output", huge, image.Pt(65535, 65535), ResizeOptions{MaxMemoryBytes: 1}},
		} {
			t.Run(api.name+"/"+tc.name, func(t *testing.T) {
				calls := 0
				tc.opts.MemAllocSemaphore = func(int64) (func(), error) { calls++; return nil, nil }
				got, d, err := api.decode(iotest.OneByteReader(bytes.NewReader(tc.data)), tc.target.X, tc.target.Y, &tc.opts)
				if got != nil || err == nil || calls != 0 {
					t.Fatalf("image %v error %v hook calls %d; want rejection before admission", got, err, calls)
				}
				if d != nil {
					memoryOptionsAssertDropped(t, d)
				}
			})
		}
	}
}

func TestResizeMemoryOptionsReleaseOnDecodeErrors(t *testing.T) {
	readErr := errors.New("input failed after admission")
	for _, tc := range memoryOptionsFixtures(t) {
		_, sos, sosEnd := resizeStreamSegment(t, tc.data, 0xda)
		lateScan := append(bytes.Clone(tc.data[:len(tc.data)-2]), tc.data[sos:sosEnd]...)
		lateScan = append(lateScan, 0xff, 0xd9)
		for _, api := range resizeOptionsAPIs {
			for _, failure := range []struct {
				name   string
				reader func() io.Reader
				want   error
			}{
				{"no-entropy", func() io.Reader { return bytes.NewReader(tc.data[:sosEnd]) }, nil},
				{"partial-entropy", func() io.Reader { return bytes.NewReader(tc.data[:sosEnd+(len(tc.data)-sosEnd)/2]) }, nil},
				{"missing-EOI", func() io.Reader { return bytes.NewReader(tc.data[:len(tc.data)-2]) }, nil},
				{"reader-error", func() io.Reader { return io.MultiReader(bytes.NewReader(tc.data[:sosEnd]), iotest.ErrReader(readErr)) }, readErr},
				{"late-SOS", func() io.Reader { return bytes.NewReader(lateScan) }, nil},
			} {
				t.Run(tc.name+"/"+api.name+"/"+failure.name, func(t *testing.T) {
					calls, releases := 0, 0
					got, d, err := api.decode(failure.reader(), tc.target.X, tc.target.Y, &ResizeOptions{
						Workers: 4, AllowFullDecode: tc.full,
						MemAllocSemaphore: func(n int64) (func(), error) {
							calls++
							if n <= 0 {
								t.Errorf("non-positive reservation %d", n)
							}
							return func() { releases++; runtime.GC() }, nil
						},
					})
					if got != nil || err == nil || calls != 1 || releases != 1 {
						t.Fatalf("image %v error %v calls/releases %d/%d; want error and one released lease", got, err, calls, releases)
					}
					if failure.want != nil && err != failure.want {
						t.Fatalf("reader error %v, want exact %v", err, failure.want)
					}
					memoryOptionsAssertDropped(t, d)
				})
			}
		}
	}
}

// Atomic counters permit observation while a hook is blocked, without races.
// Reads are deliberately one byte, preventing decoder read-ahead from hiding
// entropy consumption before acquisition. The optional gate stalls the first
// post-SOS read, holding a successful reservation until explicitly permitted.
type memoryOptionsReader struct {
	r         *bytes.Reader
	sosEnd    int64
	read      atomic.Int64
	entropy   atomic.Int64
	ctx       context.Context
	gate      <-chan struct{}
	atEntropy chan<- struct{}
	signaled  bool // Only the decoding goroutine accesses this field.
}

func (r *memoryOptionsReader) Read(p []byte) (int, error) {
	if r.read.Load() >= r.sosEnd {
		r.entropy.Add(1)
		if r.gate != nil && !r.signaled {
			r.signaled = true
			if r.atEntropy != nil {
				r.atEntropy <- struct{}{}
			}
			select {
			case <-r.gate:
			case <-r.ctx.Done():
				return 0, r.ctx.Err()
			}
		}
	}
	if len(p) > 1 {
		p = p[:1]
	}
	n, err := r.r.Read(p)
	r.read.Add(int64(n))
	return n, err
}

type memoryOptionsResult struct {
	id    int
	image *image.RGBA
	d     *decoder
	err   error
}

func memoryOptionsReceive[T any](t testing.TB, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for memory admission barrier")
		var zero T
		return zero
	}
}

func TestResizeMemoryOptionsCancellationWhileWaiting(t *testing.T) {
	data := resizeStreamEncode(t, resizeStreamPattern(257, 193, false))
	_, _, sosEnd := resizeStreamSegment(t, data, 0xda)
	for _, api := range resizeOptionsAPIs {
		t.Run(api.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			r := &memoryOptionsReader{r: bytes.NewReader(data), sosEnd: int64(sosEnd)}
			waiting := make(chan int64, 2)
			done := make(chan memoryOptionsResult, 1)
			finished := make(chan struct{})
			t.Cleanup(func() {
				cancel()
				memoryOptionsReceive(t, finished)
			})
			var releases atomic.Int64
			go func() {
				defer close(finished)
				got, d, err := api.decode(r, 17, 11, &ResizeOptions{Workers: 4,
					MemAllocSemaphore: func(n int64) (func(), error) {
						waiting <- n
						<-ctx.Done()
						return func() { releases.Add(1) }, ctx.Err()
					},
				})
				done <- memoryOptionsResult{image: got, d: d, err: err}
			}()
			if n := memoryOptionsReceive(t, waiting); n <= 0 {
				t.Fatalf("reservation %d, want positive", n)
			}
			if r.read.Load() != int64(sosEnd) || r.entropy.Load() != 0 {
				t.Fatalf("waiting hook consumed input: read %d entropy reads %d", r.read.Load(), r.entropy.Load())
			}
			select {
			case <-done:
				t.Fatal("decode returned before callback cancellation")
			default:
			}
			cancel()
			result := memoryOptionsReceive(t, done)
			if result.image != nil || result.err != context.Canceled || releases.Load() != 0 {
				t.Fatalf("image %v error %v releases %d; want exact cancellation without release", result.image, result.err, releases.Load())
			}
			if r.read.Load() != int64(sosEnd) || r.entropy.Load() != 0 {
				t.Fatal("failed acquisition allowed entropy decoding")
			}
			memoryOptionsAssertDropped(t, result.d)
			select {
			case <-waiting:
				t.Fatal("hook called again after cancellation")
			default:
			}
		})
	}
}

func TestResizeMemoryOptionsParallelSemaphore(t *testing.T) {
	data := resizeStreamEncode(t, resizeStreamPattern(513, 385, false))
	_, _, sosEnd := resizeStreamSegment(t, data, 0xda)
	const count = 3
	for _, api := range resizeOptionsAPIs {
		t.Run(api.name, func(t *testing.T) {
			want, err := api.public(bytes.NewReader(data), 32, 24, &ResizeOptions{Workers: 4})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			// The capacity is a byte budget for exactly one of these identical
			// requests, represented by one token. Waiting calls cannot decode.
			token := make(chan struct{}, 1)
			token <- struct{}{}
			entered, acquired := make(chan int, count*2), make(chan int, count*2)
			done := make(chan memoryOptionsResult, count)
			var wg sync.WaitGroup
			t.Cleanup(func() {
				cancel()
				finished := make(chan struct{})
				go func() { wg.Wait(); close(finished) }()
				memoryOptionsReceive(t, finished)
			})
			var calls, releases [count]atomic.Int64
			var live, plan, peak atomic.Int64
			var readers [count]*memoryOptionsReader
			var gates [count]chan struct{}
			var entropy [count]chan struct{}
			for id := range count {
				gates[id], entropy[id] = make(chan struct{}), make(chan struct{}, 1)
				readers[id] = &memoryOptionsReader{r: bytes.NewReader(data), sosEnd: int64(sosEnd), ctx: ctx, gate: gates[id], atEntropy: entropy[id]}
				wg.Add(1)
				go func() {
					defer wg.Done()
					got, d, err := api.decode(readers[id], 32, 24, &ResizeOptions{Workers: 4,
						MemAllocSemaphore: func(n int64) (func(), error) {
							calls[id].Add(1)
							entered <- id
							select {
							case <-ctx.Done():
								return nil, ctx.Err()
							case <-token:
							}
							plan.CompareAndSwap(0, n)
							if n != plan.Load() {
								t.Errorf("parallel request plan %d, want %d", n, plan.Load())
							}
							current := live.Add(n)
							for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
							}
							acquired <- id
							return func() {
								releases[id].Add(1)
								runtime.GC()
								live.Add(-n)
								select {
								case token <- struct{}{}:
								default:
									t.Error("reservation released more than once")
								}
							}, nil
						},
					})
					done <- memoryOptionsResult{id: id, image: got, d: d, err: err}
				}()
			}
			seen := make(map[int]bool)
			var outputs []*image.RGBA
			for range count {
				id := memoryOptionsReceive(t, entered)
				if seen[id] {
					t.Fatalf("call %d acquired more than once", id)
				}
				seen[id] = true
			}
			for turn := range count {
				id := memoryOptionsReceive(t, acquired)
				memoryOptionsReceive(t, entropy[id])
				if live.Load() != plan.Load() || live.Load() <= 0 {
					t.Fatalf("active reservation %d, want one plan %d", live.Load(), plan.Load())
				}
				// First request is intentionally paused in its reader. All
				// other requests reached the hook, but cannot consume entropy.
				if turn == 0 {
					for other := range count {
						if other != id && readers[other].entropy.Load() != 0 {
							t.Fatalf("unadmitted call %d read entropy", other)
						}
					}
				}
				select {
				case other := <-acquired:
					t.Fatalf("call %d acquired while %d still held the byte budget", other, id)
				default:
				}
				close(gates[id])
				result := memoryOptionsReceive(t, done)
				if result.id != id || result.err != nil || result.image == nil || !samePixels(result.image, want) {
					t.Fatalf("call %d result: id %d error %v", id, result.id, result.err)
				}
				if calls[id].Load() != 1 || releases[id].Load() != 1 {
					t.Fatalf("call %d calls/releases %d/%d", id, calls[id].Load(), releases[id].Load())
				}
				// Retain every returned image while later calls reuse the same
				// budget. Reservations do not track caller-owned output lifetime.
				outputs = append(outputs, result.image)
				memoryOptionsAssertDropped(t, result.d)
			}
			if peak.Load() != plan.Load() || live.Load() != 0 {
				t.Fatalf("peak/live reservation %d/%d, want %d/0", peak.Load(), live.Load(), plan.Load())
			}
			runtime.GC()
			for _, output := range outputs {
				if !samePixels(output, want) {
					t.Fatal("reusing reservations corrupted a caller-owned output")
				}
			}
		})
	}
}

// Public helpers intentionally erase scratch on release, so retain a pipeline
// pointer in this white-box lifecycle test. The same first-SOS admission and
// releaseMemory methods are used by both public APIs. This checks ordering,
// not merely that buffers happen to be absent after the API has returned.
func TestResizeMemoryOptionsAdmissionAndReleaseOrdering(t *testing.T) {
	for _, tc := range memoryOptionsFixtures(t) {
		for _, api := range resizeOptionsAPIs {
			for _, truncated := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/truncated=%v", tc.name, api.name, truncated), func(t *testing.T) {
					r := &resizeRequest{width: tc.target.X, height: tc.target.Y, thumbnail: api.thumbnail,
						opts: ResizeOptions{Workers: 4, ThumbnailHeadroom: 2, AllowFullDecode: tc.full},
					}
					d := &decoder{factor: 1, resize: r}
					var pipeline *resizePipeline
					calls, releases := 0, 0
					r.opts.MemAllocSemaphore = func(int64) (func(), error) {
						calls++
						resizeStreamAssertNoSourceBuffers(t, d)
						if r.filter != nil || r.pipeline != nil {
							t.Error("filter/workers allocated before admission")
						}
						if r.scans != 1 || r.fallback != tc.full || r.deferred != (tc.full && api.thumbnail && d.progressive) {
							t.Errorf("scan path not selected before admission: scans=%d fallback=%v deferred=%v", r.scans, r.fallback, r.deferred)
						}
						return func() {
							releases++
							if pipeline != nil {
								if !pipeline.joined {
									t.Error("reservation released before workers joined")
								}
								select {
								case <-pipeline.done:
								default:
									t.Error("reservation released before collector joined")
								}
							}
							memoryOptionsAssertDropped(t, d)
							if d.r != nil {
								t.Error("release retained caller reader")
							}
							runtime.GC()
						}, nil
					}
					defer r.releaseMemory(d)
					data := tc.data
					if truncated {
						data = data[:len(data)-2]
					}
					im, err := d.decode(bytes.NewReader(data), false)
					pipeline = r.pipeline
					if truncated {
						if im != nil || err == nil {
							t.Fatalf("truncation returned image %v error %v", im, err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					if calls != 1 || releases != 0 {
						t.Fatalf("before return: calls/releases %d/%d; want one held lease", calls, releases)
					}
					resizeStreamAssertJoined(t, d)
					r.releaseMemory(d)
					if releases != 1 {
						t.Fatalf("released %d times, want once", releases)
					}
					// Cleanup itself must be idempotent, including a repeated defer.
					r.releaseMemory(d)
					if releases != 1 {
						t.Fatalf("repeated cleanup released %d times", releases)
					}
				})
			}
		}
	}
}

type memoryOptionsPanicReader struct{ value any }

func (r memoryOptionsPanicReader) Read([]byte) (int, error) { panic(r.value) }

func TestResizeMemoryOptionsReleaseOnReaderPanic(t *testing.T) {
	sentinel := errors.New("reader panicked after admission")
	for _, tc := range memoryOptionsFixtures(t) {
		_, _, sosEnd := resizeStreamSegment(t, tc.data, 0xda)
		for _, api := range resizeOptionsAPIs {
			t.Run(tc.name+"/"+api.name, func(t *testing.T) {
				calls, releases := 0, 0
				var recovered any
				func() {
					defer func() { recovered = recover() }()
					_, _ = api.public(io.MultiReader(
						bytes.NewReader(tc.data[:sosEnd]), memoryOptionsPanicReader{sentinel},
					), tc.target.X, tc.target.Y, &ResizeOptions{
						Workers: 4, AllowFullDecode: tc.full,
						MemAllocSemaphore: func(int64) (func(), error) {
							calls++
							return func() { releases++ }, nil
						},
					})
				}()
				if recovered != sentinel || calls != 1 || releases != 1 {
					t.Fatalf("panic %v calls/releases %d/%d; want original panic and one released lease", recovered, calls, releases)
				}
			})
		}
	}
}

func FuzzResizeMemoryOptions(f *testing.F) {
	for _, data := range [][]byte{
		resizeStreamEncode(f, resizeStreamPattern(257, 193, false)),
		readJPEG(f, "testdata/video-001.progressive.jpeg"),
		resizeStreamConstantCMYK(2, true),
		resizeStreamSeparateScans(),
	} {
		for mode := byte(0); mode < 8; mode++ {
			f.Add(data, mode, uint32(7|5<<16), int64(8<<20))
		}
		f.Add(data, byte(0), uint32(1), int64(1))
	}
	f.Fuzz(func(t *testing.T, data []byte, mode byte, size uint32, limit int64) {
		if len(data) > 1<<20 {
			return
		}
		// Bound the fuzzer itself. Exercise rejection with small/negative
		// caps, but never allow an unlimited or large successful reservation.
		if limit > 8<<20 || limit == 0 {
			limit = 8 << 20
		}
		api := resizeOptionsAPIs[int(mode)%2]
		denied := errors.New("fuzz admission denied")
		calls, releases := 0, 0
		opts := &ResizeOptions{
			Workers: 1 + int(mode>>1)%4, AllowFullDecode: true,
			Filter: Filter(mode>>3) % 3, ThumbnailHeadroom: 1 + int(mode>>5)%4,
			MaxSourcePixels: 1024 * 1024, MaxMemoryBytes: limit,
			MemAllocSemaphore: func(n int64) (func(), error) {
				calls++
				if calls != 1 || n <= 0 || n > limit {
					t.Fatalf("invalid reservation: calls=%d bytes=%d limit=%d", calls, n, limit)
				}
				if mode&4 != 0 {
					return nil, denied
				}
				return func() { releases++ }, nil
			},
		}
		width, height := 1+int(size&0xffff)%32, 1+int(size>>16)%32
		im, d, err := api.decode(bytes.NewReader(data), width, height, opts)
		wantReleases := calls
		if mode&4 != 0 {
			wantReleases = 0
			if calls != 0 && err != denied {
				t.Fatalf("admission error changed: %v", err)
			}
		}
		if calls > 1 || releases != wantReleases {
			t.Fatalf("unbalanced reservation: calls=%d releases=%d want=%d", calls, releases, wantReleases)
		}
		if err != nil && im != nil {
			t.Fatal("error returned a partial image")
		}
		if err == nil && (calls != 1 || im == nil || im.Bounds().Size() != image.Pt(width, height)) {
			t.Fatal("successful decode without admitted reservation or correct output")
		}
		if d != nil {
			memoryOptionsAssertDropped(t, d)
		}
	})
}
