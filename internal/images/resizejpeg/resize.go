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
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"runtime"
	"sync"
)

// ResizeOptions controls [DecodeResize] and [DecodeThumbnail].
// A nil pointer uses the zero values.
type ResizeOptions struct {
	// Filter selects the final scale-aware resampling filter.
	// The zero value is Lanczos3. CatmullRom uses a narrower cubic kernel;
	// Bilinear uses a cheaper, softer triangle kernel. All three widen
	// their support when reducing, rather than sampling a fixed few pixels.
	Filter Filter

	// ThumbnailHeadroom controls DecodeThumbnail's intermediate dimensions.
	// Zero uses 4. Positive values retain at least that multiple of each
	// target dimension when the source permits, choosing an IDCT factor
	// from 8, 4, 2, 1. A value of 2 commonly processes about four times
	// fewer intermediate pixels than 4, at some loss of fine detail.
	// Factor limits and source dimensions can prevent that reduction.
	// DecodeResize ignores positive values and always performs full IDCT.
	ThumbnailHeadroom int

	// Workers is the number of IDCT/RGB/horizontal-filter workers.
	// Zero uses min(GOMAXPROCS, 8); explicit values must be in [1, 64].
	// The number is further limited by the number of source MCU rows.
	// Entropy decoding and ordered vertical filtering remain serial.
	Workers int

	// AllowFullDecode permits image-wide buffers for progressive images and
	// sequential scans that do not contain all components.
	// By default those inputs return UnsupportedError before allocating
	// source image planes or progressive coefficient arrays.
	//
	// DecodeResize's fallback uses full-resolution pixels and coefficients.
	// DecodeThumbnail uses its chosen reduced factor for fallback pixels
	// and compact progressive coefficients; progressive reconstruction still
	// uses row-band workers, without image-wide pixel planes. Neither
	// fallback preserves the streaming path's bounded-memory guarantee.
	AllowFullDecode bool

	// MaxSourcePixels limits width*height before allocating source buffers.
	// Zero imposes no pixel budget. This is not a total memory/CPU limit:
	// callers should also bound concurrent requests and compressed input.
	MaxSourcePixels uint64

	// MaxMemoryBytes limits the conservative memory reservation for one
	// decode. Zero imposes no limit; negative values are invalid. The plan
	// includes output pixels, temporary buffers, and an overhead allowance.
	// An over-budget decode returns UnsupportedError before allocating
	// those buffers or calling MemAllocSemaphore.
	//
	// This is not a hard Go heap or process RSS limit. It excludes the
	// reader's storage, caller-retained images, and runtime/GC overhead.
	MaxMemoryBytes int64

	// MemAllocSemaphore optionally admits a decode to a shared memory budget.
	// It is called synchronously, at most once, after the first scan header
	// determines the buffer plan and before the large allocations or workers
	// start. Its argument is the same reservation checked by MaxMemoryBytes.
	// The function may wait for capacity; capturing a request context lets
	// callers cancel that wait. An error is returned unchanged and must not
	// leave a reservation held.
	//
	// After success, a non-nil release function is called exactly once on
	// every exit, after workers stop and temporary references are dropped.
	// A nil release is allowed. No further capacity is acquired while this
	// reservation is held, including for later progressive scans.
	//
	// The reservation includes the destination during decoding. On return,
	// ownership of the destination transfers to the caller; the hook does
	// not budget images retained by callers, encoders, or caches. Garbage
	// collection may reclaim scratch space later than the release call.
	MemAllocSemaphore func(bytes int64) (release func(), err error)
}

