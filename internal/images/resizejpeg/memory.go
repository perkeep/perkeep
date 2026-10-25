/*
Copyright 2026 The Perkeep Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resizejpeg

import "unsafe"

// These allowances cover the decoder, filter and image headers, channels,
// bounded reorder map, goroutine closures, and ordinary stack/scratch usage.
// A worker includes an embedded decoder (with its Huffman tables and input
// buffer), plus stack space. Bands have a separate small object allowance.
// These are planning allowances, not promises about the Go runtime's RSS:
// allocator metadata, retained garbage, runtime stacks, and the caller's
// reader and images can consume memory outside a decode's reservation.
const (
	resizeMemoryBase   = 128 << 10
	resizeMemoryWorker = 96 << 10
	resizeMemoryBand   = 4 << 10
)

const maxMemoryEstimate = uint64(1<<63 - 1)

// memoryPlan sums separately allocated backing arrays. Individual allocations
// must fit int, including on 32-bit systems; their sum need only fit int64.
type memoryPlan struct {
	bytes uint64
}

func memorySize(factors ...uint64) (uint64, error) {
	n := uint64(1)
	limit := uint64(^uint(0) >> 1)
	for _, f := range factors {
		if f != 0 && n > limit/f {
			return 0, UnsupportedError("image is too large")
		}
		n *= f
	}
	return n, nil
}

func (p *memoryPlan) add(n uint64) error {
	if n > maxMemoryEstimate-p.bytes {
		return UnsupportedError("image is too large")
	}
	p.bytes += n
	return nil
}

// allocation includes conservative allocator rounding for each of copies
// identical backing arrays. Small allocations get 25% plus 64 bytes, rounded
// to 64 bytes and capped at Go's largest small size class (32 KiB).
// This covers small size classes and headers. Larger arrays get a possible
// header and round up to an 8 KiB page. This intentionally does not attempt to
// predict exact runtime allocation statistics.
func (p *memoryPlan) allocation(copies int, factors ...uint64) error {
	n, err := memorySize(factors...)
	if err != nil {
		return err
	}
	if copies < 0 {
		return UnsupportedError("invalid resize memory geometry")
	}
	if n == 0 || copies == 0 {
		return nil
	}
	if n <= 32<<10 {
		n = min((n+n/4+64+63)&^63, 32<<10)
	} else {
		// memorySize already bounded n by maxInt (at most maxInt64).
		// uint64 has room for the extra header and rounding.
		n = (n + 8 + 8191) &^ 8191
	}
	if n > maxMemoryEstimate/uint64(copies) {
		return UnsupportedError("image is too large")
	}
	return p.add(n * uint64(copies))
}

// memoryEstimate is called once, at the first SOS, after selecting the resize
// path and before its bulk allocations. It reserves the eventual full working
// set, not just the first scan: later progressive scans can introduce every
// component, and late APP14 metadata can require a full RGB conversion.
//
// Geometry is O(nComp), with no spans, pixels, or source rows visited and no
// image-sized allocations. Holding this one reservation through completion
// avoids deadlock from decoders trying to grow partial reservations.
func (r *resizeRequest) memoryEstimate(d *decoder, mxx, myy int) (int64, error) {
	if d.width <= 0 || d.height <= 0 || d.maxH < 1 || d.maxH > 4 || d.maxV < 1 || d.maxV > 4 ||
		(d.nComp != 1 && d.nComp != 3 && d.nComp != 4) ||
		mxx != (d.width-1)/(8*d.maxH)+1 || myy != (d.height-1)/(8*d.maxV)+1 {
		return 0, UnsupportedError("invalid resize memory geometry")
	}
	switch d.factor {
	case 1, 2, 4, 8:
	default:
		return 0, UnsupportedError("invalid resize IDCT factor")
	}
	for ci := 0; ci < d.nComp; ci++ {
		c := d.comp[ci]
		if c.h < 1 || c.v < 1 || c.h > d.maxH || c.v > d.maxV ||
			d.maxH%c.h != 0 || d.maxV%c.v != 0 {
			return 0, UnsupportedError("invalid resize memory geometry")
		}
	}
	sw, sh := (d.width-1)/d.factor+1, (d.height-1)/d.factor+1
	a, err := filterAllocationGeometry(sw, sh, r.width, r.height,
		float64(d.height)/float64(d.factor), r.opts.Filter)
	if err != nil {
		return 0, err
	}
	p := memoryPlan{bytes: resizeMemoryBase}
	// Destination pixels, x/y spans, the constructor's conservative xWeights
	// capacity (not merely its final length), and vertical accumulation ring.
	for _, factors := range [][]uint64{
		{uint64(r.width), uint64(r.height), 4},
		{uint64(r.width), uint64(unsafe.Sizeof(lanczosSpan{}))},
		{uint64(r.height), uint64(unsafe.Sizeof(lanczosSpan{}))},
		{uint64(a.weightCount), 8},
		{uint64(a.sumBytes)},
	} {
		if err := p.allocation(1, factors...); err != nil {
			return 0, err
		}
	}
	bs := uint64(8 / d.factor)
	if d.progressive {
		// All components, regardless of which ones the first scan transmits.
		for ci := 0; ci < d.nComp; ci++ {
			blocks, err := memorySize(uint64(mxx), uint64(myy), uint64(d.comp[ci].h), uint64(d.comp[ci].v))
			if err != nil {
				return 0, err
			}
			if err := p.allocation(1, blocks, bs, bs, 4); err != nil {
				return 0, err
			}
			if d.factor != 1 {
				if err := p.allocation(1, blocks, 8); err != nil {
					return 0, err
				}
			}
		}
	}
	if !r.fallback || r.deferred {
		if r.opts.Workers < 1 || r.opts.Workers > 64 {
			return 0, UnsupportedError("invalid resize memory workers")
		}
		nw := min(r.opts.Workers, myy)
		nb := min(nw+1, myy)
		if err := p.add(uint64(nw)*resizeMemoryWorker + uint64(nb)*resizeMemoryBand); err != nil {
			return 0, err
		}
		bandHeight := bs * uint64(d.maxV)
		// Each band retains full 8x8 coefficient blocks, even for thumbnails.
		// Match the pipeline's aggregate band overflow checks as well as the
		// per-slice checks, so it cannot reject this geometry after admission.
		for ci := 0; ci < d.nComp; ci++ {
			n, err := memorySize(uint64(mxx), uint64(d.comp[ci].h), uint64(d.comp[ci].v), 256)
			if err != nil {
				return 0, err
			}
			if _, err := memorySize(n, uint64(nb)); err != nil {
				return 0, err
			}
			if err := p.allocation(nb, n); err != nil {
				return 0, err
			}
		}
		rowBytes, err := memorySize(bandHeight, uint64(r.width), 3, 8)
		if err != nil {
			return 0, err
		}
		if _, err := memorySize(rowBytes, uint64(nb)); err != nil {
			return 0, err
		}
		if err := p.allocation(nb, rowBytes); err != nil {
			return 0, err
		}
		if err := p.sourcePlanes(d, mxx, 1, nw); err != nil {
			return 0, err
		}
		if err := p.allocation(nw, uint64(sw), bandHeight, 4); err != nil {
			return 0, err
		}
	} else {
		if err := p.sourcePlanes(d, mxx, myy, 1); err != nil {
			return 0, err
		}
		// Reserve for every three-component image, not just isRGB at SOS:
		// APP14 between scans can change the final interpretation to RGB.
		// Four-component fallback always creates an interleaved CMYK image.
		if d.nComp >= 3 {
			if err := p.allocation(1, uint64(sw), uint64(sh), 4); err != nil {
				return 0, err
			}
		}
		if err := p.allocation(1, uint64(sw), 4); err != nil {
			return 0, err
		}
		if err := p.allocation(1, uint64(r.width), 3, 8); err != nil {
			return 0, err
		}
	}
	return int64(p.bytes), nil
}

// sourcePlanes mirrors makeImg, including MCU padding, the single combined
// YCbCr backing array, flex expansion to 4:4:4, and a separate black plane.
// Normal 4:2:0 remains 1.5 bytes per padded pixel, not three.
func (p *memoryPlan) sourcePlanes(d *decoder, mxx, myy, copies int) error {
	bs := uint64(8 / d.factor)
	if d.nComp == 1 {
		return p.allocation(copies, bs, uint64(mxx), bs, uint64(myy))
	}
	pw, err := memorySize(bs, uint64(d.maxH), uint64(mxx))
	if err != nil {
		return err
	}
	ph, err := memorySize(bs, uint64(d.maxV), uint64(myy))
	if err != nil {
		return err
	}
	ch, cv := 1, 1
	if d.comp[1].h == d.comp[2].h && d.comp[1].v == d.comp[2].v &&
		d.maxH == d.comp[0].h && d.maxV == d.comp[0].v {
		h, v := d.maxH/d.comp[1].h, d.maxV/d.comp[1].v
		switch h<<4 | v {
		case 0x11, 0x12, 0x21, 0x22, 0x41, 0x42:
			ch, cv = h, v
		}
	}
	y, err := memorySize(pw, ph)
	if err != nil {
		return err
	}
	c, err := memorySize(2, pw/uint64(ch), ph/uint64(cv))
	if err != nil {
		return err
	}
	// y and c each fit maxInt, so their sum cannot overflow uint64.
	if err := p.allocation(copies, y+c); err != nil {
		return err
	}
	if d.nComp == 4 {
		return p.allocation(copies, bs, uint64(d.comp[3].h), uint64(mxx),
			bs, uint64(d.comp[3].v), uint64(myy))
	}
	return nil
}
