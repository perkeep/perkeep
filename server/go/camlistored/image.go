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

package main

import (
	"bytes"
	"fmt"
	"http"
	"image"
	"image/jpeg"
	"image/png"
	"log"
	"io"
	"os"

	"camli/blobref"
	"camli/blobserver"
	"camli/misc/resize"
	"camli/schema"
)

type ImageHandler struct {
	Fetcher             blobref.StreamingFetcher
	Cache               blobserver.Storage // optional
	MaxWidth, MaxHeight int
	Square              bool
}

func (ih *ImageHandler) storageSeekFetcher() (blobref.SeekFetcher, os.Error) {
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

	fetchSeeker, err := ih.storageSeekFetcher()
	if err != nil {
		http.Error(rw, err.String(), 500)
		return
	}

	fr, err := schema.NewFileReader(fetchSeeker, file)
	if err != nil {
		http.Error(rw, "Can't serve file: "+err.String(), 500)
		return
	}

	var buf bytes.Buffer
	n, err := io.Copy(&buf, fr)
	if err != nil {
		log.Printf("image resize: error reading image %s: %v", file, err)
		return
	}
	i, format, err := image.Decode(bytes.NewBuffer(buf.Bytes()))
	if err != nil {
		http.Error(rw, "Can't serve file: "+err.String(), 500)
		return
	}
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
			err = jpeg.Encode(&buf, i, nil)
		default:
			err = png.Encode(&buf, i)
		}
		if err != nil {
			http.Error(rw, "Can't serve file: "+err.String(), 500)
			return
		}
	}

	rw.Header().Set("Content-Type", imageContentTypeOfFormat(format))
	size := buf.Len()
	rw.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	n, err = io.Copy(rw, &buf)
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
