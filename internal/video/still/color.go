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
	"image"
	"math"
	"runtime"
	"sync"

	"perkeep.org/internal/media/quicktime"
)

const (
	hdrReferenceWhite = 203.0  // nits, by ITU-R BT.2408
	hlgPeak           = 1000.0 // nits, the nominal display for HLG's system gamma
	pqPeak            = 10000.0

	// sdrWhite is the linear light, relative to HDR reference white,
	// that tone mapping (toneMap) would map to SDR white if it didn't
	// roll off. HDR reference white comes out darker than SDR white
	// (39% of its linear light, or sRGB 168), leaving room for
	// highlights, as video players show HDR video on SDR displays.
	// With toneMap's curve, it's fitted to libplacebo's rendering of
	// iPhone HLG video frames for SDR displays, without its per-frame
	// peak detection.
	sdrWhite = 2.0
)

// colorConversion converts decoded frames of a video's color space to
// sRGB. HEIF decoders give frames as R'G'B', or as full range BT.601
// Y'CbCr (as in JPEG, and as Go's image.YCbCr assumes), converting their
// samples' range and matrix but not their primaries or transfer function.
type colorConversion struct {
	// linear converts R'G'B' (as 0 to lutSize-1) to linear light,
	// relative to SDR white, or for HDR, to HDR reference white.
	linear    *[lutSize]float64
	hlg       bool        // whether to apply HLG's system gamma
	hdr       bool        // whether to tone map
	primaries *[9]float64 // to BT.709, from the video's, or nil
}

const lutSize = 1024

// newColorConversion returns the conversion of frames of a video with
// color information ci, or nil if they need none: if they're SDR with
// BT.709 (sRGB) primaries, or of unknown color.
func newColorConversion(ci quicktime.ColorInfo) *colorConversion {
	if !ci.Known {
		return nil
	}
	c := new(colorConversion)
	switch ci.Transfer {
	case quicktime.TransferHLG:
		c.linear, c.hlg, c.hdr = &hlgLinear, true, true
	case quicktime.TransferPQ:
		c.linear, c.hdr = &pqLinear, true
	}
	switch ci.Primaries {
	case quicktime.PrimariesBT2020:
		c.primaries = &bt2020ToBT709
	case quicktime.PrimariesDisplayP3:
		c.primaries = &p3ToBT709
	}
	if c.linear == nil {
		if c.primaries == nil {
			return nil
		}
		// SDR with wide color primaries.
		c.linear = &sRGBLinear
	}
	return c
}

// convert returns the decoded frame im converted to sRGB, or im if it's
// of a type the decoders don't return.
func (c *colorConversion) convert(im image.Image) image.Image {
	// pixel returns the R'G'B' of a pixel, from 0 to 1.
	var pixel func(x, y int) (r, g, b float64)
	switch im := im.(type) {
	case *image.YCbCr:
		pixel = func(x, y int) (r, g, b float64) {
			yy := float64(im.Y[im.YOffset(x, y)]) / 255
			ci := im.COffset(x, y)
			cb, cr := (float64(im.Cb[ci])-128)/255, (float64(im.Cr[ci])-128)/255
			// BT.601 full range, as in JFIF.
			return yy + 1.402*cr, yy - 0.344136*cb - 0.714136*cr, yy + 1.772*cb
		}
	case *image.NRGBA:
		// Opaque, as video frames are.
		pixel = func(x, y int) (r, g, b float64) {
			p := im.Pix[im.PixOffset(x, y):]
			return float64(p[0]) / 255, float64(p[1]) / 255, float64(p[2]) / 255
		}
	default:
		return im
	}
	bounds := im.Bounds()
	out := image.NewRGBA(bounds)
	rows := bounds.Dy()
	workers := min(runtime.GOMAXPROCS(0), 8, max(1, rows/16))
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for y := bounds.Min.Y + w*rows/workers; y < bounds.Min.Y+(w+1)*rows/workers; y++ {
				pix := out.Pix[out.PixOffset(bounds.Min.X, y):]
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					r, g, b := c.toSRGBLinear(pixel(x, y))
					pix[0] = to8(encodeSRGB(r))
					pix[1] = to8(encodeSRGB(g))
					pix[2] = to8(encodeSRGB(b))
					pix[3] = 0xff
					pix = pix[4:]
				}
			}
		})
	}
	wg.Wait()
	return out
}

