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
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"math"
	"reflect"
	"testing"
	"testing/iotest"

	xdraw "golang.org/x/image/draw"
)

var resizeOptionsAPIs = []struct {
	name      string
	thumbnail bool
	decode    func(io.Reader, int, int, *ResizeOptions) (*image.RGBA, *decoder, error)
	public    func(io.Reader, int, int, *ResizeOptions) (*image.RGBA, error)
}{
	{"resize", false, decodeResize, DecodeResize},
	{"thumbnail", true, decodeThumbnail, DecodeThumbnail},
}

var resizeOptionsFilters = []Filter{Lanczos3, CatmullRom, Bilinear}

// Form the headroom goal in uint64, guarding the product with division first.
// In particular maxInt*maxInt must not overflow even on a 64-bit test host.
// This reference does not call either production factor-selection helper.
func resizeOptionsReferenceFactor(source, target image.Point, headroom int) int {
	if headroom == 0 {
		headroom = 4
	}
	goal := func(source, target int) uint64 {
		s, d, h := uint64(source), uint64(target), uint64(headroom)
		if d > s/h {
			return s
		}
		return d * h
	}
	w, h := goal(source.X, target.X), goal(source.Y, target.Y)
	for _, factor := range []uint64{8, 4, 2, 1} {
		if (uint64(source.X)-1)/factor+1 >= w && (uint64(source.Y)-1)/factor+1 >= h {
			return int(factor)
		}
	}
	return 1
}

// Full-resolution integration reference: standard JPEG, clipped opaque RGBA,
// then x/image/draw. None of these kernels calls a production filter helper.
func resizeOptionsReference(src image.Image, width, height int, filter Filter) *image.RGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Src)
	if filter == Lanczos3 {
		return lanczosTestReference(rgba, width, height)
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	var scaler xdraw.Scaler = xdraw.BiLinear
	if filter == CatmullRom {
		scaler = xdraw.CatmullRom
	}
	scaler.Scale(dst, dst.Bounds(), rgba, rgba.Bounds(), draw.Src, nil)
	return dst
}

