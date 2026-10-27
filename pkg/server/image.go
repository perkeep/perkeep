/*
Copyright 2011 The Perkeep Authors

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
	"context"
	"errors"
	"expvar"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bradfitz/resizejpeg"
	_ "github.com/nf/cr2"
	"go4.org/readerutil"
	"go4.org/syncutil"
	"go4.org/syncutil/singleflight"
	"go4.org/types"
	"perkeep.org/internal/httputil"
	"perkeep.org/internal/images"
	"perkeep.org/internal/magic"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/constants"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/search"
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

func writeToCache(ctx context.Context, cache blobserver.Storage, thumbBytes []byte, name string) (br blob.Ref, err error) {
	tr := bytes.NewReader(thumbBytes)
	if len(thumbBytes) < constants.MaxBlobSize {
		br = blob.RefFromBytes(thumbBytes)
		_, err = blobserver.Receive(ctx, cache, br, tr)
	} else {
		// TODO: don't use rolling checksums when writing this. Tell
		// the filewriter to use 16 MB chunks instead.
		br, err = schema.WriteFileFromReader(ctx, cache, name, tr)
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
func (ih *ImageHandler) cacheScaled(ctx context.Context, thumbBytes []byte, name string) error {
	br, err := writeToCache(ctx, ih.Cache, thumbBytes, name)
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
func (ih *ImageHandler) cached(ctx context.Context, br blob.Ref) (io.ReadCloser, error) {
	rsc, _, err := ih.Cache.Fetch(ctx, br)
	if err != nil {
		return nil, err
	}
	slurp, err := io.ReadAll(rsc)
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
		return io.NopCloser(bytes.NewReader(slurp)), nil
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
func (ih *ImageHandler) scaledCached(ctx context.Context, buf *bytes.Buffer, file blob.Ref) (format string) {
	key := cacheKey(file.String(), ih.MaxWidth, ih.MaxHeight)
	br, err := ih.ThumbMeta.Get(key)
	if errors.Is(err, errCacheMiss) {
		return ""
	}
	if err != nil {
		log.Printf("Warning: thumbnail cachekey(%q)->meta lookup error: %v", key, err)
		return ""
	}
	fr, err := ih.cached(ctx, br)
	if err != nil {
		if imageDebug {
			log.Printf("Could not get cached image %v: %v\n", br, err)
		}
		return ""
	}
	defer fr.Close()
	_, err = io.Copy(buf, fr)
	if err != nil {
		return ""
	}
	mime := magic.MIMEType(buf.Bytes())
	format = strings.TrimPrefix(mime, "image/")

	if format == mime {
		log.Printf("Warning: unescaped MIME type %q of %v file for thumbnail %q", mime, br, key)
		return ""
	}
	return format
}

// Gate the number of concurrent image resizes to limit RAM & CPU use.

type formatAndImage struct {
	format string
	image  []byte
}

// imageConfigFromReader calls image.DecodeConfig on r. It returns an
// io.Reader that is the concatenation of the bytes read and the remaining r,
// the image configuration, its format, and the error from image.DecodeConfig.
func imageConfigFromReader(r io.Reader) (io.Reader, image.Config, string, error) {
	header := new(bytes.Buffer)
	tr := io.TeeReader(r, header)
	// We just need width & height for memory considerations, so we use the
	// standard library's DecodeConfig, skipping the EXIF parsing and
	// orientation correction for images.DecodeConfig. image.DecodeConfig is
	// able to deal with HEIC because it's registered indirectly in
	// internal/images via the gen2brain/heic package.
	conf, format, err := image.DecodeConfig(tr)
	return io.MultiReader(header, r), conf, format, err
}

// imageMemorySlots bounds both active resizes and workers waiting on the legacy
// semaphore, which has no context-aware Acquire. Canceled callers can leave a
// worker behind until memory becomes available, but never an unbounded number.
var imageMemorySlots = make(chan struct{}, 16)

func (ih *ImageHandler) acquireImageMemory(ctx context.Context, n int64) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ih.ResizeSem == nil {
		return nil, errors.New("image resize semaphore is not configured")
	}
	select {
	case imageMemorySlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	sem := ih.ResizeSem
	ready := make(chan error)
	go func() {
		err := sem.Acquire(n)
		select {
		case ready <- err:
		case <-ctx.Done():
			if err == nil {
				sem.Release(n)
			}
			<-imageMemorySlots
		}
	}()
	select {
	case err := <-ready:
		if err != nil {
			<-imageMemorySlots
			return nil, err
		}
		release := func() {
			sem.Release(n)
			<-imageMemorySlots
		}
		// Cancellation may race with the worker's successful acquisition.
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func imageMemoryEstimate(width, height int, bytesPerPixel int64) (int64, error) {
	if width <= 0 || height <= 0 || bytesPerPixel <= 0 ||
		int64(width) > math.MaxInt64/int64(height)/bytesPerPixel {
		return 0, errors.New("image dimensions overflow memory estimate")
	}
	return int64(width) * int64(height) * bytesPerPixel, nil
}

// jpegPostDecodeMemory budgets rotation and the encoded output buffer in
// addition to resizejpeg's decode plan. Rotation may swap the target bounds,
// but cannot increase their area or the source area.
func (ih *ImageHandler) jpegPostDecodeMemory(conf image.Config) (int64, error) {
	pixels, err := imageMemoryEstimate(conf.Width, conf.Height, 1)
	if err != nil {
		return 0, err
	}
	if ih.MaxWidth > 0 && ih.MaxHeight > 0 {
		// Only multiply the target dimensions when their product is smaller
		// than the source area, so oversized caller bounds cannot overflow.
		if int64(ih.MaxWidth) <= pixels/int64(ih.MaxHeight) {
			pixels = int64(ih.MaxWidth) * int64(ih.MaxHeight)
		}
	}
	const overhead = 256 << 10
	if pixels > (math.MaxInt64-overhead)/16 {
		return 0, errors.New("JPEG dimensions overflow memory estimate")
	}
	return pixels*16 + overhead, nil
}

func (ih *ImageHandler) newFileReader(ctx context.Context, fileRef blob.Ref) (io.ReadCloser, error) {
	fi, ok := fileInfoPacked(ctx, ih.Search, ih.Fetcher, nil, fileRef)
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
	return schema.NewFileReader(ctx, ih.Fetcher, fileRef)
}

// ScaleImage reads the image file schema at fileRef using ih.Fetcher and returns
// its encoded bytes, scaled according to ih.MaxWidth, ih.MaxHeight, and ih.Square.
// The returned format is "png" for PNG images and "jpeg" for all other supported
// formats. Fetcher and ResizeSem must be set; Search is optional.
// ScaleImage does not read or write the thumbnail cache.
func (ih *ImageHandler) ScaleImage(ctx context.Context, fileRef blob.Ref) (data []byte, format string, err error) {
	im, err := ih.scaleImage(ctx, fileRef)
	if err != nil {
		return nil, "", err
	}
	return im.image, im.format, nil
}

func (ih *ImageHandler) scaleImage(ctx context.Context, fileRef blob.Ref) (*formatAndImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fr, err := ih.newFileReader(ctx, fileRef)
	if err != nil {
		return nil, err
	}
	defer fr.Close()

	sr := readerutil.NewStatsReader(imageBytesFetchedVar, fr)
	sr, conf, sourceFormat, err := imageConfigFromReader(sr)
	if err != nil {
		return nil, err
	}

	opts := &images.DecodeOpts{
		MaxWidth: ih.MaxWidth, MaxHeight: ih.MaxHeight,
	}
	var release func()
	// The JPEG hook transfers its one reservation to us rather than releasing
	// it when decoding ends: orientation, cropping, and encoding still retain
	// the destination. In particular, never reserve full source pixels before
	// asking resizejpeg for its streaming/compact progressive memory plan.
	defer func() {
		if release != nil {
			release()
		}
	}()
	if sourceFormat == "jpeg" {
		extra, err := ih.jpegPostDecodeMemory(conf)
		if err != nil {
			return nil, err
		}
		opts.JPEGOptions = &resizejpeg.ResizeOptions{
			AllowFullDecode: true,
			// Keep at least 2x the thumbnail's width and height (4x its
			// pixels) before filtering, rather than the default 4x per
			// dimension. This lets typical UI thumbnails of large photos
			// use the 1/4 or 1/8 scaled IDCT, which greatly cuts the decode
			// and Lanczos work, at little visible cost at thumbnail sizes.
			ThumbnailHeadroom: 2,
			MemAllocSemaphore: func(n int64) (func(), error) {
				if n < 0 || n > math.MaxInt64-extra {
					return nil, errors.New("JPEG decode plan overflows memory estimate")
				}
				var err error
				release, err = ih.acquireImageMemory(ctx, n+extra)
				return nil, err
			},
		}
	} else {
		// Preserve the legacy estimate for other formats: 3 bytes per source
		// pixel (an approximation; e.g. PNGs have varying pixel depths).
		ramSize, err := imageMemoryEstimate(conf.Width, conf.Height, 3)
		if err != nil {
			return nil, err
		}
		release, err = ih.acquireImageMemory(ctx, ramSize)
		if err != nil {
			return nil, err
		}
	}

	i, imConfig, err := images.Decode(sr, opts)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := i.Bounds()
	format := imConfig.Format

	isSquare := b.Dx() == b.Dy()
	if ih.Square && !isSquare {
		i = squareImage(i)
		b = i.Bounds()
		_ = b
	}

	// Encode as a new image
	var buf bytes.Buffer
	switch format {
	case "png":
		err = png.Encode(&buf, i)
	default:
		// Recompress all other formats as JPEG.
		format = "jpeg"
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
	ctx := req.Context()
	if ctx.Err() != nil {
		return
	}

	if !httputil.IsGet(req) {
		http.Error(rw, "Invalid method", http.StatusBadRequest)
		return
	}

	mw, mh := ih.MaxWidth, ih.MaxHeight
	if mw <= 0 || mh <= 0 || mw > search.MaxImageSize || mh > search.MaxImageSize {
		http.Error(rw, "bogus dimensions", http.StatusBadRequest)
		return
	}

	// Permanodes are mutable: describe them before considering any validator.
	if ih.Search == nil {
		http.Error(rw, "thumbnail search unavailable", http.StatusInternalServerError)
		return
	}
	res, err := ih.Search.Describe(ctx, &search.DescribeRequest{BlobRef: file, Depth: 1})
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		http.Error(rw, "thumbnail metadata unavailable", http.StatusInternalServerError)
		return
	}
	db := res.Meta[file.String()]
	if db == nil {
		http.NotFound(rw, req)
		return
	}
	if db.Permanode != nil && db.Permanode.Attr.Get(nodeattr.Type) == nodeattr.FitnessActivity {
		ih.serveFitnessThumbnail(rw, req, file, db.Permanode)
		return
	}
	if db.File == nil || !strings.HasPrefix(db.File.MIMEType, "image/") {
		http.NotFound(rw, req)
		return
	}

	key := cacheKey(file.String(), mw, mh)
	_, etag, _ := strings.Cut(blob.RefFromString(key).String(), "-")
	inm := req.Header.Get("If-None-Match")
	if inm != "" {
		if strings.Trim(inm, `"`) == etag {
			thumbCacheHeader304.Add(1)
			rw.WriteHeader(http.StatusNotModified)
			return
		}
	} else if !disableThumbCache && req.Header.Get("If-Modified-Since") != "" {
		thumbCacheHeader304.Add(1)
		rw.WriteHeader(http.StatusNotModified)
		return
	}

	if db.File.MIMEType == "image/svg+xml" {
		fr, err := schema.NewFileReader(ctx, ih.Fetcher, file)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		defer fr.Close()

		h := rw.Header()
		if !disableThumbCache {
			h.Set("Expires", time.Now().UTC().Add(oneYear).Format(http.TimeFormat))
			h.Set("Etag", strconv.Quote(etag))
		}
		h.Set("Content-Type", db.File.MIMEType)

		http.ServeContent(rw, req, file.String(), time.Now(), fr)
		return
	}

	var imageData []byte
	var format string

	cacheHit := false
	if ih.ThumbMeta != nil && !disableThumbCache {
		var buf bytes.Buffer
		format = ih.scaledCached(ctx, &buf, file)
		if format != "" {
			cacheHit = true
			imageData = buf.Bytes()
		}
	}

	if !cacheHit {
		thumbCacheMiss.Add(1)
		imi, err := singleResize.Do(key, func() (any, error) {
			return ih.scaleImage(ctx, file)
		})
		if err != nil {
			http.Error(rw, err.Error(), 500)
			return
		}
		im := imi.(*formatAndImage)
		imageData = im.image
		format = im.format

		if ih.ThumbMeta != nil {
			err := ih.cacheScaled(ctx, imageData, key)
			if err != nil {
				log.Printf("image resize error: %v", err)
			}
		}
	}

	h := rw.Header()
	if !disableThumbCache {
		h.Set("Expires", time.Now().UTC().Add(oneYear).Format(http.TimeFormat))
		h.Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		h.Set("Etag", strconv.Quote(etag))
	}
	h.Set("Content-Type", "image/"+format)
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
