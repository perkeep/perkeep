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
	"strconv"

	_ "image/gif"
	_ "image/png"

	"camlistore.org/pkg/images/resize"
	"camlistore.org/third_party/github.com/camlistore/goexif/exif"
)

// Exif Orientation Tag values
// http://sylvana.net/jpegcrop/exif_orientation.html
const (
	topLeftSide     = 1
	topRightSide    = 2
	bottomRightSide = 3
	bottomLeftSide  = 4
	leftSideTop     = 5
	rightSideTop    = 6
	rightSideBottom = 7
	leftSideBottom  = 8
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

// ScaledDimensions returns the newWidth and newHeight obtained
// when an image of dimensions w x h has to be rescaled under
// mw x mh, while conserving the proportions.
// It returns 1,1 if any of the parameter is 0.
func ScaledDimensions(w, h, mw, mh int) (newWidth int, newHeight int) {
	if w == 0 || h == 0 || mw == 0 || mh == 0 {
		imageDebug("ScaledDimensions was given as 0; returning 1x1 as dimensions.")
		return 1, 1
	}
	newWidth, newHeight = mw, mh
	if float32(h)/float32(mh) > float32(w)/float32(mw) {
		newWidth = w * mh / h
	} else {
		newHeight = h * mw / w
	}
	return
}

func rescale(im image.Image, opts *DecodeOpts, swapDimensions bool) image.Image {
	mw, mh := opts.MaxWidth, opts.MaxHeight
	mwf, mhf := opts.ScaleWidth, opts.ScaleHeight
	b := im.Bounds()
	// only do downscaling, otherwise just serve the original image
	if !opts.wantRescale(b, swapDimensions) {
		return im
	}

	if swapDimensions {
		mw, mh = mh, mw
	}

	// ScaleWidth and ScaleHeight overrule MaxWidth and MaxHeight
	if mwf > 0.0 && mwf <= 1 {
		mw = int(mwf * float32(b.Dx()))
	}
	if mhf > 0.0 && mhf <= 1 {
		mh = int(mhf * float32(b.Dy()))
	}
	// If it's gigantic, it's more efficient to downsample first
	// and then resize; resizing will smooth out the roughness.
	// (trusting the moustachio guys on that one).
	if b.Dx() > mw*2 || b.Dy() > mh*2 {
		w, h := ScaledDimensions(b.Dx(), b.Dy(), mw*2, mh*2)
		im = resize.ResampleInplace(im, b, w, h)
		return resize.HalveInplace(im)
	}
	mw, mh = ScaledDimensions(b.Dx(), b.Dy(), mw, mh)
	return resize.Resize(im, b, mw, mh)
}

func (opts *DecodeOpts) wantRescale(b image.Rectangle, swapDimensions bool) bool {
	if opts == nil {
		return false
	}

	// In rescale Scale* trumps Max* so we assume the same relationship here.

	// Floating point compares probably only allow this to work if the values
	// were specified as the literal 1 or 1.0, computed values will likely be
	// off.  If Scale{Width,Height} end up being 1.0-epsilon we'll rescale
	// when it probably wouldn't even be noticible but that's okay.
	if opts.ScaleWidth == 1.0 && opts.ScaleHeight == 1.0 {
		return false
	}
	if opts.ScaleWidth > 0 && opts.ScaleWidth < 1.0 ||
		opts.ScaleHeight > 0 && opts.ScaleHeight < 1.0 {
		return true
	}

	w, h := b.Dx(), b.Dy()
	if swapDimensions {
		w, h = h, w
	}

	// Same size, don't rescale.
	if opts.MaxWidth == w && opts.MaxHeight == h {
		return false
	}
	return opts.MaxWidth > 0 && opts.MaxWidth < w ||
		opts.MaxHeight > 0 && opts.MaxHeight < h
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

var debug, _ = strconv.ParseBool(os.Getenv("CAMLI_DEBUG_IMAGES"))

func imageDebug(msg string) {
	if debug {
		log.Print(msg)
	}
}

// DecodeConfig returns the image Config similarly to
// the standard library's image.DecodeConfig with the
// addition that it also checks for an EXIF orientation,
// and sets the Width and Height as they would visibly
// be after correcting for that orientation.
func DecodeConfig(r io.Reader) (Config, error) {
	var c Config
	var buf bytes.Buffer
	tr := io.TeeReader(io.LimitReader(r, 2<<20), &buf)
	swapDimensions := false

	ex, err := exif.Decode(tr)
	if err != nil {
		imageDebug("No valid EXIF.")
	} else {
		tag, err := ex.Get(exif.Orientation)
		if err != nil {
			imageDebug("No \"Orientation\" tag in EXIF.")
		} else {
			orient := tag.Int(0)
			switch orient {
			// those are the orientations that require
			// a rotation of ±90
			case leftSideTop, rightSideTop, rightSideBottom, leftSideBottom:
				swapDimensions = true
			}
		}
	}
	conf, format, err := image.DecodeConfig(io.MultiReader(&buf, r))
	if err != nil {
		imageDebug(fmt.Sprintf("Image Decoding failed: %v", err))
		return c, err
	}
	c.Format = format
	if swapDimensions {
		c.Width, c.Height = conf.Height, conf.Width
	} else {
		c.Width, c.Height = conf.Width, conf.Height
	}
	return c, err
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
			if err == nil && opts.wantRescale(im.Bounds(), false) {
				im = rescale(im, opts, false)
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
		case topLeftSide:
			// do nothing
		case topRightSide:
			flipMode = 2
		case bottomRightSide:
			angle = 180
		case bottomLeftSide:
			angle = 180
			flipMode = 2
		case leftSideTop:
			angle = -90
			flipMode = 2
		case rightSideTop:
			angle = -90
		case rightSideBottom:
			angle = 90
			flipMode = 2
		case leftSideBottom:
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
	// Orientation changing rotations should have their dimensions swapped
	// when scaling.
	var swapDimensions bool
	switch angle {
	case 90, -90:
		swapDimensions = true
	}
	if opts.wantRescale(im.Bounds(), swapDimensions) {
		im = rescale(im, opts, swapDimensions)
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
