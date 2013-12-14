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
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/images"
	"camlistore.org/pkg/magic"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/syncutil"

	_ "camlistore.org/third_party/github.com/nf/cr2"
)

const imageDebug = false

type ImageHandler struct {
	Fetcher             blob.StreamingFetcher
	Cache               blobserver.Storage // optional
	MaxWidth, MaxHeight int
	Square              bool
	thumbMeta           *thumbMeta // optional cache for scaled images
}

func (ih *ImageHandler) storageSeekFetcher() blob.SeekFetcher {
	return blob.SeekerFromStreamingFetcher(ih.Fetcher) // TODO: pass ih.Cache?
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

func (ih *ImageHandler) cache(tr io.Reader, name string) (blob.Ref, error) {
	br, err := schema.WriteFileFromReader(ih.Cache, name, tr)
	if err != nil {
		return br, errors.New("failed to cache " + name + ": " + err.Error())
	}
	if imageDebug {
		log.Printf("Image Cache: saved as %v\n", br)
	}
	return br, nil
}

// cacheScaled saves in the image handler's cache the scaled image read
// from tr, and puts its blobref in the scaledImage under the key name.
func (ih *ImageHandler) cacheScaled(tr io.Reader, name string) error {
	br, err := ih.cache(tr, name)
	if err != nil {
		return err
	}
	ih.thumbMeta.Put(name, br)
	return nil
}

// cached returns a FileReader for the given file schema blobref.
// The FileReader should be closed when done reading.
func (ih *ImageHandler) cached(fileRef blob.Ref) (*schema.FileReader, error) {
	fetchSeeker := blob.SeekerFromStreamingFetcher(ih.Cache)
	fr, err := schema.NewFileReader(fetchSeeker, fileRef)
	if err != nil {
		return nil, err
	}
	if imageDebug {
		log.Printf("Image Cache: hit: %v\n", fileRef)
	}
	return fr, nil
}

// Key format: "scaled:" + bref + ":" + width "x" + height
// where bref is the blobref of the unscaled image.
func cacheKey(bref string, width int, height int) string {
	return fmt.Sprintf("scaled:%v:%dx%d", bref, width, height)
}

// ScaledCached reads the scaled version of the image in file,
// if it is in cache and writes it to buf.
//
// On successful read and population of buf, the returned format is non-empty.
// Almost all errors are not interesting. Real errors will be logged.
func (ih *ImageHandler) scaledCached(buf *bytes.Buffer, file blob.Ref) (format string) {
	key := cacheKey(file.String(), ih.MaxWidth, ih.MaxHeight)
	br, err := ih.thumbMeta.Get(key)
	if err == errCacheMiss {
		return
	}
	if err != nil {
		log.Printf("Warning: thumbnail cachekey(%q)->meta lookup error: %v", key, err)
		return
	}
	fr, err := ih.cached(br)
	if err != nil {
		return
	}
	defer fr.Close()
	_, err = io.Copy(buf, fr)
	if err != nil {
		return
	}
	mime := magic.MIMEType(buf.Bytes())
	if format = strings.TrimPrefix(mime, "image/"); format == mime {
		log.Printf("Warning: unescaped MIME type %q of %v file for thumbnail %q", mime, br, key)
		return
	}
	return format
}

// These gates control the max concurrency of slurping raw images
// (e.g. JPEG bytes) to RAM, and then decoding and resizing them,
// respectively.  We allow more concurrency for the former because
// it's slower and less memory-intensive.  The actual resizing takes
// much more CPU and RAM.

// TODO: these numbers were just guesses and not based on any
// data. measure? make these configurable? Automatically tuned
// somehow? Based on memory usage/availability?
var (
	scaleImageGateSlurp  = syncutil.NewGate(5)
	scaleImageGateResize = syncutil.NewGate(2)
)

func (ih *ImageHandler) scaleImage(buf *bytes.Buffer, file blob.Ref) (format string, err error) {
	fr, err := schema.NewFileReader(ih.storageSeekFetcher(), file)
	if err != nil {
		return format, err
	}
	defer fr.Close()

	scaleImageGateSlurp.Start()
	_, err = io.Copy(buf, fr)
	scaleImageGateSlurp.Done()
	if err != nil {
		return format, fmt.Errorf("image resize: error reading image %s: %v", file, err)
	}

	scaleImageGateResize.Start()
	defer scaleImageGateResize.Done()

	i, imConfig, err := images.Decode(bytes.NewReader(buf.Bytes()),
		&images.DecodeOpts{MaxWidth: ih.MaxWidth, MaxHeight: ih.MaxHeight})
	if err != nil {
		return format, err
	}
	b := i.Bounds()
	format = imConfig.Format

	useBytesUnchanged := !imConfig.Modified &&
		format != "cr2" // always recompress CR2 files

	isSquare := b.Dx() == b.Dy()
	if ih.Square && !isSquare {
		useBytesUnchanged = false
		i = squareImage(i)
		b = i.Bounds()
	}

	if !useBytesUnchanged {
		// Encode as a new image
		buf.Reset()
		switch format {
		case "png":
			err = png.Encode(buf, i)
		case "cr":
			// Recompress CR2 files as JPEG
			format = "jpeg"
			fallthrough
		default:
			err = jpeg.Encode(buf, i, nil)
		}
		if err != nil {
			return format, err
		}
	}
	return format, nil
}

func (ih *ImageHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request, file blob.Ref) {
	if !httputil.IsGet(req) {
		http.Error(rw, "Invalid method", 400)
		return
	}
	mw, mh := ih.MaxWidth, ih.MaxHeight
	if mw == 0 || mh == 0 || mw > search.MaxImageSize || mh > search.MaxImageSize {
		http.Error(rw, "bogus dimensions", 400)
		return
	}
	if req.Header.Get("If-Modified-Since") != "" {
		// Immutable, so any copy's a good copy.
		rw.WriteHeader(http.StatusNotModified)
		return
	}

	var buf bytes.Buffer
	var err error
	format := ""
	cacheHit := false
	if ih.thumbMeta != nil {
		format = ih.scaledCached(&buf, file)
		if format != "" {
			cacheHit = true
		}
	}

	if !cacheHit {
		format, err = ih.scaleImage(&buf, file)
		if err != nil {
			http.Error(rw, err.Error(), 500)
			return
		}
		if ih.thumbMeta != nil {
			name := cacheKey(file.String(), mw, mh)
			bufcopy := buf.Bytes()
			err = ih.cacheScaled(bytes.NewBuffer(bufcopy), name)
			if err != nil {
				log.Printf("image resize: %v", err)
			}
		}
	}

	h := rw.Header()
	h.Set("Expires", time.Now().Add(oneYear).Format(http.TimeFormat))
	h.Set("Last-Modified", time.Now().Format(http.TimeFormat))
	h.Set("Content-Type", imageContentTypeOfFormat(format))
	size := buf.Len()
	h.Set("Content-Length", fmt.Sprintf("%d", size))

	if req.Method == "GET" {
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
}

func imageContentTypeOfFormat(format string) string {
	if format == "jpeg" {
		return "image/jpeg"
	}
	return "image/png"
}
