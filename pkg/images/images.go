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
	"io"
	"log"
	"os"

	_ "image/gif"
	_ "image/png"

	"camlistore.org/pkg/misc/resize"
	"camlistore.org/third_party/github.com/camlistore/goexif/exif"
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
	// image's size. Rescaling is done before flipping or rotating.
	// Proportions are conserved, so the smallest of the two is used
	// as the decisive one if needed.
	MaxWidth, MaxHeight int

	// ScaleWidth and ScaleHeight optionally specify how to rescale the
	// image's dimensions. Rescaling is done before flipping or rotating.
	// Proportions are conserved, so the smallest of the two is used
	// as the decisive one if needed.
	// They overrule MaxWidth and MaxHeight.
	ScaleWidth, ScaleHeight float32

	// TODO: consider alternate options if scaled ratio doesn't
	// match original ratio:
	//   Crop    bool
	//   Stretch bool
}

// Config is like the standard library's image.Config as used by DecodeConfig.
type Config struct {
	Width, Height int
	Format        string
	Modified      bool // true if Decode actually rotated or flipped the image.
}

func (c *Config) setBounds(im image.Image) {
	if im != nil {
		c.Width = im.Bounds().Dx()
		c.Height = im.Bounds().Dy()
	}
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

func rescale(im image.Image, opts *DecodeOpts) image.Image {
	mw, mh := opts.MaxWidth, opts.MaxHeight
	mwf, mhf := opts.ScaleWidth, opts.ScaleHeight
	b := im.Bounds()
	// only do downscaling, otherwise just serve the original image
	if !opts.wantRescale(b) {
		return im
	}
	// ScaleWidth and ScaleHeight overrule MaxWidth and MaxHeight
	if mwf > 0.0 && mwf <= 1 {
		mw = int(mwf * float32(b.Dx()))
	}
	if mhf > 0.0 && mhf <= 1 {
		mh = int(mhf * float32(b.Dy()))
	}

	const huge = 2400
	// If it's gigantic, it's more efficient to downsample first
	// and then resize; resizing will smooth out the roughness.
	// (trusting the moustachio guys on that one).
	if b.Dx() > huge || b.Dy() > huge {
		w, h := mw*2, mh*2
		if b.Dx() > b.Dy() {
			w = b.Dx() * h / b.Dy()
		} else {
			h = b.Dy() * w / b.Dx()
		}
		im = resize.Resample(im, b, w, h)
		b = im.Bounds()
	}
	// conserve proportions. use the smallest of the two as the decisive one.
	if mw > mh {
		mw = b.Dx() * mh / b.Dy()
	} else {
		mh = b.Dy() * mw / b.Dx()
	}
	return resize.Resize(im, b, mw, mh)
}

func (opts *DecodeOpts) wantRescale(b image.Rectangle) bool {
	return opts != nil &&
		(opts.MaxWidth > 0 && opts.MaxWidth < b.Dx() ||
			opts.MaxHeight > 0 && opts.MaxHeight < b.Dy() ||
			opts.ScaleWidth > 0.0 && opts.ScaleWidth < float32(b.Dx()) ||
			opts.ScaleHeight > 0.0 && opts.ScaleHeight < float32(b.Dy()))
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

func imageDebug(msg string) {
	if os.Getenv("CAM_DEBUG_IMAGES") != "" {
		log.Print(msg)
	}
}

// Decode decodes an image from r using the provided decoding options.
// The Config returned is similar to the one from the image package,
// with the addition of the Modified field which indicates if the
// image was actually flipped or rotated.
// If opts is nil, the defaults are used.
func Decode(r io.Reader, opts *DecodeOpts) (image.Image, Config, error) {
	var c Config
	var buf bytes.Buffer
	tr := io.TeeReader(io.LimitReader(r, 2<<20), &buf)
	angle := 0
	flipMode := FlipDirection(0)
	if opts.useEXIF() {
		ex, err := exif.Decode(tr)
		maybeRescale := func() (image.Image, Config, error) {
			im, format, err := image.Decode(io.MultiReader(&buf, r))
			if err == nil && opts.wantRescale(im.Bounds()) {
				im = rescale(im, opts)
				c.Modified = true
			}
			c.Format = format
			c.setBounds(im)
			return im, c, err
		}
		if err != nil {
			imageDebug("No valid EXIF; will not rotate or flip.")
			return maybeRescale()
		}
		tag, err := ex.Get(exif.Orientation)
		if err != nil {
			imageDebug("No \"Orientation\" tag in EXIF; will not rotate or flip.")
			return maybeRescale()
		}
		orient := tag.Int(0)
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
				return nil, c, fmt.Errorf("Rotate should be an int, not a %T", opts.Rotate)
			}
		}
		if opts.forcedFlip() {
			var ok bool
			flipMode, ok = opts.Flip.(FlipDirection)
			if !ok {
				return nil, c, fmt.Errorf("Flip should be a FlipDirection, not a %T", opts.Flip)
			}
		}
	}

	im, format, err := image.Decode(io.MultiReader(&buf, r))
	if err != nil {
		return nil, c, err
	}
	rescaled := false
	if opts.wantRescale(im.Bounds()) {
		im = rescale(im, opts)
		rescaled = true
	}
	im = flip(rotate(im, angle), flipMode)
	modified := true
	if angle == 0 && flipMode == 0 && !rescaled {
		modified = false
	}

	c.Format = format
	c.Modified = modified
	c.setBounds(im)
	return im, c, nil
}