// Like thumbnailDenseReference, this is destination-driven and retains a full
// floating-point horizontal image. It independently evaluates mathematical
// kernels and uses original/factor coordinates, NEVER the ceil-rounded grid.
// Both passes widen their support when reducing; no intermediate clamp or
// quantization is allowed. The production span/ring/kernel code is not reused.
func resizeOptionsDenseReference(src image.Image, original image.Point, factor, width, height int, filter Filter) *image.RGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Src)
	support := 3.0
	if filter == CatmullRom {
		support = 2
	} else if filter == Bilinear {
		support = 1
	}
	kernel := func(distance float64) float64 {
		x := math.Abs(distance)
		if x >= support {
			return 0
		}
		switch filter {
		case Bilinear:
			return 1 - x
		case CatmullRom:
			if x < 1 {
				return 1 - 2.5*x*x + 1.5*x*x*x
			}
			return 2 - 4*x + 2.5*x*x - 0.5*x*x*x
		default:
			if x == 0 {
				return 1
			}
			a := math.Pi * x
			return (math.Sin(a) / a) * (math.Sin(a/3) / (a / 3))
		}
	}
	type samples struct {
		first int
		w     []float64
	}
	weights := func(n, original, outputs int) []samples {
		rows := make([]samples, outputs)
		step := float64(original) / float64(factor) / float64(outputs)
		scale := math.Max(1, step)
		for out := range rows {
			center := (float64(out)+0.5)*step - 0.5
			first := max(0, int(math.Ceil(center-support*scale)))
			last := min(n, int(math.Floor(center+support*scale))+1)
			row := samples{first: first, w: make([]float64, last-first)}
			sum := 0.0
			for in := range row.w {
				row.w[in] = kernel((float64(first+in) - center) / scale)
				sum += row.w[in]
			}
			for in := range row.w {
				row.w[in] /= sum
			}
			rows[out] = row
		}
		return rows
	}
	xw, yw := weights(rgba.Rect.Dx(), original.X, width), weights(rgba.Rect.Dy(), original.Y, height)
	horizontal := make([]float64, rgba.Rect.Dy()*width*3)
	for y := 0; y < rgba.Rect.Dy(); y++ {
		for x, row := range xw {
			for offset, weight := range row.w {
				for c := 0; c < 3; c++ {
					horizontal[(y*width+x)*3+c] += float64(rgba.Pix[y*rgba.Stride+4*(row.first+offset)+c]) / 255 * weight
				}
			}
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y, row := range yw {
		for x := 0; x < width; x++ {
			for c := 0; c < 3; c++ {
				value := 0.0
				for offset, weight := range row.w {
					value += horizontal[((row.first+offset)*width+x)*3+c] * weight
				}
				// x/image/draw rounds to 16 bits, then retains the high byte.
				value = math.Max(0, math.Min(65535, value*65535+0.5))
				dst.Pix[y*dst.Stride+4*x+c] = uint8(uint16(value) >> 8)
			}
			dst.Pix[y*dst.Stride+4*x+3] = 255
		}
	}
	return dst
}

func resizeOptionsMaxDifference(a, b *image.RGBA) int {
	largest := 0
	for y := 0; y < a.Rect.Dy(); y++ {
		for x := 0; x < a.Rect.Dx(); x++ {
			for c := 0; c < 3; c++ {
				d := int(a.Pix[y*a.Stride+4*x+c]) - int(b.Pix[y*b.Stride+4*x+c])
				largest = max(largest, d, -d)
			}
		}
	}
	return largest
}

func TestResizeOptionsFilterValues(t *testing.T) {
	if Lanczos3 != Filter(0) || CatmullRom != Filter(1) || Bilinear != Filter(2) {
		t.Fatal("public filter values changed; zero must remain Lanczos3")
	}
}

func TestResizeOptionsInvalidBeforeRead(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, api := range resizeOptionsAPIs {
		for _, opts := range []ResizeOptions{
			{Filter: Filter(-1)}, {Filter: Filter(99)}, {Filter: Filter(maxInt)}, {Filter: Filter(-maxInt - 1)},
			{ThumbnailHeadroom: -1}, {Filter: Bilinear, ThumbnailHeadroom: -99},
			{Filter: CatmullRom, ThumbnailHeadroom: -maxInt - 1},
		} {
			t.Run(fmt.Sprintf("%s/filter=%d/headroom=%d", api.name, opts.Filter, opts.ThumbnailHeadroom), func(t *testing.T) {
				opts.Workers, opts.AllowFullDecode = 4, true
				before := opts
				r := new(resizeStreamNoRead)
				got, err := api.public(r, 1, 1, &opts)
				var unsupported UnsupportedError
				if got != nil || !errors.As(err, &unsupported) || r.reads != 0 {
					t.Fatalf("image %v error %v after %d reads; want UnsupportedError before Read", got, err, r.reads)
				}
				if !reflect.DeepEqual(opts, before) {
					t.Fatal("decoder modified caller options")
				}
			})
		}
	}
}

func TestResizeOptionsDefaultsBitExact(t *testing.T) {
	data := resizeStreamEncode(t, resizeStreamPattern(513, 385, false))
	for _, api := range resizeOptionsAPIs {
		t.Run(api.name, func(t *testing.T) {
			var first *image.RGBA
			for _, opts := range []*ResizeOptions{
				nil, {}, {Filter: Lanczos3}, {ThumbnailHeadroom: 4},
				{Filter: Lanczos3, ThumbnailHeadroom: 4},
				{Workers: 1}, {Workers: 2}, {Workers: 4},
				{Workers: 1, Filter: Lanczos3, ThumbnailHeadroom: 4},
				{Workers: 2, Filter: Lanczos3, ThumbnailHeadroom: 4},
				{Workers: 4, Filter: Lanczos3, ThumbnailHeadroom: 4},
			} {
				got, d, err := api.decode(bytes.NewReader(data), 32, 24, opts)
				if err != nil {
					t.Fatal(err)
				}
				wantFactor := 1
				if api.thumbnail {
					wantFactor = 4
					thumbnailAssertBounded(t, d, got)
				} else {
					resizeStreamAssertBounded(t, d, got)
				}
				if d.factor != wantFactor {
					t.Fatalf("default factor %d, want %d", d.factor, wantFactor)
				}
				if first == nil {
					first = got
				} else if !samePixels(got, first) {
					t.Fatalf("options %+v changed default pixels", opts)
				}
				public, err := api.public(bytes.NewReader(data), 32, 24, opts)
				if err != nil || !samePixels(public, first) {
					t.Fatalf("public endpoint changed default pixels: %v", err)
				}
			}
		})
	}
}

func TestResizeOptionsChooseHeadroom(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		source, target image.Point
		headroom, want int
	}{
		{image.Pt(4000, 3000), image.Pt(256, 192), 0, 2},
		{image.Pt(4000, 3000), image.Pt(256, 192), 1, 8},
		{image.Pt(4000, 3000), image.Pt(256, 192), 2, 4},
		{image.Pt(4000, 3000), image.Pt(256, 192), 4, 2},
		{image.Pt(4000, 3000), image.Pt(256, 192), 8, 1},
		{image.Pt(8192, 6144), image.Pt(64, 48), 2, 8},
		{image.Pt(8192, 6144), image.Pt(64, 48), 4, 8},
		{image.Pt(513, 385), image.Pt(32, 24), 3, 4},
		{image.Pt(512, 3), image.Pt(16, 1), 2, 2},
		{image.Pt(1, 512), image.Pt(1, 16), 2, 8},
		{image.Pt(17, 9), image.Pt(1, 1), maxInt, 1},
		{image.Pt(maxInt, maxInt), image.Pt(1, 1), maxInt, 1},
		{image.Pt(maxInt, maxInt), image.Pt(2, 2), maxInt, 1},
		{image.Pt(maxInt, maxInt), image.Pt(2, 2), maxInt/2 + 1, 1},
		{image.Pt(maxInt, maxInt), image.Pt(1, 1), maxInt / 8, 8},
		{image.Pt(maxInt, maxInt), image.Pt(2, 2), maxInt / 16, 8},
		{image.Pt(maxInt, 3), image.Pt(maxInt, 1), maxInt, 1},
		{image.Pt(3, maxInt), image.Pt(1, maxInt), maxInt, 1},
	} {
		got := chooseThumbnailFactorHeadroom(tc.source.X, tc.source.Y, tc.target.X, tc.target.Y, tc.headroom)
		if got != tc.want || resizeOptionsReferenceFactor(tc.source, tc.target, tc.headroom) != tc.want {
			t.Errorf("source %v target %v headroom %d: factor %d, want %d", tc.source, tc.target, tc.headroom, got, tc.want)
		}
	}
	for _, sw := range []int{1, 2, 3, 7, 8, 9, 17, 31, 32, 33, 63, 64, 65, maxInt / 16, maxInt / 2, maxInt} {
		for _, sh := range []int{1, 3, 8, 31, 65, maxInt} {
			for _, target := range []image.Point{{1, 1}, {min(4, sw), min(3, sh)}, {sw, sh}} {
				for _, headroom := range []int{0, 1, 2, 3, 4, 8, 17, maxInt / 2, maxInt} {
					want := resizeOptionsReferenceFactor(image.Pt(sw, sh), target, headroom)
					if got := chooseThumbnailFactorHeadroom(sw, sh, target.X, target.Y, headroom); got != want {
						t.Fatalf("source %dx%d target %v headroom %d: factor %d, want %d", sw, sh, target, headroom, got, want)
					}
					if headroom == 4 && chooseThumbnailFactor(sw, sh, target.X, target.Y) != want {
						t.Fatal("legacy four-argument factor helper changed policy")
					}
				}
			}
		}
	}
	// For the usual photograph lowering 4x to 2x headroom doubles each IDCT
	// reduction, hence quarters intermediate pixels. The factor-8 cap means
	// this is not a universal fourfold saving (the capped examples above).
	pixels := func(headroom int) int {
		factor := chooseThumbnailFactorHeadroom(4000, 3000, 256, 192, headroom)
		return ((4000-1)/factor + 1) * ((3000-1)/factor + 1)
	}
	if pixels(4) != 4*pixels(2) {
		t.Fatal("4000x3000 -> 256x192 no longer quarters intermediate pixels at headroom 2")
	}
}

