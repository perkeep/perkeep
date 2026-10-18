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

package server

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"perkeep.org/pkg/blob"
)

const fitnessMaxBasemapTiles = 16

// These errors accompany a successful PNG, but must not poison the map cache.
var (
	errFitnessMapFallback    = errors.New("fitness basemap unavailable")
	errFitnessMapUncacheable = errors.New("fitness basemap requires revalidation")
)

func fitnessMapCacheKey(track, sport string, w, h int, profile string) string {
	return "map:" + fitnessCacheKey(track, sport, w, h) + ":" + profile + ":" + blob.RefFromString(fitnessMapAttributionText()).String()
}

func fitnessMapAttributionText() string {
	credit := strings.TrimSpace(os.Getenv("PERKEEP_FITNESS_TILE_ATTRIBUTION"))
	if credit == "" || len(credit) > 512 {
		return "© OpenStreetMap contributors"
	}
	return credit
}

// fitnessMapReferer preserves a valid web origin without disclosing the route
// URL, search terms, account identifiers, or URL credentials to a tile server.
func fitnessMapReferer(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/"}).String()
}

func fitnessValidCachedPNG(data []byte, w, h int) bool {
	if len(data) == 0 || int64(len(data)) > int64(w)*int64(h)*4+(1<<20) {
		return false
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	return err == nil && cfg.Width == w && cfg.Height == h
}

func (ih *ImageHandler) serveFitnessBasemap(rw http.ResponseWriter, req *http.Request, track, sport string, w, h int, source *fitnessTileSource, sourceErr error) {
	ctx := req.Context()
	head := rw.Header()
	head.Set("Content-Type", "image/png")
	head.Set("X-Content-Type-Options", "nosniff")
	head.Set("Cache-Control", "private, max-age=0, must-revalidate")
	profile := "unavailable"
	if source != nil {
		profile = source.profile()
	}
	key := fitnessMapCacheKey(track, sport, w, h, profile)
	var data []byte
	if source != nil && !disableThumbCache {
		if cached, _, ok := source.cachedThumbnail(key); ok && fitnessValidCachedPNG(cached, w, h) {
			data = cached
			thumbCacheHitFull.Add(1)
		}
	}
	// HEAD is metadata, not a viewed thumbnail: no local GPX render or tile HTTP
	// on a miss. In particular, a stale composite cannot yield a premature 304.
	if req.Method == http.MethodHead && len(data) == 0 {
		head.Set("Cache-Control", "private, no-store")
		return
	}
	var renderErr error
	if len(data) == 0 {
		thumbCacheMiss.Add(1)
		data, renderErr = fitnessRenderOnce(ctx, key, func() ([]byte, error) {
			// Check again after entering the flight to avoid concurrent stale reads
			// causing a second fetch after the first request stored a fresh composite.
			if source != nil && !disableThumbCache {
				if cached, _, ok := source.cachedThumbnail(key); ok && fitnessValidCachedPNG(cached, w, h) {
					return cached, nil
				}
			}
			return ih.renderFitnessBasemap(ctx, track, sport, w, h, source, sourceErr, fitnessMapReferer(req.Referer()), key)
		})
	}
	if ctx.Err() != nil {
		return
	}
	fallback := errors.Is(renderErr, errFitnessMapFallback) || errors.Is(renderErr, errFitnessMissing)
	uncacheable := errors.Is(renderErr, errFitnessMapUncacheable)
	if renderErr != nil && !fallback && !uncacheable {
		head.Del("Content-Type")
		head.Set("Cache-Control", "private, no-store")
		http.Error(rw, "fitness thumbnail unavailable", http.StatusUnprocessableEntity)
		return
	}
	if fallback || uncacheable {
		head.Set("Cache-Control", "private, no-store")
	}
	if !fallback {
		// Validate current tile freshness before honoring a conditional request.
		// The ETag describes actual bytes, never just a GPX/provider input key.
		etag := strconv.Quote(blob.RefFromBytes(data).String())
		head.Set("ETag", etag)
		if !disableThumbCache && fitnessIfNoneMatch(req.Header.Get("If-None-Match"), etag) {
			thumbCacheHeader304.Add(1)
			rw.WriteHeader(http.StatusNotModified)
			return
		}
	}
	head.Set("Content-Length", strconv.Itoa(len(data)))
	if req.Method == http.MethodGet {
		n, _ := rw.Write(data)
		imageBytesServedVar.Add(int64(n))
	}
}

func (ih *ImageHandler) renderFitnessBasemap(ctx context.Context, track, sport string, w, h int, source *fitnessTileSource, sourceErr error, referer, key string) ([]byte, error) {
	// Existing 16 B/pixel covers the canvas, vector rasterizer and PNG buffers.
	// Tiles are bounded independently: at most sixteen 256-square RGBA images.
	release, err := ih.acquireFitnessMemory(ctx, int64(w)*int64(h)*16+fitnessMaxGPXBytes*4+fitnessMaxPoints*64+fitnessMaxBasemapTiles*256*256*4)
	if err != nil {
		return nil, err
	}
	defer release()
	segments, trackErr := ih.readFitnessTrack(ctx, track)
	if trackErr != nil && !errors.Is(trackErr, errFitnessMissing) {
		return nil, trackErr
	}
	plan := fitnessProjectionFor(segments, w, h)
	var background *image.RGBA
	var resultErr error
	cacheable := true
	expires := time.Now().Add(7 * 24 * time.Hour)
	if len(plan.projectedSegments) != 0 {
		if source == nil || sourceErr != nil {
			resultErr = errFitnessMapFallback
		} else {
			background, expires, cacheable, err = fitnessDrawBasemap(ctx, plan, source, referer)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				// Network/cooldown/cache failures never hide an otherwise valid route.
				background, resultErr = nil, errFitnessMapFallback
			}
		}
	}
	if errors.Is(trackErr, errFitnessMissing) {
		resultErr = trackErr
	}
	// An invalid map configuration also must not create an eternal neutral cache.
	if source == nil || sourceErr != nil {
		resultErr = errFitnessMapFallback
	}
	img, err := drawFitnessProjectedThumbnail(ctx, plan, sport, background)
	if err != nil {
		return nil, err
	}
	if background != nil && !drawFitnessAttribution(img) {
		// Never display a basemap with truncated provider terms. This failure is
		// treated just like unavailable tiles: a successful, uncached route card.
		img, err = drawFitnessProjectedThumbnail(ctx, plan, sport, nil)
		if err != nil {
			return nil, err
		}
		resultErr = errFitnessMapFallback
	}
	var buf bytes.Buffer
	if err := png.Encode(fitnessContextWriter{ctx, &buf}, img); err != nil {
		return nil, err
	}
	if resultErr == nil {
		if !cacheable {
			resultErr = errFitnessMapUncacheable
		} else if source != nil && !disableThumbCache && ctx.Err() == nil {
			// This cache is private filesystem storage, never ThumbMeta/blob storage.
			_ = source.storeThumbnail(key, buf.Bytes(), expires)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return buf.Bytes(), resultErr
}

// Raw x is deliberately unwrapped; only the upstream request x is modulo 2^z.
// All ranges cover exactly the viewport, without neighbor-region prefetch.
type fitnessTileViewport struct {
	zoom, minX, maxX, minY, maxY int
	worldPixels                  float64
}

func fitnessViewportAtZoom(plan fitnessProjection, zoom int) fitnessTileViewport {
	world := math.Ldexp(256, zoom)
	worldScale := world / (2 * math.Pi)
	cx := (plan.centreMercatorX + math.Pi) * worldScale
	cy := (plan.centreMercatorY + math.Pi) * worldScale
	halfW := float64(plan.width) / 2 / plan.scalePixelsPerRad * worldScale
	halfH := float64(plan.height) / 2 / plan.scalePixelsPerRad * worldScale
	n := 1 << zoom
	boundary := func(pixel float64) float64 {
		v := pixel / 256
		// Adding pi to a Mercator center can introduce a few ulps of
		// cancellation error. Snap only machine-precision noise at exact
		// tile edges, so an edge-touch does not fetch a neighboring region.
		if nearest := math.Round(v); math.Abs(v-nearest) <= math.Max(float64(n), math.Abs(v))*2e-15 {
			return nearest
		}
		return v
	}
	return fitnessTileViewport{
		zoom: zoom, worldPixels: world,
		minX: int(math.Floor(boundary(cx - halfW))), maxX: int(math.Ceil(boundary(cx+halfW))) - 1,
		minY: max(0, int(math.Floor(boundary(cy-halfH)))), maxY: min(n-1, int(math.Ceil(boundary(cy+halfH)))-1),
	}
}

func fitnessBasemapViewport(plan fitnessProjection) fitnessTileViewport {
	zoom := min(18, max(0, int(math.Floor(math.Log2(plan.scalePixelsPerRad*2*math.Pi/256)))))
	for {
		vp := fitnessViewportAtZoom(plan, zoom)
		count := int64(vp.maxX-vp.minX+1) * int64(max(0, vp.maxY-vp.minY+1))
		if count <= fitnessMaxBasemapTiles || zoom == 0 {
			return vp
		}
		zoom--
	}
}

func fitnessDrawBasemap(ctx context.Context, plan fitnessProjection, source *fitnessTileSource, referer string) (*image.RGBA, time.Time, bool, error) {
	vp := fitnessBasemapViewport(plan)
	count := int64(vp.maxX-vp.minX+1) * int64(max(0, vp.maxY-vp.minY+1))
	if count <= 0 || count > fitnessMaxBasemapTiles {
		return nil, time.Time{}, false, errFitnessLimit
	}
	// Duplicate wrapped tiles (a world-scale route) need only one network request.
	fetched := make(map[image.Point]fitnessTile, int(count))
	n := 1 << vp.zoom
	for y := vp.minY; y <= vp.maxY; y++ {
		for x := vp.minX; x <= vp.maxX; x++ {
			wrappedX := ((x % n) + n) % n
			fetched[image.Pt(wrappedX, y)] = fitnessTile{}
		}
	}
	// Fan out only the exact, bounded viewport. Workers do not touch the map;
	// the caller aggregates results and cancels remaining work on any failure.
	fetchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type tileResult struct {
		coord image.Point
		tile  fitnessTile
		err   error
	}
	results := make(chan tileResult, len(fetched))
	for coord := range fetched {
		go func() {
			tile, err := source.tile(fetchCtx, vp.zoom, coord.X, coord.Y, referer, true)
			results <- tileResult{coord, tile, err}
		}()
	}
	var expires time.Time
	cacheable := true
	var fetchErr error
	for range len(fetched) {
		result := <-results
		if result.err == nil && (result.tile.Image == nil || result.tile.Image.Bounds().Dx() != 256 || result.tile.Image.Bounds().Dy() != 256) {
			result.err = errFitnessMapFallback
		}
		if result.err != nil {
			if fetchErr == nil {
				fetchErr = result.err
				cancel()
			}
			continue
		}
		fetched[result.coord] = result.tile
		if expires.IsZero() || result.tile.Expires.Before(expires) {
			expires = result.tile.Expires
		}
		cacheable = cacheable && result.tile.Cacheable
	}
	if fetchErr != nil {
		return nil, time.Time{}, false, fetchErr
	}
	img := image.NewRGBA(image.Rect(0, 0, plan.width, plan.height))
	// Areas beyond the Web Mercator polar limit have no fabricated geographic map.
	draw.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{246, 248, 250, 255}), image.Point{}, draw.Src)
	// Inverse-sample output pixel centers through the canonical Mercator
	// transform. Independent tile-origin arithmetic can introduce a one-pixel
	// drift or seam at exact boundaries through cancellation and floor rounding.
	// Precompute horizontal samples; Point.X is tile x and Point.Y is pixel x.
	sourceX := make([]image.Point, plan.width)
	world := int(vp.worldPixels)
	for x := range sourceX {
		mx := plan.centreMercatorX + (float64(x)+.5-float64(plan.width)/2)/plan.scalePixelsPerRad
		px := int(math.Floor((mx + math.Pi) / (2 * math.Pi) * vp.worldPixels))
		px = ((px % world) + world) % world
		sourceX[x] = image.Pt(px/256, px%256)
	}
	for y := 0; y < plan.height; y++ {
		if err := ctx.Err(); err != nil {
			return nil, time.Time{}, false, err
		}
		my := plan.centreMercatorY + (float64(y)+.5-float64(plan.height)/2)/plan.scalePixelsPerRad
		py := int(math.Floor((my + math.Pi) / (2 * math.Pi) * vp.worldPixels))
		if py < 0 || py >= world {
			continue
		}
		for x, sx := range sourceX {
			tile, ok := fetched[image.Pt(sx.X, py/256)]
			if !ok {
				return nil, time.Time{}, false, errFitnessMapFallback
			}
			bounds := tile.Image.Bounds()
			img.Set(x, y, tile.Image.At(bounds.Min.X+sx.Y, bounds.Min.Y+py%256))
		}
	}
	return img, expires, cacheable, nil
}

