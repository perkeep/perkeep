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
	"image/color"
	"math"
	"testing"

	"perkeep.org/internal/media/quicktime"
)

// sienna is the sRGB color of testdata/p3.mov, #996633.
var sienna = color.RGBA{0x99, 0x66, 0x33, 0xff}

func TestDecodeP3(t *testing.T) {
	forEachHEICDecoder(t, func(t *testing.T) {
		v, err := openTestdata(t, "p3.mov", "video/quicktime")
		if err != nil {
			t.Fatal(err)
		}
		if v.color == nil {
			t.Fatal("no color conversion for a Display P3 video")
		}
		im, err := v.Decode()
		if err != nil {
			t.Fatal(err)
		}
		// Without the conversion, it's about #91683c.
		if d := maxDiff(im.At(32, 24), sienna); d > 2 {
			t.Errorf("decoded %v; want about %v", im.At(32, 24), sienna)
		}
	})
}

func TestNoColorConversion(t *testing.T) {
	for _, ci := range []quicktime.ColorInfo{
		{},
		{Known: true, Primaries: quicktime.PrimariesBT709, Transfer: 1, Matrix: 1},
		{Known: true, Primaries: 2, Transfer: 2, Matrix: 2}, // unspecified
	} {
		if c := newColorConversion(ci); c != nil {
			t.Errorf("newColorConversion(%+v) = %+v; want nil", ci, c)
		}
	}
}

// TestHDR converts frames encoded here by ITU-R BT.2100, with HDR
// reference white at 203 nits (ITU-R BT.2408).
func TestHDR(t *testing.T) {
	hlg := newColorConversion(quicktime.ColorInfo{Known: true, Primaries: quicktime.PrimariesBT2020, Transfer: quicktime.TransferHLG, Matrix: 9})
	pq := newColorConversion(quicktime.ColorInfo{Known: true, Primaries: quicktime.PrimariesBT2020, Transfer: quicktime.TransferPQ, Matrix: 9})
	for _, tt := range []struct {
		name string
		c    *colorConversion
		in   [3]float64 // R'G'B', from 0 to 1
		want color.RGBA
		tol  int
	}{
		{"hlg sienna", hlg, hlgEncode(sienna), toneMapped(sienna), 2},
		{"pq sienna", pq, pqEncode(sienna), toneMapped(sienna), 2},
		{"hlg black", hlg, [3]float64{0, 0, 0}, color.RGBA{0, 0, 0, 0xff}, 0},
		// HLG's reference white, at 75%, and peak.
		{"hlg reference white", hlg, [3]float64{0.75, 0.75, 0.75}, color.RGBA{168, 168, 168, 0xff}, 2},
		{"hlg peak", hlg, [3]float64{1, 1, 1}, color.RGBA{245, 245, 245, 0xff}, 1},
		{"pq peak", pq, [3]float64{1, 1, 1}, color.RGBA{0xff, 0xff, 0xff, 0xff}, 1},
	} {
		im := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		for i, v := range tt.in {
			im.Pix[i] = uint8(math.Round(v * 255))
		}
		im.Pix[3] = 0xff
		got := tt.c.convert(im).At(0, 0)
		if d := maxDiff(got, tt.want); d > tt.tol {
			t.Errorf("%s: got %v; want %v", tt.name, got, tt.want)
		}
	}
}

func maxDiff(a, b color.Color) int {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	d := 0
	for _, p := range [][2]uint32{{ar, br}, {ag, bg}, {ab, bb}} {
		d = max(d, int(p[0]>>8)-int(p[1]>>8), int(p[1]>>8)-int(p[0]>>8))
	}
	return d
}

// toneMapped returns sRGB color c, as HDR light relative to its reference
// white, tone mapped to sRGB.
func toneMapped(c color.RGBA) color.RGBA {
	lin := [3]float64{decodeSRGB(float64(c.R) / 255), decodeSRGB(float64(c.G) / 255), decodeSRGB(float64(c.B) / 255)}
	m := max(lin[0], lin[1], lin[2])
	var out [3]uint8
	for i, v := range lin {
		out[i] = to8(encodeSRGB(v * toneMap(m) / m))
	}
	return color.RGBA{out[0], out[1], out[2], 0xff}
}

// bt2020Linear returns sRGB color c as linear BT.2020 light, relative to
// HDR reference white.
func bt2020Linear(c color.RGBA) [3]float64 {
	r, g, b := decodeSRGB(float64(c.R)/255), decodeSRGB(float64(c.G)/255), decodeSRGB(float64(c.B)/255)
	return [3]float64{
		0.6274*r + 0.3293*g + 0.0433*b,
		0.0691*r + 0.9195*g + 0.0114*b,
		0.0164*r + 0.0880*g + 0.8956*b,
	}
}

// hlgEncode returns sRGB color c as HLG R'G'B' for a 1000 nit display:
// the inverse of the OOTF, by luminance, and then the OETF.
func hlgEncode(c color.RGBA) (e [3]float64) {
	fd := bt2020Linear(c)
	for i := range fd {
		fd[i] *= hdrReferenceWhite / hlgPeak
	}
	yd := 0.2627*fd[0] + 0.6780*fd[1] + 0.0593*fd[2]
	for i, v := range fd {
		es := v * math.Pow(yd, (1-1.2)/1.2) // scene light
		const a, b, c = 0.17883277, 0.28466892, 0.55991073
		if es <= 1.0/12 {
			e[i] = math.Sqrt(3 * es)
		} else {
			e[i] = a*math.Log(12*es-b) + c
		}
	}
	return e
}

// pqEncode returns sRGB color c as PQ R'G'B'.
func pqEncode(c color.RGBA) (e [3]float64) {
	const m1, m2 = 2610.0 / 16384, 2523.0 / 4096 * 128
	const c1, c2, c3 = 3424.0 / 4096, 2413.0 / 4096 * 32, 2392.0 / 4096 * 32
	for i, v := range bt2020Linear(c) {
		y := math.Pow(v*hdrReferenceWhite/pqPeak, m1)
		e[i] = math.Pow((c1+c2*y)/(1+c3*y), m2)
	}
	return e
}