func TestResizeOptionsFilterHeadroomMatrix(t *testing.T) {
	for _, tc := range []struct {
		name       string
		data       []byte
		target     image.Point
		fallback   bool
		oracleData []byte
	}{
		{"even", resizeStreamEncode(t, resizeStreamPattern(256, 192, false)), image.Pt(32, 24), false, nil},
		{"odd", resizeStreamEncode(t, resizeStreamPattern(257, 193, false)), image.Pt(32, 24), false, nil},
		{"gray", resizeStreamEncode(t, resizeStreamPattern(129, 97, true)), image.Pt(16, 12), false, nil},
		{"tiny", resizeStreamEncode(t, resizeStreamPattern(3, 5, true)), image.Pt(1, 1), false, nil},
		{"one-column", resizeStreamEncode(t, resizeStreamPattern(1, 33, false)), image.Pt(1, 3), false, nil},
		{"RGB", readJPEG(t, "testdata/video-001.rgb.jpeg"), image.Pt(16, 12), false, nil},
		{"CMYK", resizeStreamConstantCMYK(0, false), image.Pt(1, 1), false, nil},
		{"YCCK", resizeStreamConstantCMYK(2, true), image.Pt(1, 1), false, nil},
		{"progressive", readJPEG(t, "testdata/video-001.progressive.jpeg"), image.Pt(7, 5), true, readJPEG(t, "testdata/video-001.jpeg")},
		{"separate-scans", resizeStreamSeparateScans(), image.Pt(1, 1), true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oracleData := tc.oracleData
			if oracleData == nil {
				oracleData = tc.data
			}
			fullSource, err := jpeg.Decode(bytes.NewReader(oracleData))
			if err != nil {
				t.Fatal(err)
			}
			source := fullSource.Bounds().Size()
			pixels := uint64(source.X) * uint64(source.Y)
			for _, api := range resizeOptionsAPIs {
				for _, filter := range resizeOptionsFilters {
					t.Run(fmt.Sprintf("%s/filter=%d", api.name, filter), func(t *testing.T) {
						fullWant := resizeOptionsReference(fullSource, tc.target.X, tc.target.Y, filter)
						var fullSerial *image.RGBA
						for _, headroom := range []int{0, 1, 2, 3, 4, 8, int(^uint(0) >> 1)} {
							t.Run(fmt.Sprintf("headroom=%d", headroom), func(t *testing.T) {
								factor, want := 1, fullWant
								if api.thumbnail {
									factor = resizeOptionsReferenceFactor(source, tc.target, headroom)
									reduced, err := Decode(bytes.NewReader(oracleData), factor)
									if err != nil {
										t.Fatal(err)
									}
									want = resizeOptionsDenseReference(reduced, source, factor, tc.target.X, tc.target.Y, filter)
								}
								var serial *image.RGBA
								for _, workers := range []int{1, 2, 4} {
									opts := ResizeOptions{Workers: workers, AllowFullDecode: tc.fallback, Filter: filter, ThumbnailHeadroom: headroom, MaxSourcePixels: pixels}
									before := opts
									got, d, err := api.decode(bytes.NewReader(tc.data), tc.target.X, tc.target.Y, &opts)
									resizeStreamAssertJoined(t, d)
									if err != nil {
										t.Fatalf("workers %d: %v", workers, err)
									}
									if !reflect.DeepEqual(opts, before) {
										t.Fatal("decoder modified caller options")
									}
									if d.factor != factor {
										t.Fatalf("workers %d: factor %d, want %d", workers, d.factor, factor)
									}
									if api.thumbnail {
										if tc.fallback {
											thumbnailAssertAllowedBuffers(t, d, got)
										} else {
											thumbnailAssertBounded(t, d, got)
										}
									} else if tc.fallback {
										if !d.resize.fallback || d.resize.streaming || d.resize.pipeline != nil {
											t.Fatal("full-IDCT fallback changed its buffer policy")
										}
									} else {
										resizeStreamAssertBounded(t, d, got)
									}
									resizeStreamAssertNear(t, got, want)
									if serial == nil {
										serial = got
									} else if !samePixels(got, serial) {
										t.Fatalf("workers %d changed pixels", workers)
									}
									if !api.thumbnail {
										if fullSerial == nil {
											fullSerial = got
										} else if !samePixels(got, fullSerial) {
											t.Fatal("DecodeResize used ThumbnailHeadroom; want unchanged full IDCT")
										}
									}
								}
							})
						}
					})
				}
			}
		})
	}
}

