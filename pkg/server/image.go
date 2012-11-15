/*
Copyright 2011 Google Inc.

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

package server

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"strings"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/images"
	"camlistore.org/pkg/magic"
	"camlistore.org/pkg/misc/resize"
	"camlistore.org/pkg/schema"

	_ "image/gif"
)

type ImageHandler struct {
	Fetcher             blobref.StreamingFetcher
	Cache               blobserver.Storage // optional
	MaxWidth, MaxHeight int
	Square              bool
	sc                  ScaledImage // optional cache for scaled images
}

func (ih *ImageHandler) storageSeekFetcher() (blobref.SeekFetcher, error) {
	return blobref.SeekerFromStreamingFetcher(ih.Fetcher) // TODO: pass ih.Cache?
}

type subImager interface {
	SubImage(image.Rectangle) image.Image
}

func squareImage(i image.Image) image.Image {
	si, ok := i.(subImager)
	if !ok {
		log.Fatalf("image %T isn't a subImager", i)
	}
	b := i.Bounds()
	if b.Dx() > b.Dy() {
		thin := (b.Dx() - b.Dy()) / 2
		newB := b
		newB.Min.X += thin
		newB.Max.X -= thin
		return si.SubImage(newB)
	}
	thin := (b.Dy() - b.Dx()) / 2
	newB := b
	newB.Min.Y += thin
	newB.Max.Y -= thin
	return si.SubImage(newB)
}

func (ih *ImageHandler) cache(tr io.Reader, name string) (*blobref.BlobRef, error) {
	br, err := schema.WriteFileFromReader(ih.Cache, name, tr)
	if err != nil {
		return br, errors.New("failed to cache " + name + ": " + err.Error())
	}
	log.Printf("Image Cache: saved as %v\n", br)
	return br, nil
}

// CacheScaled saves in the image handler's cache the scaled image read 
// from tr, and puts its blobref in the scaledImage under the key name.
func (ih *ImageHandler) cacheScaled(tr io.Reader, name string) error {
	br, err := ih.cache(tr, name)
	if err != nil {
		return err
	}
	ih.sc.Put(name, br)
	return nil
}

func (ih *ImageHandler) cached(br *blobref.BlobRef) (fr *schema.FileReader, err error) {
	fetchSeeker, err := blobref.SeekerFromStreamingFetcher(ih.Cache)
	if err != nil {
		return nil, err
	}

	fr, err = schema.NewFileReader(fetchSeeker, br)
	if err != nil {
		return nil, err
	}
	log.Printf("Image Cache: hit: %v\n", br)
	return fr, nil
}

// Key format: "scaled:" + bref + ":" + width "x" + height
// where bref is the blobref of the unscaled image.
func cacheKey(bref string, width int, height int) string {
	return fmt.Sprintf("scaled:%v:%dx%d", bref, width, height)
}

// ScaledCached reads the scaled version of the image in file,
// if it is in cache. On success, the image format is returned.
func (ih *ImageHandler) scaledCached(buf *bytes.Buffer, file *blobref.BlobRef) (format string, err error) {
	name := cacheKey(file.String(), ih.MaxWidth, ih.MaxHeight)
	br, err := ih.sc.Get(name)
	if err != nil {
		return format, fmt.Errorf("%v: %v", name, err)
	}
	fr, err := ih.cached(br)
	if err != nil {
		return format, fmt.Errorf("No cache hit for %v: %v", br, err)
	}
	_, err = io.Copy(buf, fr)
	if err != nil {
		return format, fmt.Errorf("error reading cached thumbnail %v: %v", name, err)
	}
	mime := magic.MimeType(buf.Bytes())
	if mime == "" {
		return format, fmt.Errorf("error with cached thumbnail %v: unknown mime type", name)
	}
	pieces := strings.Split(mime, "/")
	if len(pieces) < 2 {
		return format, fmt.Errorf("error with cached thumbnail %v: bogus mime type", name)
	}
	if pieces[0] != "image" {
		return format, fmt.Errorf("error with cached thumbnail %v: not an image", name)
	}
	return pieces[1], nil
}

func (ih *ImageHandler) scaleImage(buf *bytes.Buffer, file *blobref.BlobRef) (format string, err error) {
	mw, mh := ih.MaxWidth, ih.MaxHeight

	fetchSeeker, err := ih.storageSeekFetcher()
	if err != nil {
		return format, err
	}

	fr, err := schema.NewFileReader(fetchSeeker, file)
	if err != nil {
		return format, err
	}

	_, err = io.Copy(buf, fr)
	if err != nil {
		return format, fmt.Errorf("image resize: error reading image %s: %v", file, err)
	}
	i, format, err := images.Decode(bytes.NewBuffer(buf.Bytes()), nil)
	if err != nil {
		return format, err
	}
	// TODO(mpl): maybe detect if it was rotated and if we need to force repushing
	// the bytes to buf? If not, it means images that are already smaller than the
	// requested thumbnail size will not be returned corrected.
	b := i.Bounds()

	useBytesUnchanged := true

	isSquare := b.Dx() == b.Dy()
	if ih.Square && !isSquare {
		useBytesUnchanged = false
		i = squareImage(i)
		b = i.Bounds()
	}

	// only do downscaling, otherwise just serve the original image
	if mw < b.Dx() || mh < b.Dy() {
		useBytesUnchanged = false

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
			i = resize.Resample(i, i.Bounds(), w, h)
			b = i.Bounds()
		}
		// conserve proportions. use the smallest of the two as the decisive one.
		if mw > mh {
			mw = b.Dx() * mh / b.Dy()
		} else {
			mh = b.Dy() * mw / b.Dx()
		}
	}

	if !useBytesUnchanged {
		i = resize.Resize(i, b, mw, mh)
		// Encode as a new image
		buf.Reset()
		switch format {
		case "jpeg":
			err = jpeg.Encode(buf, i, nil)
		default:
			err = png.Encode(buf, i)
		}
		if err != nil {
			return format, err
		}
	}
	return format, nil
}

func (ih *ImageHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request, file *blobref.BlobRef) {
	if req.Method != "GET" && req.Method != "HEAD" {
		http.Error(rw, "Invalid method", 400)
		return
	}
	mw, mh := ih.MaxWidth, ih.MaxHeight
	if mw == 0 || mh == 0 || mw > 2000 || mh > 2000 {
		http.Error(rw, "bogus dimensions", 400)
		return
	}

	var buf bytes.Buffer
	var err error
	format := ""
	cacheHit := false
	if ih.sc != nil {
		format, err = ih.scaledCached(&buf, file)
		if err != nil {
			log.Printf("image resize: %v", err)
		} else {
			cacheHit = true
		}
	}

	if !cacheHit {
		format, err = ih.scaleImage(&buf, file)
		if err != nil {
			http.Error(rw, err.Error(), 500)
			return
		}
		if ih.sc != nil {
			name := cacheKey(file.String(), mw, mh)
			bufcopy := buf.Bytes()
			err = ih.cacheScaled(bytes.NewBuffer(bufcopy), name)
			if err != nil {
				log.Printf("image resize: %v", err)
			}
		}
	}

	rw.Header().Set("Content-Type", imageContentTypeOfFormat(format))
	size := buf.Len()
	rw.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	n, err := io.Copy(rw, &buf)
	if err != nil {
		log.Printf("error serving thumbnail of file schema %s: %v", file, err)
		return
	}
	if n != int64(size) {
		log.Printf("error serving thumbnail of file schema %s: sent %d, expected size of %d",
			file, n, size)
		return
	}
}

func imageContentTypeOfFormat(format string) string {
	if format == "jpeg" {
		return "image/jpeg"
	}
	return "image/png"
}