// DecodeResize fully decodes JPEG samples and resizes them with the selected
// scale-aware filter (Lanczos3 by default), returning an opaque RGB image of
// exactly width by height pixels.
// Both dimensions must be positive and no larger than the source dimensions.
// Non-positive or out-of-JPEG-range target dimensions, invalid filters or
// worker counts, and negative headroom or memory budgets are rejected without
// reading r.
// Upsampling is rejected after reading the source dimensions.
//
// Unlike [Decode] at reduced factors, this applies full 8x8 IDCT, sample
// clipping, and RGB conversion before resizing. Filtering uses encoded RGB
// values, not linear light; ICC profiles and EXIF orientation are not applied.
//
// Single-scan grayscale and interleaved color images use bounded MCU-row
// buffers rather than a full-resolution source image. A bounded worker pool
// overlaps entropy decoding with IDCT, RGB conversion, and horizontal
// filtering; an ordered collector performs vertical filtering. Scratch memory
// depends on source width, destination dimensions, and worker count, not on
// source height. The destination image itself is always allocated in full.
//
// See [ResizeOptions.AllowFullDecode] for progressive and non-interleaved
// inputs. Additional scans after an already streamed scan, or color metadata
// changing after that scan, are unsupported even when fallback is allowed:
// streaming cannot replay previously discarded source data.
func DecodeResize(r io.Reader, width, height int, opts *ResizeOptions) (*image.RGBA, error) {
	im, _, err := decodeResize(r, width, height, opts)
	return im, err
}

// DecodeThumbnail decodes a JPEG at a reduced IDCT resolution, then resizes it
// with the selected scale-aware filter (Lanczos3 by default) to exactly width
// by height opaque RGB pixels. It chooses the largest factor in 8, 4, 2, 1
// that retains at least ThumbnailHeadroom times each target dimension
// (four by default), or the original dimension when that is smaller.
// The factor is chosen from the original dimensions before allocating buffers.
// Unlike [DecodeResize], this may discard high-frequency coefficients before
// sample clipping and RGB conversion, so the two APIs have different results.
// The implementation is pure Go, without native libraries or external programs.
//
// Dimensions, options, color handling, streaming support, and source budgets
// follow [DecodeResize]. Both dimensions must be positive and no larger than
// the original; the decoder never upsamples the original image. Filtering
// preserves the reduced samples' original-image phase, including at odd sizes.
// Single-scan inputs use bounded reduced MCU-row buffers. IDCT, RGB conversion,
// and horizontal filtering share the worker pool; entropy decoding and ordered
// vertical filtering remain serial.
//
// With AllowFullDecode, progressive inputs retain image-wide compact
// coefficients, but use row workers for final reconstruction without image-wide
// pixel planes. Sequential non-interleaved fallback uses reduced pixel planes
// and serial filtering.
func DecodeThumbnail(r io.Reader, width, height int, opts *ResizeOptions) (*image.RGBA, error) {
	im, _, err := decodeThumbnail(r, width, height, opts)
	return im, err
}

// Returning the decoder lets package tests inspect the actual owned buffers
// and verify worker shutdown, rather than inferring memory use from output size.
func decodeResize(r io.Reader, width, height int, opts *ResizeOptions) (*image.RGBA, *decoder, error) {
	return decodeResizeMode(r, width, height, opts, false)
}

func decodeThumbnail(r io.Reader, width, height int, opts *ResizeOptions) (*image.RGBA, *decoder, error) {
	return decodeResizeMode(r, width, height, opts, true)
}

func decodeResizeMode(r io.Reader, width, height int, opts *ResizeOptions, thumbnail bool) (*image.RGBA, *decoder, error) {
	var o ResizeOptions
	if opts != nil {
		o = *opts
	}
	if width <= 0 || height <= 0 || width > 65535 || height > 65535 {
		return nil, nil, UnsupportedError("resize dimensions must be in [1, 65535]")
	}
	if !o.Filter.valid() {
		return nil, nil, UnsupportedError("invalid resize filter")
	}
	if o.ThumbnailHeadroom < 0 {
		return nil, nil, UnsupportedError("resize thumbnail headroom must be non-negative")
	}
	if o.ThumbnailHeadroom == 0 {
		o.ThumbnailHeadroom = 4
	}
	if o.Workers < 0 || o.Workers > 64 {
		return nil, nil, UnsupportedError("resize workers must be in [0, 64]")
	}
	if o.MaxMemoryBytes < 0 {
		return nil, nil, UnsupportedError("resize memory budget must be non-negative")
	}
	if o.Workers == 0 {
		o.Workers = min(runtime.GOMAXPROCS(0), 8)
	}
	req := &resizeRequest{width: width, height: height, opts: o, thumbnail: thumbnail}
	d := &decoder{factor: 1, resize: req}
	defer req.releaseMemory(d)
	im, err := d.decode(r, false)
	if err != nil {
		return nil, d, err
	}
	if req.streaming {
		return req.filter.dst, d, nil
	}
	// Only explicitly permitted inputs can get here with image-wide pixels.
	// Thumbnail fallback pixels already use the chosen reduced factor.
	f, err := req.newFilter(d)
	if err != nil {
		return nil, d, err
	}
	req.filter = f
	row := image.NewRGBA(image.Rect(0, 0, f.sw, 1))
	values := make([]float64, 3*width)
	for y := 0; y < f.sh; y++ {
		draw.Draw(row, row.Rect, im, image.Pt(0, y), draw.Src)
		f.horizontal(values, row.Pix)
		if err := f.addRows(values, y, 1); err != nil {
			return nil, d, err
		}
	}
	if err := f.finish(); err != nil {
		return nil, d, err
	}
	return f.dst, d, nil
}