func TestResizeOptionsFiltersObservable(t *testing.T) {
	data := resizeStreamEncode(t, lanczosTestSource(257, 129, "edge"))
	for _, api := range resizeOptionsAPIs {
		t.Run(api.name, func(t *testing.T) {
			var images []*image.RGBA
			for _, filter := range resizeOptionsFilters {
				got, _, err := api.decode(bytes.NewReader(data), 37, 19, &ResizeOptions{Filter: filter, ThumbnailHeadroom: 8, Workers: 2})
				if err != nil {
					t.Fatal(err)
				}
				for i, other := range images {
					if resizeOptionsMaxDifference(got, other) <= 2 {
						t.Fatalf("edge fixture does not distinguish filters %d and %d", filter, resizeOptionsFilters[i])
					}
				}
				images = append(images, got)
			}
		})
	}
	// A four-source-sample bilinear interpolator is not an antialiasing
	// reduction filter. Make this failure observable after real JPEG decoding.
	stripes := image.NewGray(image.Rect(0, 0, 512, 257))
	for y := 0; y < stripes.Rect.Dy(); y++ {
		for x := 0; x < stripes.Rect.Dx(); x++ {
			stripes.Pix[y*stripes.Stride+x] = byte(((x/3 + y/5) % 2) * 255)
		}
	}
	data = resizeStreamEncode(t, stripes)
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	want := resizeOptionsReference(src, 13, 9, Bilinear)
	rgba := image.NewRGBA(src.Bounds())
	draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Src)
	naive := image.NewRGBA(want.Bounds())
	xdraw.ApproxBiLinear.Scale(naive, naive.Bounds(), rgba, rgba.Bounds(), draw.Src, nil)
	if resizeOptionsMaxDifference(want, naive) <= 20 {
		t.Fatal("stripe fixture does not expose naive four-sample bilinear aliasing")
	}
	for _, api := range resizeOptionsAPIs {
		// Huge positive headroom forces factor 1 for thumbnails; full resize
		// must ignore it. Both still need scale-widened bilinear filtering.
		got, d, err := api.decode(bytes.NewReader(data), 13, 9, &ResizeOptions{Filter: Bilinear, ThumbnailHeadroom: int(^uint(0) >> 1), Workers: 4})
		if err != nil || d.factor != 1 {
			t.Fatalf("%s: factor or error: %v", api.name, err)
		}
		resizeStreamAssertNear(t, got, want)
		if resizeOptionsMaxDifference(got, naive) <= 20 {
			t.Fatalf("%s aliased like a four-sample interpolator", api.name)
		}
	}
}

