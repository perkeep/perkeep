/*
Copyright 2012 Google Inc.

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
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"log"

	_ "image/gif"
	_ "image/png"

	"github.com/camlistore/goexif/exif"
)

// The FlipDirection type is used by the Flip option in DecodeOpts
// to indicate in which direction to flip an image.
type FlipDirection int

// FlipVertical and FlipHorizontal are two possible FlipDirections
// values to indicate in which direction an image will be flipped.
const (
	FlipVertical FlipDirection = 1 << iota
	FlipHorizontal
)

type DecodeOpts struct {
	// Rotate specifies how to rotate the image.
	// If nil, the image is rotated automatically based on EXIF metadata.
	// If an int, Rotate is the number of degrees to rotate
	// counter clockwise and must be one of 0, 90, -90, 180, or
	// -180.
	Rotate interface{}

	// Flip specifies how to flip the image.
	// If nil, the image is flipped automatically based on EXIF metadata.
	// Otherwise, Flip is a FlipDirection bitfield indicating how to flip.
	Flip interface{}

	// MaxWidgth and MaxHeight optionally specify bounds on the
	// final image's size.
	MaxWidth, MaxHeight int

	// TODO: consider alternate options if scaled ratio doesn't
	// match original ratio:
	//   Crop    bool   
	//   Stretch bool
}

func rotate(im image.Image, angle int) image.Image {
	var rotated *image.NRGBA
	// trigonometric (i.e counter clock-wise)
	switch angle {
	case 90:
		newH, newW := im.Bounds().Dx(), im.Bounds().Dy()
		rotated = image.NewNRGBA(image.Rect(0, 0, newW, newH))
		for y := 0; y < newH; y++ {
			for x := 0; x < newW; x++ {
				rotated.Set(x, y, im.At(newH-1-y, x))
			}
		}
	case -90:
		newH, newW := im.Bounds().Dx(), im.Bounds().Dy()
		rotated = image.NewNRGBA(image.Rect(0, 0, newW, newH))
		for y := 0; y < newH; y++ {
			for x := 0; x < newW; x++ {
				rotated.Set(x, y, im.At(y, newW-1-x))
			}
		}
	case 180, -180:
		newW, newH := im.Bounds().Dx(), im.Bounds().Dy()
		rotated = image.NewNRGBA(image.Rect(0, 0, newW, newH))
		for y := 0; y < newH; y++ {
			for x := 0; x < newW; x++ {
				rotated.Set(x, y, im.At(newW-1-x, newH-1-y))
			}
		}
	default:
		return im
	}
	return rotated
}

// flip returns a flipped version of the image im, according to
// the direction(s) in dir.
// It may flip the imput im in place and return it, or it may allocate a
// new NRGBA (if im is an *image.YCbCr).
func flip(im image.Image, dir FlipDirection) image.Image {
	if dir == 0 {
		return im
	}
	ycbcr := false
	var nrgba image.Image
	dx, dy := im.Bounds().Dx(), im.Bounds().Dy()
	di, ok := im.(draw.Image)
	if !ok {
		if _, ok := im.(*image.YCbCr); !ok {
			log.Printf("failed to flip image: input does not satisfy draw.Image")
			return im
		}
		// because YCbCr does not implement Set, we replace it with a new NRGBA
		ycbcr = true
		nrgba = image.NewNRGBA(image.Rect(0, 0, dx, dy))
		di, ok = nrgba.(draw.Image)
		if !ok {
			log.Print("failed to flip image: could not cast an NRGBA to a draw.Image")
			return im
		}
	}
	if dir&FlipHorizontal != 0 {
		for y := 0; y < dy; y++ {
			for x := 0; x < dx/2; x++ {
				old := im.At(x, y)
				di.Set(x, y, im.At(dx-1-x, y))
				di.Set(dx-1-x, y, old)
			}
		}
	}
	if dir&FlipVertical != 0 {
		for y := 0; y < dy/2; y++ {
			for x := 0; x < dx; x++ {
				old := im.At(x, y)
				di.Set(x, y, im.At(x, dy-1-y))
				di.Set(x, dy-1-y, old)
			}
		}
	}
	if ycbcr {
		return nrgba
	}
	return im
}

func (opts *DecodeOpts) forcedRotate() bool {
	return opts != nil && opts.Rotate != nil
}

func (opts *DecodeOpts) forcedFlip() bool {
	return opts != nil && opts.Flip != nil
}

func (opts *DecodeOpts) useEXIF() bool {
	return !(opts.forcedRotate() || opts.forcedFlip())
}

// Decode decodes an image from r using the provided decoding options.
// The string returned is the format name returned by image.Decode.
// If opts is nil, the defaults are used.
func Decode(r io.Reader, opts *DecodeOpts) (image.Image, string, error) {
	var buf bytes.Buffer
	tr := io.TeeReader(io.LimitReader(r, 2<<20), &buf)
	angle := 0
	flipMode := FlipDirection(0)
	if opts.useEXIF() {
		ex, err := exif.Decode(tr)
		if err != nil {
			// TODO(mpl): analyse error. for now assume there's just no exif info.
			// will do tomorrow.
			return image.Decode(io.MultiReader(&buf, r))
		}
		tag, err := ex.Get(exif.Orientation)
		if err != nil {
			return nil, "", err
		}
		orient := tag.Val[1]
		switch orient {
		case 1:
			// do nothing
		case 2:
			flipMode = 2
		case 3:
			angle = 180
		case 4:
			angle = 180
			flipMode = 2
		case 5:
			angle = -90
			flipMode = 2
		case 6:
			angle = -90
		case 7:
			angle = 90
			flipMode = 2
		case 8:
			angle = 90
		}
	} else {
		if opts.forcedRotate() {
			var ok bool
			angle, ok = opts.Rotate.(int)
			if !ok {
				return nil, "", fmt.Errorf("Rotate should be an int, not a %T", opts.Rotate)
			}
		}
		if opts.forcedFlip() {
			var ok bool
			flipMode, ok = opts.Flip.(FlipDirection)
			if !ok {
				return nil, "", fmt.Errorf("Flip should be a FlipDirection, not a %T", opts.Flip)
			}
		}
	}

	im, err := jpeg.Decode(io.MultiReader(&buf, r))
	if err != nil {
		return nil, "", err
	}
	return flip(rotate(im, angle), flipMode), "jpeg", nil
}