// chooseThumbnailFactor is the default four-times-headroom policy.
func chooseThumbnailFactor(sw, sh, dw, dh int) int {
	return chooseThumbnailFactorHeadroom(sw, sh, dw, dh, 4)
}

// Compare before multiplying so even a maxInt headroom cannot overflow.
// Invalid dimensions/headroom conservatively keep full resolution; public
// callers validate these values before reading the input.
func chooseThumbnailFactorHeadroom(sw, sh, dw, dh, headroom int) int {
	if sw <= 0 || sh <= 0 || dw <= 0 || dh <= 0 || headroom < 0 {
		return 1
	}
	if headroom == 0 {
		headroom = 4
	}
	minW, minH := sw, sh
	if dw <= sw/headroom {
		minW = headroom * dw
	}
	if dh <= sh/headroom {
		minH = headroom * dh
	}
	for _, factor := range [...]int{8, 4, 2, 1} {
		if (sw-1)/factor+1 >= minW && (sh-1)/factor+1 >= minH {
			return factor
		}
	}
	return 1
}

type resizeRequest struct {
	width, height  int
	opts           ResizeOptions
	thumbnail      bool
	streaming      bool
	fallback       bool
	deferred       bool // Progressive thumbnail: coefficients only until EOI.
	complete       bool
	scans          int
	filter         *lanczosResizer
	pipeline       *resizePipeline
	rgb            bool
	adobeValid     bool
	adobe          uint8
	memoryAdmitted bool
	memoryRelease  func()
}

func (r *resizeRequest) source(d *decoder) error {
	if r.width > d.width || r.height > d.height {
		return UnsupportedError("resize target exceeds source dimensions")
	}
	if r.opts.MaxSourcePixels != 0 && uint64(d.width)*uint64(d.height) > r.opts.MaxSourcePixels {
		return UnsupportedError("resize source exceeds pixel budget")
	}
	if r.thumbnail {
		d.factor = chooseThumbnailFactorHeadroom(d.width, d.height, r.width, r.height, r.opts.ThumbnailHeadroom)
	}
	if d.progressive && !r.opts.AllowFullDecode {
		return UnsupportedError("progressive resize requires AllowFullDecode")
	}
	return nil
}

func (r *resizeRequest) startScan(d *decoder, nComp, mxx, myy int) error {
	r.scans++
	if r.streaming {
		return UnsupportedError("additional scan after streaming resize")
	}
	if r.fallback {
		return nil
	}
	if d.progressive || nComp != d.nComp || d.nComp == 4 && !d.adobeTransformValid {
		if !r.opts.AllowFullDecode {
			return UnsupportedError("streaming resize requires one complete interleaved scan and known color model")
		}
		r.fallback = true
		r.deferred = r.thumbnail && d.progressive
		return r.reserveMemory(d, mxx, myy)
	}
	if err := checkResizeColor(d); err != nil {
		return err
	}
	if err := r.reserveMemory(d, mxx, myy); err != nil {
		return err
	}
	return r.startPipeline(d, mxx, myy)
}