func TestResizeOptionsOddCoordinateReference(t *testing.T) {
	original, target := image.Pt(513, 385), image.Pt(32, 24)
	data := resizeStreamEncode(t, resizeStreamPattern(original.X, original.Y, false))
	src, err := Decode(bytes.NewReader(data), 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, filter := range resizeOptionsFilters {
		want := resizeOptionsDenseReference(src, original, 4, target.X, target.Y, filter)
		rounded := resizeOptionsReference(src, target.X, target.Y, filter)
		if resizeOptionsMaxDifference(want, rounded) <= 1 {
			t.Fatalf("filter %d fixture does not distinguish original/factor coordinates from ceil grid", filter)
		}
		got, d, err := decodeThumbnail(bytes.NewReader(data), target.X, target.Y, &ResizeOptions{Filter: filter, ThumbnailHeadroom: 4, Workers: 2})
		if err != nil || d.factor != 4 {
			t.Fatalf("filter %d: %v", filter, err)
		}
		resizeStreamAssertNear(t, got, want)
		if filter == Lanczos3 {
			resizeStreamAssertNear(t, want, thumbnailDenseReference(src, original, 4, target.X, target.Y))
		}
	}
}

func TestResizeOptionsScratchIndependentOfSourceHeight(t *testing.T) {
	short := resizeStreamEncode(t, resizeStreamPattern(512, 512, false))
	tall := resizeStreamEncode(t, resizeStreamPattern(512, 1024, false))
	for _, filter := range resizeOptionsFilters {
		for _, workers := range []int{1, 2, 4} {
			var first resizeStreamMemory
			for i, data := range [][]byte{short, tall} {
				got, d, err := decodeThumbnail(bytes.NewReader(data), 16, 16, &ResizeOptions{Filter: filter, ThumbnailHeadroom: 2, Workers: workers})
				if err != nil {
					t.Fatal(err)
				}
				if d.factor != 8 {
					t.Fatalf("factor %d, want 8", d.factor)
				}
				memory := thumbnailAssertBounded(t, d, got)
				if i == 0 {
					first = memory
				} else if memory != first {
					t.Fatalf("filter %d workers %d: source height changed scratch: %+v vs %+v", filter, workers, first, memory)
				}
			}
		}
	}
}

func TestResizeOptionsSourcePolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		full bool
	}{
		{"baseline", resizeStreamEncode(t, resizeStreamPattern(257, 193, false)), false},
		{"progressive", readJPEG(t, "testdata/video-001.progressive.jpeg"), true},
		{"separate", resizeStreamSeparateScans(), true},
	} {
		cfg, err := DecodeConfig(bytes.NewReader(tc.data))
		if err != nil {
			t.Fatal(err)
		}
		pixels := uint64(cfg.Width) * uint64(cfg.Height)
		_, _, sofEnd := resizeStreamSegment(t, tc.data, 0xc0, 0xc1, 0xc2)
		_, _, sosEnd := resizeStreamSegment(t, tc.data, 0xda)
		for _, api := range resizeOptionsAPIs {
			for _, filter := range resizeOptionsFilters {
				t.Run(fmt.Sprintf("%s/%s/filter=%d", tc.name, api.name, filter), func(t *testing.T) {
					opts := &ResizeOptions{Workers: 4, Filter: filter, ThumbnailHeadroom: 2, AllowFullDecode: tc.full, MaxSourcePixels: pixels - 1}
					r := &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(tc.data))}
					got, d, err := api.decode(r, 1, 1, opts)
					thumbnailAssertRejectedBeforeBuffers(t, d)
					var unsupported UnsupportedError
					if got != nil || !errors.As(err, &unsupported) || r.n > sofEnd {
						t.Fatalf("original source budget: image %v error %v after %d bytes; SOF ends %d", got, err, r.n, sofEnd)
					}
					opts.MaxSourcePixels = pixels
					// Headroom 1 must not accidentally authorize upsampling merely
					// because reduced sample dimensions are rounded upward.
					opts.ThumbnailHeadroom = 1
					r = &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(tc.data))}
					got, d, err = api.decode(r, cfg.Width+1, 1, opts)
					thumbnailAssertRejectedBeforeBuffers(t, d)
					if got != nil || !errors.As(err, &unsupported) || r.n > sofEnd {
						t.Fatalf("upsampling: image %v error %v after %d bytes", got, err, r.n)
					}
					if tc.full {
						opts.AllowFullDecode = false
						r = &resizeStreamCountingReader{r: iotest.OneByteReader(bytes.NewReader(tc.data))}
						got, d, err = api.decode(r, 1, 1, opts)
						thumbnailAssertRejectedBeforeBuffers(t, d)
						if got != nil || !errors.As(err, &unsupported) || r.n > sosEnd {
							t.Fatalf("fallback opt-in: image %v error %v after %d bytes; SOS ends %d", got, err, r.n, sosEnd)
						}
					}
				})
			}
		}
	}
}

