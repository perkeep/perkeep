/*
Copyright 2012 The Camlistore Authors.

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

package images

import (
	"image"
	"image/jpeg"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"camlistore.org/third_party/github.com/camlistore/goexif/exif"
)

const datadir = "testdata"

func equals(im1, im2 image.Image) bool {
	for y := 0; y < im1.Bounds().Dy(); y++ {
		for x := 0; x < im1.Bounds().Dx(); x++ {
			r1, g1, b1, a1 := im1.At(x, y).RGBA()
			r2, g2, b2, a2 := im2.At(x, y).RGBA()
			if !(r1 == r2 && g1 == g2 && b1 == b2 && a1 == a2) {
				return false
			}
		}
	}
	return true
}

func straightFImage(t *testing.T) image.Image {
	g, err := os.Open(path.Join(datadir, "f1.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	straightF, err := jpeg.Decode(g)
	if err != nil {
		t.Fatal(err)
	}
	return straightF
}

func sampleNames(t *testing.T) []string {
	dir, err := os.Open(datadir)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	samples, err := dir.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}
	return samples
}

func TestExifCorrection(t *testing.T) {
	samples := sampleNames(t)
	straightF := straightFImage(t)
	for _, v := range samples {
		if !strings.Contains(v, "exif") {
			continue
		}
		name := path.Join(datadir, v)
		t.Logf("correcting %s with EXIF Orientation", name)
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		im, _, err := Decode(f, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !equals(im, straightF) {
			t.Fatalf("%v not properly corrected with exif", name)
		}
	}
}

func TestForcedCorrection(t *testing.T) {
	samples := sampleNames(t)
	straightF := straightFImage(t)
	for _, v := range samples {
		name := path.Join(datadir, v)
		t.Logf("forced correction of %s", name)
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		num := name[10]
		angle, flipMode := 0, 0
		switch num {
		case '1':
			// nothing to do
		case '2':
			flipMode = 2
		case '3':
			angle = 180
		case '4':
			angle = 180
			flipMode = 2
		case '5':
			angle = -90
			flipMode = 2
		case '6':
			angle = -90
		case '7':
			angle = 90
			flipMode = 2
		case '8':
			angle = 90
		}
		im, _, err := Decode(f, &DecodeOpts{Rotate: angle, Flip: FlipDirection(flipMode)})
		if err != nil {
			t.Fatal(err)
		}
		if !equals(im, straightF) {
			t.Fatalf("%v not properly corrected", name)
		}
	}
}

// TODO(mpl): move this test to the goexif lib if/when we contribute
// back the DateTime stuff to upstream.
func TestDateTime(t *testing.T) {
	f, err := os.Open(path.Join(datadir, "f1-exif.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ex, err := exif.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ex.DateTime()
	if err != nil {
		t.Fatal(err)
	}
	exifTimeLayout := "2006:01:02 15:04:05"
	want, err := time.Parse(exifTimeLayout, "2012:11:04 05:42:02")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Creation times differ; got %v, want: %v\n", got, want)
	}
}