// reserveMemory acquires the entire plan before any large allocation. In
// particular, progressive scans never acquire more capacity while holding a
// reservation for previously decoded components.
func (r *resizeRequest) reserveMemory(d *decoder, mxx, myy int) error {
	if r.memoryAdmitted {
		return nil
	}
	if r.opts.MaxMemoryBytes == 0 && r.opts.MemAllocSemaphore == nil {
		return nil
	}
	n, err := r.memoryEstimate(d, mxx, myy)
	if err != nil {
		return err
	}
	if r.opts.MaxMemoryBytes > 0 && n > r.opts.MaxMemoryBytes {
		return UnsupportedError(fmt.Sprintf("resize requires %d bytes, exceeds memory budget %d", n, r.opts.MaxMemoryBytes))
	}
	if r.opts.MemAllocSemaphore != nil {
		release, err := r.opts.MemAllocSemaphore(n)
		if err != nil {
			return err
		}
		r.memoryRelease = release
	}
	r.memoryAdmitted = true
	return nil
}

func (r *resizeRequest) releaseMemory(d *decoder) {
	if !r.memoryAdmitted || r.opts.MemAllocSemaphore == nil {
		return
	}
	// All pipeline exits join workers before reaching this defer. Drop the
	// decoder's scratch references before giving the reservation back. Keep
	// private buffer-inspection helpers unchanged when no hook is configured.
	release := r.memoryRelease
	r.memoryRelease = nil
	r.opts.MemAllocSemaphore = nil
	r.pipeline, r.filter = nil, nil
	d.img1, d.img3, d.blackPix = nil, nil, nil
	d.progCoeffs = [maxComponents][]int32{}
	d.progNonzero = [maxComponents][]uint64{}
	d.r = nil
	if release != nil {
		release()
	}
}

func (r *resizeRequest) newFilter(d *decoder) (*lanczosResizer, error) {
	if r.thumbnail {
		return newScaledFilteredResizer(d.width, d.height, d.factor, r.width, r.height, r.opts.Filter)
	}
	return newFilteredResizer(d.width, d.height, r.width, r.height, r.opts.Filter)
}

func checkResizeColor(d *decoder) error {
	if d.nComp == 4 && !d.adobeTransformValid {
		return UnsupportedError("unknown color model: 4-component JPEG doesn't have Adobe APP14 metadata")
	}
	if d.nComp == 3 && d.isRGB() {
		h0, h1, h2 := d.comp[0].h, d.comp[1].h, d.comp[2].h
		v0, v1, v2 := d.comp[0].v, d.comp[1].v, d.comp[2].v
		if h1 != h2 || h0%h1 != 0 || v1 != v2 || v0%v1 != 0 {
			return errUnsupportedSubsamplingRatio
		}
	}
	return nil
}

func (r *resizeRequest) startPipeline(d *decoder, mxx, myy int) error {
	if err := checkResizeColor(d); err != nil {
		return err
	}
	f, err := r.newFilter(d)
	if err != nil {
		return err
	}
	r.filter, r.rgb = f, d.isRGB()
	r.adobeValid, r.adobe = d.adobeTransformValid, d.adobeTransform
	p, err := newResizePipeline(d, r, mxx, myy)
	if err != nil {
		return err
	}
	r.pipeline, r.streaming = p, true
	return nil
}

// reconstructProgressive waits until all scans and metadata have been read,
// then feeds compact retained coefficients through the same bounded worker
// pipeline as sequential input. Only the coefficients are image-wide.
func (r *resizeRequest) reconstructProgressive(d *decoder) (retErr error) {
	mxx := (d.width + 8*d.maxH - 1) / (8 * d.maxH)
	myy := (d.height + 8*d.maxV - 1) / (8 * d.maxV)
	if err := r.startPipeline(d, mxx, myy); err != nil {
		return err
	}
	p := r.pipeline
	defer func() {
		if err := p.closeAndWait(); retErr == nil {
			retErr = err
		}
		if retErr == nil {
			retErr = r.filter.finish()
		}
		r.complete = retErr == nil
	}()
	bandHeight := (8 / d.factor) * d.maxV
	height := (d.height + d.factor - 1) / d.factor
	for my := 0; my < myy; my++ {
		b, err := p.acquire()
		if err != nil {
			return err
		}
		b.y = my * bandHeight
		b.n = min(bandHeight, height-b.y)
		for ci := 0; ci < d.nComp; ci++ {
			// The full decoder leaves never-transmitted components zero,
			// not IDCT(zero coefficients) == 128. These workers are newly
			// allocated after all scans; this skip mask is identical in
			// every band, so their untouched planes remain zero on reuse.
			b.skip[ci] = d.progCoeffs[ci] == nil
			if b.skip[ci] {
				continue
			}
			stride := mxx * d.comp[ci].h
			for by := 0; by < d.comp[ci].v; by++ {
				for bx := 0; bx < stride; bx++ {
					index := (my*d.comp[ci].v+by)*stride + bx
					d.loadProgressiveBlock(&b.coeffs[ci][by*stride+bx], ci, index)
				}
			}
		}
		if err := p.submit(b); err != nil {
			return err
		}
	}
	return nil
}