func TestResizeOptionsCannotReplayStream(t *testing.T) {
	color := readJPEG(t, "testdata/video-001.q50.420.jpeg")
	_, sosStart, sosEnd := resizeStreamSegment(t, color, 0xda)
	for _, tc := range []struct {
		name        string
		data, after []byte
	}{
		{"late-Adobe", readJPEG(t, "testdata/video-001.cmyk.jpeg"), append([]byte{0xff, 0xee, 0, 14}, []byte("Adobe\x00\x64\x00\x00\x00\x00\x02")...)},
		{"second-SOS", color, color[sosStart:sosEnd]},
	} {
		data := append(bytes.Clone(tc.data[:len(tc.data)-2]), tc.after...)
		data = append(data, 0xff, 0xd9)
		for _, api := range resizeOptionsAPIs {
			for _, filter := range []Filter{CatmullRom, Bilinear} {
				t.Run(fmt.Sprintf("%s/%s/filter=%d", tc.name, api.name, filter), func(t *testing.T) {
					got, d, err := api.decode(bytes.NewReader(data), 16, 12, &ResizeOptions{
						Filter: filter, ThumbnailHeadroom: 2, Workers: 4, AllowFullDecode: true,
					})
					var unsupported UnsupportedError
					if got != nil || !errors.As(err, &unsupported) {
						t.Fatalf("image %v error %v; AllowFullDecode must not permit replaying a streamed scan", got, err)
					}
					if api.thumbnail {
						thumbnailAssertBounded(t, d, nil)
					} else {
						resizeStreamAssertBounded(t, d, nil)
					}
				})
			}
		}
	}
}

