/*
Copyright 2013 The Camlistore Authors

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

package resize

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// psnrThreshold is the threshold over which images must match to consider
	// HalveInplace equivalent to Resize. It is in terms of dB and 60-80 is
	// good for RGB.
	psnrThreshold = 50.0

	// TODO(wathiede, mpl): figure out why we got an increase from ~3% to ~16% for
	// YCbCr images in Go 1.5. That is, for halving vs resizing.
	maxPixelDiffPercentage = 10
)

var (
	output = flag.String("output", "", "If non-empty, the directory to save comparison images.")

	orig  = image.Rect(0, 0, 1024, 1024)
	thumb = image.Rect(0, 0, 64, 64)
)

var somePalette = []color.Color{
	color.RGBA{0x00, 0x00, 0x00, 0xff},
	color.RGBA{0x00, 0x00, 0x44, 0xff},
	color.RGBA{0x00, 0x00, 0x88, 0xff},
	color.RGBA{0x00, 0x00, 0xcc, 0xff},
	color.RGBA{0x00, 0x44, 0x00, 0xff},
	color.RGBA{0x00, 0x44, 0x44, 0xff},
	color.RGBA{0x00, 0x44, 0x88, 0xff},
	color.RGBA{0x00, 0x44, 0xcc, 0xff},
}

func makeImages(r image.Rectangle) []image.Image {
	return []image.Image{
		image.NewGray(r),
		image.NewGray16(r),
		image.NewNRGBA(r),
		image.NewNRGBA64(r),
		image.NewPaletted(r, somePalette),
		image.NewRGBA(r),
		image.NewRGBA64(r),
		image.NewYCbCr(r, image.YCbCrSubsampleRatio444),
		image.NewYCbCr(r, image.YCbCrSubsampleRatio422),
		image.NewYCbCr(r, image.YCbCrSubsampleRatio420),
		image.NewYCbCr(r, image.YCbCrSubsampleRatio440),
		image.NewYCbCr(r, image.YCbCrSubsampleRatio410),
		image.NewYCbCr(r, image.YCbCrSubsampleRatio411),
	}
}

func TestResize(t *testing.T) {
	for i, im := range makeImages(orig) {
		m := Resize(im, orig, thumb.Dx(), thumb.Dy())
		got, want := m.Bounds(), thumb
		if !got.Eq(want) {
			t.Error(i, "Want bounds", want, "got", got)
		}
	}
}

func TestResampleInplace(t *testing.T) {
	for i, im := range makeImages(orig) {
		m := ResampleInplace(im, orig, thumb.Dx(), thumb.Dy())
		got, want := m.Bounds(), thumb
		if !got.Eq(want) {
			t.Error(i, "Want bounds", want, "got", got)
		}
	}
}

func TestResample(t *testing.T) {
	for i, im := range makeImages(orig) {
		m := Resample(im, orig, thumb.Dx(), thumb.Dy())
		got, want := m.Bounds(), thumb
		if !got.Eq(want) {
			t.Error(i, "Want bounds", want, "got", got)
		}
	}

	for _, d := range []struct {
		wantFn string
		r      image.Rectangle
		w, h   int
	}{
		{
			// Generated with imagemagick:
			// $ convert -crop 128x128+320+160 -resize 64x64 -filter point \
			//      testdata/test.png testdata/test-resample-128x128-64x64.png
			wantFn: "test-resample-128x128-64x64.png",
			r:      image.Rect(320, 160, 320+128, 160+128),
			w:      64,
			h:      64,
		},
		{
			// Generated with imagemagick:
			// $ convert -resize 128x128 -filter point testdata/test.png \
			//      testdata/test-resample-768x576-128x96.png
			wantFn: "test-resample-768x576-128x96.png",
			r:      image.Rect(0, 0, 768, 576),
			w:      128,
			h:      96,
		},
	} {
		m := image.NewRGBA(testIm.Bounds())
		fillTestImage(m)
		r, err := os.Open(filepath.Join("testdata", d.wantFn))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		want, err := png.Decode(r)
		if err != nil {
			t.Fatal(err)
		}
		got := Resample(m, d.r, d.w, d.h)
		res := compareImages(got, want)
		t.Logf("PSNR %.4f", res.psnr)
		s := got.Bounds().Size()
		tot := s.X * s.Y
		per := float32(100*res.diffCnt) / float32(tot)
		t.Logf("Resample not the same %d pixels different %.2f%%", res.diffCnt, per)
		if *output != "" {
			err = savePng(t, want, fmt.Sprintf("Resample.%s->%dx%d.want.png",
				d.r, d.w, d.h))
			if err != nil {
				t.Fatal(err)
			}
			err = savePng(t, got, fmt.Sprintf("Resample.%s->%dx%d.got.png",
				d.r, d.w, d.h))
			if err != nil {
				t.Fatal(err)
			}
			err = savePng(t, res.diffIm,
				fmt.Sprintf("Resample.%s->%dx%d.diff.png", d.r, d.w, d.h))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestHalveInplace(t *testing.T) {
	for i, im := range makeImages(orig) {
		m := HalveInplace(im)
		b := im.Bounds()
		got, want := m.Bounds(), image.Rectangle{
			Min: b.Min,
			Max: b.Min.Add(b.Max.Div(2)),
		}
		if !got.Eq(want) {
			t.Error(i, "Want bounds", want, "got", got)
		}
	}
}

type results struct {
	diffCnt int
	psnr    float64
	diffIm  *image.Gray
}

func compareImages(m1, m2 image.Image) results {
	b := m1.Bounds()
	s := b.Size()
	res := results{}
	mse := uint32(0)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r1, g1, b1, a1 := m1.At(x, y).RGBA()
			r2, g2, b2, a2 := m2.At(x, y).RGBA()

			mse += ((r1-r2)*(r1-r2) + (g1-g2)*(g1-g2) + (b1-b2)*(b1-b2)) / 3
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				if res.diffIm == nil {
					res.diffIm = image.NewGray(m1.Bounds())
				}
				res.diffCnt++
				res.diffIm.Set(x, y, color.White)
			}
		}
	}
	mse = mse / uint32(s.X*s.Y)
	res.psnr = 20*math.Log10(1<<16) - 10*math.Log10(float64(mse))
	return res
}

var testIm image.Image

func init() {
	r, err := os.Open(filepath.Join("testdata", "test.png"))
	if err != nil {
		panic(err)
	}
	defer r.Close()
	testIm, err = png.Decode(r)
}

func fillTestImage(im image.Image) {
	b := im.Bounds()
	if !b.Eq(testIm.Bounds()) {
		panic("Requested target image dimensions not equal reference image.")
	}
	src := testIm
	if dst, ok := im.(*image.YCbCr); ok {
		b := testIm.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, b, _ := src.At(x, y).RGBA()
				yp, cb, cr := color.RGBToYCbCr(uint8(r), uint8(g), uint8(b))

				dst.Y[dst.YOffset(x, y)] = yp
				off := dst.COffset(x, y)
				dst.Cb[off] = cb
				dst.Cr[off] = cr
			}
		}
		return
	}
	draw.Draw(im.(draw.Image), b, testIm, b.Min, draw.Src)
}

func savePng(t *testing.T, m image.Image, fn string) error {
	fn = filepath.Join(*output, fn)
	t.Log("Saving", fn)
	f, err := os.Create(fn)
	if err != nil {
		return err
	}
	defer f.Close()

	return png.Encode(f, m)
}

func getFilename(im image.Image, method string) string {
	imgType := fmt.Sprintf("%T", im)
	imgType = imgType[strings.Index(imgType, ".")+1:]
	if m, ok := im.(*image.YCbCr); ok {
		imgType += "." + m.SubsampleRatio.String()
	}
	return fmt.Sprintf("%s.%s.png", imgType, method)
}

func TestCompareResizeToHalveInplace(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping TestCompareNewResizeToHalveInplace in short mode.")
	}
	testCompareResizeMethods(t, "resize", "halveInPlace")
}

var resizeMethods = map[string]func(image.Image) image.Image{
	"resize": func(im image.Image) image.Image {
		s := im.Bounds().Size()
		return Resize(im, im.Bounds(), s.X/2, s.Y/2)
	},
	"halveInPlace": func(im image.Image) image.Image {
		return HalveInplace(im)
	},
}

func testCompareResizeMethods(t *testing.T, method1, method2 string) {
	images1, images2 := []image.Image{}, []image.Image{}
	var imTypes []string
	for _, im := range makeImages(testIm.Bounds()) {
		// keeping track of the types for the final output
		imTypes = append(imTypes, fmt.Sprintf("%T", im))
		fillTestImage(im)
		images1 = append(images1, resizeMethods[method1](im))
	}
	for _, im := range makeImages(testIm.Bounds()) {
		fillTestImage(im)
		images2 = append(images2, resizeMethods[method2](im))
	}
	var (
		f   io.WriteCloser
		err error
	)
	if *output != "" {
		os.Mkdir(*output, os.FileMode(0777))
		f, err = os.Create(filepath.Join(*output, "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fmt.Fprintf(f, `<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>Image comparison for `+method1+` vs `+method2+`</title>
  </head>
  <body style="background-color: grey">
<table>
`)
	}
	for i, im1 := range images1 {
		im2 := images2[i]
		res := compareImages(im1, im2)
		if *output != "" {
			fmt.Fprintf(f, "<tr>")
			fn := getFilename(im1, "halve")
			err := savePng(t, im1, fn)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(f, `<td><img src="%s"><br>%s`, fn, fn)

			fn = getFilename(im1, "resize")
			err = savePng(t, im2, fn)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(f, `<td><img src="%s"><br>%s`, fn, fn)

			if res.diffIm != nil {
				fn = getFilename(im1, "diff")
				err = savePng(t, res.diffIm, fn)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(f, `<td><img src="%s"><br>%s`, fn, fn)
			}
			fmt.Fprintln(f)
		}

		if res.psnr < psnrThreshold {
			t.Errorf("%v PSNR too low %.4f", imTypes[i], res.psnr)
		} else {
			t.Logf("%v PSNR %.4f", imTypes[i], res.psnr)
		}
		s := im1.Bounds().Size()
		tot := s.X * s.Y
		if per := float32(100*res.diffCnt) / float32(tot); per > maxPixelDiffPercentage {
			t.Errorf("%v not the same %d pixels different %.2f%%", imTypes[i], res.diffCnt, per)
		}
	}

}
