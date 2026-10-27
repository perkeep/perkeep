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

package images

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"

	"github.com/gen2brain/heic"
	"go4.org/media/heif"
)

// ErrNoUsableThumbnail is returned by HEIC.Thumbnail when a HEIC file
// has no embedded thumbnail that can stand in for its primary image at
// the requested size.
var ErrNoUsableThumbnail = errors.New("images: no usable HEIC thumbnail")

// maxThumbnailOversize is how much larger than the requested size, in
// each dimension, an embedded thumbnail is returned without rescaling.
// iPhones embed 416x312 thumbnails, which then serve the UI's 256 pixel
// tall requests directly.
const maxThumbnailOversize = 1.5

// HEIC is a HEIC file's metadata, as read by OpenHEIC.
type HEIC struct {
	// Width and Height are the primary image's visual dimensions,
	// after any rotation.
	Width, Height int

	f       *heif.File
	primary *heif.Item
}

// OpenHEIC reads the metadata of the HEIC file ra. Unlike decoding the
// image or its config with the heic package, which has libheif parse up
// to 256 KB of the file, it only reads the metadata boxes.
func OpenHEIC(ra io.ReaderAt) (*HEIC, error) {
	f := heif.Open(ra)
	primary, err := f.PrimaryItem()
	if err != nil {
		return nil, err
	}
	w, h, ok := primary.VisualDimensions()
	if !ok || w <= 0 || h <= 0 {
		return nil, errors.New("images: HEIC primary image lacks dimensions")
	}
	return &HEIC{Width: w, Height: h, f: f, primary: primary}, nil
}

// HEICThumbnail is an embedded thumbnail of a HEIC file's primary image,
// as found by HEIC.Thumbnail.
type HEICThumbnail struct {
	// Width and Height are the thumbnail's visual dimensions, after any
	// rotation, before any rescaling by Decode.
	Width, Height int

	f    *heif.File
	item *heif.Item
	opts DecodeOpts
}

// Thumbnail returns the smallest thumbnail embedded in the HEIC file
// that covers the size requested by opts.MaxWidth and opts.MaxHeight and
// has its primary image's aspect ratio.
// It returns ErrNoUsableThumbnail if there is no such thumbnail or if
// opts asks for anything other than a downscaled, automatically
// oriented image. Decoding the thumbnail is much cheaper than decoding
// the primary image, which is commonly 12 to 48 megapixels.
func (h *HEIC) Thumbnail(opts *DecodeOpts) (*HEICThumbnail, error) {
	if opts == nil || opts.MaxWidth <= 0 || opts.MaxHeight <= 0 ||
		opts.ScaleWidth != 0 || opts.ScaleHeight != 0 || !opts.useEXIF() {
		return nil, ErrNoUsableThumbnail
	}
	pw, ph := h.Width, h.Height
	tw, th := ScaledDimensions(pw, ph, opts.MaxWidth, opts.MaxHeight)
	if tw >= pw && th >= ph {
		// Not downscaling, so the primary image is needed.
		return nil, ErrNoUsableThumbnail
	}
	f := h.f
	thumbs, err := f.Thumbnails(h.primary)
	if err != nil {
		return nil, err
	}
	var best *HEICThumbnail
	for _, it := range thumbs {
		switch it.Info.ItemType {
		case "hvc1":
		case "jpeg":
			if hasProperty(it, "imir") {
				// decodeJPEGItem doesn't mirror.
				continue
			}
		default:
			continue
		}
		w, h, ok := it.VisualDimensions()
		// Allow a pixel of rounding in the requested dimensions.
		if !ok || w < tw-1 || h < th-1 || !sameAspect(w, h, pw, ph) {
			continue
		}
		if best == nil || w*h < best.Width*best.Height {
			best = &HEICThumbnail{Width: w, Height: h, f: f, item: it, opts: *opts}
		}
	}
	if best == nil {
		return nil, ErrNoUsableThumbnail
	}
	return best, nil
}

// sameAspect reports whether w x h and pw x ph have aspect ratios
// within 1% of each other, so that a thumbnail isn't letterboxed or
// cropped relative to its primary image.
func sameAspect(w, h, pw, ph int) bool {
	a, b := int64(w)*int64(ph), int64(h)*int64(pw)
	d := a - b
	if d < 0 {
		d = -d
	}
	return d*100 <= b
}

func hasProperty(it *heif.Item, typ string) bool {
	for _, p := range it.Properties {
		if p.Type().EqualString(typ) {
			return true
		}
	}
	return false
}

// Decode decodes the thumbnail, rotated per its HEIF properties.
// Thumbnails more than 1.5 times the requested size in either dimension
// are rescaled to fit it; smaller ones are returned as is, even if
// slightly larger than requested.
func (t *HEICThumbnail) Decode() (image.Image, Config, error) {
	var im image.Image
	switch t.item.Info.ItemType {
	case "hvc1":
		b, err := t.f.StandaloneHEIF(t.item)
		if err != nil {
			return nil, Config{}, err
		}
		// libheif applies the copied irot and imir properties.
		if im, err = heic.Decode(bytes.NewReader(b)); err != nil {
			return nil, Config{}, fmt.Errorf("decoding HEIC thumbnail: %w", err)
		}
	case "jpeg":
		var err error
		if im, err = decodeJPEGItem(t.item); err != nil {
			return nil, Config{}, err
		}
	default:
		return nil, Config{}, fmt.Errorf("unsupported HEIC thumbnail type %q", t.item.Info.ItemType)
	}
	if b := im.Bounds(); b.Dx() != t.Width || b.Dy() != t.Height {
		return nil, Config{}, fmt.Errorf("HEIC thumbnail decoded to %dx%d; want %dx%d", b.Dx(), b.Dy(), t.Width, t.Height)
	}
	tw, th := ScaledDimensions(t.Width, t.Height, t.opts.MaxWidth, t.opts.MaxHeight)
	if float64(t.Width) > maxThumbnailOversize*float64(tw) || float64(t.Height) > maxThumbnailOversize*float64(th) {
		im = rescale(im, tw, th)
	}
	c := Config{Format: "heic", Modified: true}
	c.setBounds(im)
	return im, c, nil
}

// decodeJPEGItem decodes a JPEG-coded HEIF item, applying its rotation.
func decodeJPEGItem(it *heif.Item) (image.Image, error) {
	data, err := it.Data()
	if err != nil {
		return nil, err
	}
	im, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decoding HEIC JPEG thumbnail: %w", err)
	}
	// Rotations counts quarter turns counter-clockwise, as does rotate.
	switch it.Rotations() {
	case 1:
		im = rotate(im, 90)
	case 2:
		im = rotate(im, 180)
	case 3:
		im = rotate(im, -90)
	}
	return im, nil
}