func TestResizeOptionsShortReadsAndErrorJoining(t *testing.T) {
	readErr := errors.New("injected resize-options reader failure")
	for _, tc := range []struct {
		name string
		data []byte
		full bool
	}{
		{"odd", resizeStreamEncode(t, resizeStreamPattern(257, 193, false)), false},
		{"progressive", readJPEG(t, "testdata/video-001.progressive.jpeg"), true},
		{"separate", resizeStreamSeparateScans(), true},
	} {
		for _, api := range resizeOptionsAPIs {
			for _, filter := range resizeOptionsFilters {
				t.Run(fmt.Sprintf("%s/%s/filter=%d", tc.name, api.name, filter), func(t *testing.T) {
					opts := &ResizeOptions{Workers: 4, Filter: filter, ThumbnailHeadroom: 2, AllowFullDecode: tc.full}
					want, err := api.public(bytes.NewReader(tc.data), 1, 1, opts)
					if err != nil {
						t.Fatal(err)
					}
					for _, reader := range []io.Reader{
						iotest.OneByteReader(bytes.NewReader(tc.data)),
						iotest.HalfReader(bytes.NewReader(tc.data)),
						iotest.DataErrReader(bytes.NewReader(tc.data)),
					} {
						got, d, err := api.decode(reader, 1, 1, opts)
						resizeStreamAssertJoined(t, d)
						if err != nil || !samePixels(got, want) {
							t.Fatalf("short reader changed pixels: %v", err)
						}
					}
					for _, cut := range []int{len(tc.data) / 2, len(tc.data) - 2} {
						got, d, err := api.decode(bytes.NewReader(tc.data[:cut]), 1, 1, opts)
						resizeStreamAssertJoined(t, d)
						if got != nil || err == nil {
							t.Fatalf("accepted truncation at %d: %v", cut, err)
						}
					}
					reader := io.MultiReader(bytes.NewReader(tc.data[:len(tc.data)/2]), iotest.ErrReader(readErr))
					got, d, err := api.decode(reader, 1, 1, opts)
					resizeStreamAssertJoined(t, d)
					if got != nil || !errors.Is(err, readErr) {
						t.Fatalf("reader failure: image %v error %v", got, err)
					}
				})
			}
		}
	}
}