// toSRGBLinear converts R'G'B' of the video's transfer and primaries to
// linear BT.709 (sRGB) light, tone mapped to SDR's range of 0 to 1.
func (c *colorConversion) toSRGBLinear(r, g, b float64) (float64, float64, float64) {
	r, g, b = c.linear[lutIndex(r)], c.linear[lutIndex(g)], c.linear[lutIndex(b)]
	if c.hlg {
		// HLG's OOTF: system gamma 1.2 on the BT.2020 luminance of
		// the scene light, for a 1000 nit display, then relative to
		// HDR reference white.
		ys := 0.2627*r + 0.6780*g + 0.0593*b
		const n = len(hlgGain) - 1
		s := hlgGain[min(int(math.Sqrt(max(ys, 0))*float64(n)+0.5), n)]
		r, g, b = r*s, g*s, b*s
	}
	if p := c.primaries; p != nil {
		r, g, b = p[0]*r+p[1]*g+p[2]*b, p[3]*r+p[4]*g+p[5]*b, p[6]*r+p[7]*g+p[8]*b
		r, g, b = max(r, 0), max(g, 0), max(b, 0) // clip out of gamut colors
	}
	if c.hdr {
		// Tone map, scaling all three components alike to keep their
		// hues.
		m := max(r, g, b)
		s := toneGain[min(int(m/toneStep+0.5), len(toneGain)-1)]
		r, g, b = r*s, g*s, b*s
	}
	return r, g, b
}

// hlgGain is HLG's OOTF gain for scene luminance Ys, relative to SDR
// white, indexed by the square root of Ys, for precision in dark tones.
var hlgGain = func() (lut [4096]float64) {
	for i := range lut {
		ys := math.Pow(float64(i)/float64(len(lut)-1), 2)
		lut[i] = math.Pow(max(ys, 1e-6), 0.2) * hlgPeak / hdrReferenceWhite
	}
	return lut
}()

// toneGain is toneMap(v)/v for v from 0, by toneStep. Brighter light
// gets the last gain, which maps it to 1 or less.
var toneGain = func() (lut [4096]float64) {
	lut[0] = 1 / sdrWhite // toneMap's slope at 0
	for i := 1; i < len(lut); i++ {
		v := float64(i) * toneStep
		lut[i] = toneMap(v) / v
	}
	return lut
}()

// toneStep is toneGain's step, for light up to 64 times HDR reference
// white, beyond the brightest HLG or PQ light after gamut conversion.
const toneStep = 64.0 / 4095

// toneMap maps linear light v, relative to HDR reference white, to SDR's
// range of 0 to 1, approaching 1.
func toneMap(v float64) float64 {
	return 1 - math.Exp(-v/sdrWhite)
}

func lutIndex(v float64) int {
	return min(max(int(v*(lutSize-1)+0.5), 0), lutSize-1)
}

func to8(v float64) uint8 {
	return uint8(min(max(v*255+0.5, 0), 255))
}

// Lookup tables from R'G'B' (0 to 1, as 0 to lutSize-1) to linear light.
var (
	// HLG's inverse OETF, giving scene light (0 to 1). ITU-R BT.2100.
	hlgLinear = makeLUT(func(e float64) float64 {
		const a, b, c = 0.17883277, 0.28466892, 0.55991073
		if e <= 0.5 {
			return e * e / 3
		}
		return (math.Exp((e-c)/a) + b) / 12
	})
	// PQ's EOTF, giving display light relative to HDR reference white.
	// SMPTE ST 2084.
	pqLinear = makeLUT(func(e float64) float64 {
		const m1, m2 = 2610.0 / 16384, 2523.0 / 4096 * 128
		const c1, c2, c3 = 3424.0 / 4096, 2413.0 / 4096 * 32, 2392.0 / 4096 * 32
		p := math.Pow(e, 1/m2)
		return math.Pow(max(p-c1, 0)/(c2-c3*p), 1/m1) * pqPeak / hdrReferenceWhite
	})
	// sRGB's EOTF, for SDR video with wide color primaries, whose tones
	// are kept as they are, encoded again with encodeSRGB.
	sRGBLinear = makeLUT(decodeSRGB)
)

func makeLUT(f func(float64) float64) (lut [lutSize]float64) {
	for i := range lut {
		lut[i] = f(float64(i) / (lutSize - 1))
	}
	return lut
}

func decodeSRGB(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// encodeSRGB applies sRGB's inverse EOTF to linear light v of 0 to 1.
func encodeSRGB(v float64) float64 {
	v = min(max(v, 0), 1)
	return srgbEncodeLUT[int(v*float64(len(srgbEncodeLUT)-1)+0.5)]
}

// srgbEncodeLUT is encodeSRGB's table, of linear light, finely enough for
// 8-bit output of dark tones.
var srgbEncodeLUT = func() (lut [4096]float64) {
	for i := range lut {
		v := float64(i) / float64(len(lut)-1)
		if v <= 0.0031308 {
			lut[i] = v * 12.92
		} else {
			lut[i] = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
	}
	return lut
}()

// Matrices converting linear RGB of other primaries, with D65 white, to
// BT.709 (sRGB) primaries, by rows.
var (
	bt2020ToBT709 = [9]float64{
		1.6605, -0.5876, -0.0728,
		-0.1246, 1.1329, -0.0083,
		-0.0182, -0.1006, 1.1187,
	}
	p3ToBT709 = [9]float64{
		1.2249, -0.2247, 0,
		-0.0420, 1.0419, 0,
		-0.0197, -0.0786, 1.0979,
	}
)
