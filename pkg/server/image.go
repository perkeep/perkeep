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
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/constants"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/images"
	"camlistore.org/pkg/magic"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/types"

	_ "github.com/nf/cr2"

	"go4.org/syncutil"
	"go4.org/syncutil/singleflight"
)

const imageDebug = false

var (
	imageBytesServedVar  = expvar.NewInt("image-bytes-served")
	imageBytesFetchedVar = expvar.NewInt("image-bytes-fetched")
	thumbCacheMiss       = expvar.NewInt("thumbcache-miss")
	thumbCacheHitFull    = expvar.NewInt("thumbcache-hit-full")
	thumbCacheHitFile    = expvar.NewInt("thumbcache-hit-file")
	thumbCacheHeader304  = expvar.NewInt("thumbcache-header-304")
)

type ImageHandler struct {
	Fetcher             blob.Fetcher
	Search              *search.Handler    // optional
	Cache               blobserver.Storage // optional
	MaxWidth, MaxHeight int
	Square              bool
	ThumbMeta           *ThumbMeta    // optional cache index for scaled images
	ResizeSem           *syncutil.Sem // Limit peak RAM used by concurrent image thumbnail calls.
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

func writeToCache(cache blobserver.Storage, thumbBytes []byte, name string) (br blob.Ref, err error) {
	tr := bytes.NewReader(thumbBytes)
	if len(thumbBytes) < constants.MaxBlobSize {
		br = blob.SHA1FromBytes(thumbBytes)
		_, err = blobserver.Receive(cache, br, tr)
	} else {
		// TODO: don't use rolling checksums when writing this. Tell
		// the filewriter to use 16 MB chunks instead.
		br, err = schema.WriteFileFromReader(cache, name, tr)
	}
	if err != nil {
		return br, errors.New("failed to cache " + name + ": " + err.Error())
	}
	if imageDebug {
		log.Printf("Image Cache: saved as %v\n", br)
	}
	return br, nil
}

// cacheScaled saves in the image handler's cache the scaled image bytes
// in thumbBytes, and puts its blobref in the scaledImage under the key name.
func (ih *ImageHandler) cacheScaled(thumbBytes []byte, name string) error {
	br, err := writeToCache(ih.Cache, thumbBytes, name)
	if err != nil {
		return err
	}
	ih.ThumbMeta.Put(name, br)
	return nil
}

// cached returns a FileReader for the given blobref, which may
// point to either a blob representing the entire thumbnail (max
// 16MB) or a file schema blob.
//
// The ReadCloser should be closed when done reading.
func (ih *ImageHandler) cached(br blob.Ref) (io.ReadCloser, error) {
	rsc, _, err := ih.Cache.Fetch(br)
	if err != nil {
		return nil, err
	}
	slurp, err := ioutil.ReadAll(rsc)
	rsc.Close()
	if err != nil {
		return nil, err
	}
	// In the common case, when the scaled image itself is less than 16 MB, it's
	// all together in one blob.
	if strings.HasPrefix(magic.MIMEType(slurp), "image/") {
		thumbCacheHitFull.Add(1)
		if imageDebug {
			log.Printf("Image Cache: hit: %v\n", br)
		}
		return ioutil.NopCloser(bytes.NewReader(slurp)), nil
	}

	// For large scaled images, the cached blob is a file schema blob referencing
	// the sub-chunks.
	fileBlob, err := schema.BlobFromReader(br, bytes.NewReader(slurp))
	if err != nil {
		log.Printf("Failed to parse non-image thumbnail cache blob %v: %v", br, err)
		return nil, err
	}
	fr, err := fileBlob.NewFileReader(ih.Cache)
	if err != nil {
		log.Printf("cached(%v) NewFileReader = %v", br, err)
		return nil, err
	}
	thumbCacheHitFile.Add(1)
	if imageDebug {
		log.Printf("Image Cache: fileref hit: %v\n", br)
	}
	return fr, nil
}

// Key format: "scaled:" + bref + ":" + width "x" + height
// where bref is the blobref of the unscaled image.
func cacheKey(bref string, width int, height int) string {
	return fmt.Sprintf("scaled:%v:%dx%d:tv%v", bref, width, height, images.ThumbnailVersion())
}

// ScaledCached reads the scaled version of the image in file,
// if it is in cache and writes it to buf.
//
// On successful read and population of buf, the returned format is non-empty.
// Almost all errors are not interesting. Real errors will be logged.
func (ih *ImageHandler) scaledCached(buf *bytes.Buffer, file blob.Ref) (format string) {
	key := cacheKey(file.String(), ih.MaxWidth, ih.MaxHeight)
	br, err := ih.ThumbMeta.Get(key)
	if err == errCacheMiss {
		return
	}
	if err != nil {
		log.Printf("Warning: thumbnail cachekey(%q)->meta lookup error: %v", key, err)
		return
	}
	fr, err := ih.cached(br)
	if err != nil {
		if imageDebug {
			log.Printf("Could not get cached image %v: %v\n", br, err)
		}
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

type formatAndImage struct {
	format string
	image  []byte
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

func (ih *ImageHandler) newFileReader(fileRef blob.Ref) (io.ReadCloser, error) {
	fi, ok := fileInfoPacked(ih.Search, ih.Fetcher, nil, fileRef)
	if debugPack {
		log.Printf("pkg/server/image.go: fileInfoPacked: ok=%v, %+v", ok, fi)
	}
	if ok {
		// This would be less gross if fileInfoPacked just
		// returned an io.ReadCloser, but then the download
		// handler would need more invasive changes for
		// ServeContent. So tolerate this for now.
		return struct {
			io.Reader
			io.Closer
		}{
			fi.rs,
			types.CloseFunc(fi.close),
		}, nil
	}
	// Default path, not going through blobpacked's fast path:
	return schema.NewFileReader(ih.Fetcher, fileRef)
}

func (ih *ImageHandler) scaleImage(fileRef blob.Ref) (*formatAndImage, error) {
	fr, err := ih.newFileReader(fileRef)
	if err != nil {
		return nil, err
	}
	defer fr.Close()

	sr := types.NewStatsReader(imageBytesFetchedVar, fr)
	sr, conf, err := imageConfigFromReader(sr)
	if err != nil {
		return nil, err
	}

	// TODO(wathiede): build a size table keyed by conf.ColorModel for
	// common color models for a more exact size estimate.

	// This value is an estimate of the memory required to decode an image.
	// PNGs range from 1-64 bits per pixel (not all of which are supported by
	// the Go standard parser). JPEGs encoded in YCbCr 4:4:4 are 3 byte/pixel.
	// For all other JPEGs this is an overestimate.  For GIFs it is 3x larger
	// than needed.  How accurate this estimate is depends on the mix of
	// images being resized concurrently.
	ramSize := int64(conf.Width) * int64(conf.Height) * 3

	if err = ih.ResizeSem.Acquire(ramSize); err != nil {
		return nil, err
	}
	defer ih.ResizeSem.Release(ramSize)

	i, imConfig, err := images.Decode(sr, &images.DecodeOpts{
		MaxWidth:  ih.MaxWidth,
		MaxHeight: ih.MaxHeight,
	})
	if err != nil {
		return nil, err
	}
	b := i.Bounds()
	format := imConfig.Format

	isSquare := b.Dx() == b.Dy()
	if ih.Square && !isSquare {
		i = squareImage(i)
		b = i.Bounds()
	}

	// Encode as a new image
	var buf bytes.Buffer
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
			thumbCacheHeader304.Add(1)
			rw.WriteHeader(http.StatusNotModified)
			return
		}
	} else {
		if !disableThumbCache && req.Header.Get("If-Modified-Since") != "" {
			thumbCacheHeader304.Add(1)
			rw.WriteHeader(http.StatusNotModified)
			return
		}
	}

	var imageData []byte
	format := ""
	cacheHit := false
	if ih.ThumbMeta != nil && !disableThumbCache {
		var buf bytes.Buffer
		format = ih.scaledCached(&buf, file)
		if format != "" {
			cacheHit = true
			imageData = buf.Bytes()
		}
	}

	if !cacheHit {
		thumbCacheMiss.Add(1)
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
		if ih.ThumbMeta != nil {
			err := ih.cacheScaled(imageData, key)
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
