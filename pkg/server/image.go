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
	"expvar"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/images"
	"camlistore.org/pkg/magic"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/singleflight"
	"camlistore.org/pkg/syncutil"

	_ "camlistore.org/third_party/github.com/nf/cr2"
)

const imageDebug = false

var (
	imageBytesServedVar  = expvar.NewInt("image-bytes-served")
	imageBytesFetchedVar = expvar.NewInt("image-bytes-fetched")
)

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

func (ih *ImageHandler) writeToCache(tr io.Reader, name string) (blob.Ref, error) {
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
	br, err := ih.writeToCache(tr, name)
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
	return fmt.Sprintf("scaled:%v:%dx%d:tv%d", bref, width, height, images.ThumbnailVersion())
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

// Gate the number of concurrent image resizes to limit RAM & CPU use.

// This is the maximum concurrent number of bytes we allocate for uncompressed
// pixel data while generating thumbnails.
const maxResizeBytes = 256 << 20

var resizeSem = syncutil.NewSem(maxResizeBytes)

type formatAndImage struct {
	format string
	image  []byte
}

// TODO(wathiede): move to a common location if the pattern of TeeReader'ing
// to a statWriter proves useful.
type statWriter struct {
	*expvar.Int
}

func (sw statWriter) Write(p []byte) (int, error) {
	c := len(p)
	sw.Add(int64(c))
	return c, nil
}

// imageConfigFromReader calls image.DecodeConfig on r. It returns an
// io.Reader that is the concatentation of the bytes read and the remaining r,
// the image configuration, and the error from image.DecodeConfig.
func imageConfigFromReader(r io.Reader) (io.Reader, image.Config, error) {
	header := new(bytes.Buffer)
	tr := io.TeeReader(r, header)
	// We just need width & height for memory considerations, so we use the
	// standard library's DecodeConfig, skipping the EXIF parsing and
	// orientation correction for images.DecodeConfig.
	conf, _, err := image.DecodeConfig(tr)
	return io.MultiReader(header, r), conf, err
}

func (ih *ImageHandler) scaleImage(fileRef blob.Ref) (*formatAndImage, error) {
	fr, err := schema.NewFileReader(ih.storageSeekFetcher(), fileRef)
	if err != nil {
		return nil, err
	}
	defer fr.Close()

	sw := statWriter{imageBytesFetchedVar}
	tr := io.TeeReader(fr, sw)

	if err != nil {
		return nil, fmt.Errorf("image resize: error reading image %s: %v", fileRef, err)
	}

	tr, conf, err := imageConfigFromReader(tr)
	if err != nil {
		return nil, err
	}

	// TODO(wathiede): build a size table keyed by conf.ColorModel for
	// common color models for a more exact size estimate.

	// This value is an estimate of the memory required to decode an image,
	// for YCbCr images, i.e. JPEGs, it will often be higher, for RGBA PNGs
	// it is low.
	ramSize := int64(conf.Width) * int64(conf.Height) * 3

	// If a single image is larger than maxResizeBytes can hold, we'll never
	// successfully resize it.
	// TODO(wathiede): do we need a more graceful fallback? 256M is a max
	// image of ~9.5kx9.5k*3.
	if err = resizeSem.Acquire(ramSize); err != nil {
		return nil, err
	}
	defer resizeSem.Release(ramSize)

	i, imConfig, err := images.Decode(tr, &images.DecodeOpts{
		MaxWidth:  ih.MaxWidth,
		MaxHeight: ih.MaxHeight,
	})
	if err != nil {
		return nil, err
	}
	b := i.Bounds()
	format := imConfig.Format

	useBytesUnchanged := !imConfig.Modified &&
		format != "cr2" // always recompress CR2 files

	isSquare := b.Dx() == b.Dy()
	if ih.Square && !isSquare {
		useBytesUnchanged = false
		i = squareImage(i)
		b = i.Bounds()
	}

	var buf bytes.Buffer
	if !useBytesUnchanged {
		// Encode as a new image
		switch format {
		case "png":
			err = png.Encode(&buf, i)
		case "cr2":
			// Recompress CR2 files as JPEG
			format = "jpeg"
			fallthrough
		default:
			err = jpeg.Encode(&buf, i, &jpeg.Options{
				Quality: 90,
			})
		}
		if err != nil {
			return nil, err
		}
	}
	return &formatAndImage{format: format, image: buf.Bytes()}, nil
}

// singleResize prevents generating the same thumbnail at once from
// two different requests.  (e.g. sending out a link to a new photo
// gallery to a big audience)
var singleResize singleflight.Group

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

	key := cacheKey(file.String(), mw, mh)
	etag := blob.SHA1FromString(key).String()[5:]
	inm := req.Header.Get("If-None-Match")
	if inm != "" {
		if strings.Trim(inm, `"`) == etag {
			rw.WriteHeader(http.StatusNotModified)
			return
		}
	} else {
		if !disableThumbCache && req.Header.Get("If-Modified-Since") != "" {
			rw.WriteHeader(http.StatusNotModified)
			return
		}
	}

	var imageData []byte
	format := ""
	cacheHit := false
	if ih.thumbMeta != nil && !disableThumbCache {
		var buf bytes.Buffer
		format = ih.scaledCached(&buf, file)
		if format != "" {
			cacheHit = true
			imageData = buf.Bytes()
		}
	}

	if !cacheHit {
		imi, err := singleResize.Do(key, func() (interface{}, error) {
			return ih.scaleImage(file)
		})
		if err != nil {
			http.Error(rw, err.Error(), 500)
			return
		}
		im := imi.(*formatAndImage)
		imageData = im.image
		format = im.format
		if ih.thumbMeta != nil {
			err := ih.cacheScaled(bytes.NewReader(imageData), key)
			if err != nil {
				log.Printf("image resize: %v", err)
			}
		}
	}

	h := rw.Header()
	if !disableThumbCache {
		h.Set("Expires", time.Now().Add(oneYear).Format(http.TimeFormat))
		h.Set("Last-Modified", time.Now().Format(http.TimeFormat))
		h.Set("Etag", strconv.Quote(etag))
	}
	h.Set("Content-Type", imageContentTypeOfFormat(format))
	size := len(imageData)
	h.Set("Content-Length", fmt.Sprint(size))
	imageBytesServedVar.Add(int64(size))

	if req.Method == "GET" {
		n, err := rw.Write(imageData)
		if err != nil {
			if strings.Contains(err.Error(), "broken pipe") {
				// boring.
				return
			}
			// TODO: vlog this:
			log.Printf("error serving thumbnail of file schema %s: %v", file, err)
			return
		}
		if n != size {
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