func (r *resizeRequest) result(d *decoder) (image.Image, error) {
	if !r.complete {
		return nil, FormatError("incomplete streamed scan")
	}
	if r.rgb != d.isRGB() || r.adobeValid != d.adobeTransformValid || r.adobe != d.adobeTransform {
		return nil, UnsupportedError("color metadata changed after streamed scan")
	}
	return r.filter.dst, nil
}

type resizeBand struct {
	y, n   int
	coeffs [maxComponents][]block
	skip   [maxComponents]bool // Untransmitted progressive components.
	rows   []float64           // Packed, horizontally filtered RGB rows.
	err    error
}

type resizeWorker struct {
	d    decoder // Only one MCU row's image planes; no entropy state in use.
	rgba *image.RGBA
}

type resizePipeline struct {
	filter   *lanczosResizer
	bands    []*resizeBand
	workers  []*resizeWorker
	free     chan *resizeBand
	jobs     chan *resizeBand
	results  chan *resizeBand
	errCh    chan error
	done     chan struct{}
	wg       sync.WaitGroup
	joined   bool
	firstErr error // Collector-owned until done closes.
}

func newResizePipeline(d *decoder, req *resizeRequest, mxx, myy int) (*resizePipeline, error) {
	nw := min(req.opts.Workers, myy)
	nb := min(nw+1, myy)
	originalBandHeight := 8 * d.maxV
	bandHeight := originalBandHeight / d.factor
	reducedWidth := (d.width + d.factor - 1) / d.factor
	p := &resizePipeline{
		filter: req.filter, free: make(chan *resizeBand, nb),
		jobs: make(chan *resizeBand, nw), results: make(chan *resizeBand, nw),
		errCh: make(chan error, 1), done: make(chan struct{}),
	}
	rowValues := uint64(bandHeight) * uint64(req.width) * 3
	if err := checkAllocationSize(rowValues * 8 * uint64(nb)); err != nil {
		return nil, err
	}
	for ci := 0; ci < d.nComp; ci++ {
		count := uint64(mxx) * uint64(d.comp[ci].h*d.comp[ci].v)
		if err := checkAllocationSize(count * 256 * uint64(nb)); err != nil {
			return nil, err
		}
	}
	for range nb {
		b := &resizeBand{rows: make([]float64, int(rowValues))}
		for ci := 0; ci < d.nComp; ci++ {
			b.coeffs[ci] = make([]block, mxx*d.comp[ci].h*d.comp[ci].v)
		}
		p.bands = append(p.bands, b)
		p.free <- b
	}
	for range nw {
		w := &resizeWorker{d: decoder{
			width: d.width, height: originalBandHeight, factor: d.factor, nComp: d.nComp,
			maxH: d.maxH, maxV: d.maxV, comp: d.comp, quant: d.quant,
			jfif: d.jfif, adobeTransformValid: d.adobeTransformValid, adobeTransform: d.adobeTransform,
		}}
		if err := w.d.makeImg(mxx, 1); err != nil {
			return nil, err
		}
		if err := checkAllocationSize(uint64(reducedWidth) * uint64(bandHeight) * 4); err != nil {
			return nil, err
		}
		w.rgba = image.NewRGBA(image.Rect(0, 0, reducedWidth, bandHeight))
		p.workers = append(p.workers, w)
	}
	// No goroutines start until every allocation/validation above succeeds.
	go p.collect()
	for _, w := range p.workers {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for b := range p.jobs {
				b.err = w.process(b, p.filter, mxx)
				p.results <- b
			}
		}()
	}
	return p, nil
}