var fitnessAttributionFont struct {
	sync.Once
	font *opentype.Font
}

// A fitted layout owns its face. Bounds include every line's actual ink and
// advance, rather than clipping a too-tall paragraph to the image dimensions.
type fitnessAttributionLayout struct {
	face                                  font.Face
	lines                                 []string
	width, height, minX, minY, lineHeight int
}

func fitnessFitAttribution(credit string, w, h int) (fitnessAttributionLayout, bool) {
	fitnessAttributionFont.Do(func() {
		fitnessAttributionFont.font, _ = opentype.Parse(goregular.TTF)
	})
	if fitnessAttributionFont.font == nil || w <= 8 || h <= 4 {
		return fitnessAttributionLayout{}, false
	}
	words := strings.Fields(credit)
	if len(words) == 0 {
		return fitnessAttributionLayout{}, false
	}
	// Below six pixels the text stops being useful credit. Prefer an honest
	// route-only card over silently clipping terms or rendering illegible text.
	for size := 13.0; size >= 6; size-- {
		face, err := opentype.NewFace(fitnessAttributionFont.font, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return fitnessAttributionLayout{}, false
		}
		layout := fitnessAttributionLayout{face: face}
		line := ""
		for _, word := range words {
			next := word
			if line != "" {
				next = line + " " + word
			}
			if line != "" && font.MeasureString(face, next).Ceil()+8 > w {
				layout.lines = append(layout.lines, line)
				line = word
			} else {
				line = next
			}
		}
		layout.lines = append(layout.lines, line)
		metrics := face.Metrics()
		layout.lineHeight = max(metrics.Height.Ceil(), metrics.Ascent.Ceil()+metrics.Descent.Ceil())
		maxX, maxY := 0, 0
		for i, line := range layout.lines {
			ink, advance := font.BoundString(face, line)
			layout.minX = min(layout.minX, ink.Min.X.Floor())
			maxX = max(maxX, ink.Max.X.Ceil(), advance.Ceil())
			layout.minY = min(layout.minY, ink.Min.Y.Floor()+i*layout.lineHeight)
			maxY = max(maxY, ink.Max.Y.Ceil()+i*layout.lineHeight)
		}
		layout.width, layout.height = maxX-layout.minX+8, maxY-layout.minY+4
		if layout.width <= w && layout.height <= h {
			return layout, true
		}
		face.Close()
	}
	return fitnessAttributionLayout{}, false
}

// The downloadable PNG has its own full attribution; the UI adds a linked
// copyright notice too. All font bytes are embedded locally, with no font HTTP.
// False means nothing was drawn: the complete credit could not fit legibly.
func drawFitnessAttribution(img *image.RGBA) bool {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	layout, ok := fitnessFitAttribution(fitnessMapAttributionText(), w, h)
	if !ok {
		return false
	}
	defer layout.face.Close()
	rect := image.Rect(w-layout.width, h-layout.height, w, h)
	draw.Draw(img, rect, image.NewUniform(color.NRGBA{255, 255, 255, 238}), image.Point{}, draw.Over)
	d := font.Drawer{Dst: img, Src: image.NewUniform(color.NRGBA{45, 55, 65, 255}), Face: layout.face}
	for i, line := range layout.lines {
		d.Dot = fixed.P(rect.Min.X+4-layout.minX, rect.Min.Y+2-layout.minY+i*layout.lineHeight)
		d.DrawString(line)
	}
	return true
}