func FuzzResizeOptions(f *testing.F) {
	seeds := [][]byte{
		resizeStreamEncode(f, resizeStreamPattern(257, 193, false)),
		resizeStreamEncode(f, resizeStreamPattern(17, 9, true)),
		readJPEG(f, "testdata/video-001.rgb.jpeg"),
		readJPEG(f, "testdata/video-001.progressive.jpeg"),
		resizeStreamConstantCMYK(2, true), resizeStreamSeparateScans(),
	}
	for _, data := range seeds {
		for _, filter := range []int{0, 1, 2} {
			for _, headroom := range []int{0, 1, 2, 4, 8, int(^uint(0) >> 1)} {
				f.Add(data, uint32(7|5<<16), filter, headroom)
			}
		}
	}
	for _, invalid := range [][2]int{{-1, 4}, {99, 2}, {0, -1}, {2, -99}} {
		f.Add(seeds[0], uint32(0), invalid[0], invalid[1])
	}
	f.Fuzz(func(t *testing.T, data []byte, size uint32, filter, headroom int) {
		if len(data) > 1<<20 {
			return
		}
		opts := &ResizeOptions{Filter: Filter(filter), ThumbnailHeadroom: headroom, Workers: 1 + int(size%2), MaxSourcePixels: 1024 * 1024}
		if filter < 0 || filter > 2 || headroom < 0 {
			for _, api := range resizeOptionsAPIs {
				r := new(resizeStreamNoRead)
				got, err := api.public(r, 1, 1, opts)
				var unsupported UnsupportedError
				if got != nil || !errors.As(err, &unsupported) || r.reads != 0 {
					t.Fatalf("%s accepted invalid options or read input: %v (%d reads)", api.name, err, r.reads)
				}
			}
			return
		}
		cfg, err := DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 1024 || cfg.Height > 1024 {
			return
		}
		width := 1 + int(size&0xffff)%min(64, cfg.Width)
		height := 1 + int(size>>16)%min(48, cfg.Height)
		for _, api := range resizeOptionsAPIs {
			for _, allow := range []bool{false, true} {
				opts.AllowFullDecode = allow
				got, d, err := api.decode(bytes.NewReader(data), width, height, opts)
				resizeStreamAssertJoined(t, d)
				if d != nil && d.resize != nil && d.resize.streaming && !d.resize.fallback {
					if api.thumbnail {
						thumbnailAssertBounded(t, d, got)
					} else {
						resizeStreamAssertBounded(t, d, got)
					}
				}
				if err != nil {
					if got != nil {
						t.Fatal("error returned a partial image")
					}
					continue
				}
				factor := 1
				if api.thumbnail {
					factor = resizeOptionsReferenceFactor(image.Pt(cfg.Width, cfg.Height), image.Pt(width, height), headroom)
				}
				if d.factor != factor || got == nil || got.Bounds() != image.Rect(0, 0, width, height) {
					t.Fatalf("%s success has incorrect bounds or factor", api.name)
				}
				for y := 0; y < height; y++ {
					for x := 0; x < width; x++ {
						if got.RGBAAt(x, y).A != 255 {
							t.Fatal("successful image is not opaque")
						}
					}
				}
			}
		}
	})
}