func (p *resizePipeline) acquire() (*resizeBand, error) {
	select {
	case err := <-p.errCh:
		return nil, err
	case b := <-p.free:
		b.err = nil
		return b, nil
	}
}

func (p *resizePipeline) submit(b *resizeBand) error {
	select {
	case err := <-p.errCh:
		return err
	case p.jobs <- b:
		return nil
	}
}

func (p *resizePipeline) collect() {
	defer close(p.done)
	pending := make(map[int]*resizeBand, len(p.bands))
	next := 0
	recordError := func(err error) {
		if p.firstErr == nil && err != nil {
			p.firstErr = err
			p.errCh <- err
		}
	}
	for b := range p.results {
		recordError(b.err)
		pending[b.y] = b
		// A slot is recycled only after ordered consumption. Thus a slow
		// early band cannot permit an unbounded queue of later results.
		for {
			b := pending[next]
			if b == nil {
				break
			}
			delete(pending, next)
			if p.firstErr == nil {
				recordError(p.filter.addRows(b.rows, b.y, b.n))
			}
			next += b.n
			p.free <- b
		}
	}
}

func (p *resizePipeline) closeAndWait() error {
	close(p.jobs)
	p.wg.Wait()
	close(p.results)
	<-p.done
	p.joined = true
	return p.firstErr
}

func (w *resizeWorker) process(b *resizeBand, f *lanczosResizer, mxx int) error {
	d := &w.d
	for ci := 0; ci < d.nComp; ci++ {
		if b.skip[ci] {
			continue
		}
		stride := mxx * d.comp[ci].h
		for by := 0; by < d.comp[ci].v; by++ {
			for bx := 0; bx < stride; bx++ {
				if err := d.reconstructBlock(&b.coeffs[ci][by*stride+bx], bx, by, ci); err != nil {
					return err
				}
			}
		}
	}
	width := w.rgba.Rect.Dx()
	rect := image.Rect(0, 0, width, b.n)
	if d.nComp == 1 {
		draw.Draw(w.rgba, rect, d.img1, image.Point{}, draw.Src)
	} else if d.nComp == 3 && !d.isRGB() {
		draw.Draw(w.rgba, rect, d.img3, image.Point{}, draw.Src)
	} else if d.nComp == 3 {
		scale := d.comp[0].h / d.comp[1].h
		for y := 0; y < b.n; y++ {
			yo, co := d.img3.YOffset(0, y), d.img3.COffset(0, y)
			dst := w.rgba.Pix[y*w.rgba.Stride:]
			for x := 0; x < width; x++ {
				dst[4*x+0], dst[4*x+1], dst[4*x+2], dst[4*x+3] =
					d.img3.Y[yo+x], d.img3.Cb[co+x/scale], d.img3.Cr[co+x/scale], 255
			}
		}
	} else {
		if d.adobeTransform != adobeTransformUnknown {
			draw.Draw(w.rgba, rect, d.img3, image.Point{}, draw.Src)
		}
		for y := 0; y < b.n; y++ {
			yo := d.img3.YOffset(0, y)
			dst := w.rgba.Pix[y*w.rgba.Stride:]
			for x := 0; x < width; x++ {
				k := 255 - d.blackPix[y*d.blackStride+x]
				var c, m, yellow byte
				if d.adobeTransform == adobeTransformUnknown {
					// The original CMYK decode inverts Adobe's samples.
					c = 255 - d.img3.Y[yo+x]
					m = 255 - d.img3.Cb[d.img3.COffset(x, y)]
					yellow = 255 - d.img3.Cr[d.img3.COffset(x, y)]
				} else {
					c, m, yellow = dst[4*x], dst[4*x+1], dst[4*x+2]
				}
				dst[4*x], dst[4*x+1], dst[4*x+2] = color.CMYKToRGB(c, m, yellow, k)
				dst[4*x+3] = 255
			}
		}
	}
	stride := 3 * f.dst.Rect.Dx()
	for y := 0; y < b.n; y++ {
		f.horizontal(b.rows[y*stride:(y+1)*stride], w.rgba.Pix[y*w.rgba.Stride:])
	}
	return nil
}
