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
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"go4.org/syncutil"
	"golang.org/x/image/vector"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/search"
)

const (
	fitnessRenderVersion = 1
	fitnessMaxGPXBytes   = 16 << 20
	fitnessMaxPoints     = 200000
)

var (
	errFitnessGPX   = errors.New("invalid fitness track")
	errFitnessLimit = errors.New("fitness track exceeds thumbnail limits")
	// Limit both renders and pending acquisitions of the shared image semaphore.
	fitnessRenderSlots = make(chan struct{}, 4)
	fitnessFallbackSem = syncutil.NewSem(256 << 20)
	errFitnessMissing  = errors.New("fitness track missing")
)

// fitnessCacheKey describes the rendered source, not its mutable permanode or
// untrusted URL hints. Only derived PNGs are stored in the UI thumbnail cache.
func fitnessCacheKey(track, sport string, width, height int) string {
	return fmt.Sprintf("fitness:%s:%s:%dx%d:fv%d", track, sport, width, height, fitnessRenderVersion)
}

func fitnessSport(s string) string {
	switch s {
	case "run", "walk", "hike", "ride", "swim", "row", "ski", "workout":
		return s
	default:
		return "other"
	}
}

// ifNoneMatch uses weak comparison for GET/HEAD, including lists and wildcard.
func fitnessIfNoneMatch(header, etag string) bool {
	for _, item := range strings.Split(header, ",") {
		item = strings.TrimSpace(item)
		if item == "*" || strings.TrimPrefix(item, "W/") == etag {
			return true
		}
	}
	return false
}

func (ih *ImageHandler) serveFitnessThumbnail(rw http.ResponseWriter, req *http.Request, _ blob.Ref, dp *search.DescribedPermanode) {
	if !httputil.IsGet(req) {
		http.Error(rw, "Invalid method", http.StatusBadRequest)
		return
	}
	w, h := ih.MaxWidth, ih.MaxHeight
	if w <= 0 || h <= 0 || w > search.MaxImageSize || h > search.MaxImageSize {
		http.Error(rw, "bogus dimensions", http.StatusBadRequest)
		return
	}
	if dp == nil {
		http.NotFound(rw, req)
		return
	}
	ctx := req.Context()
	if ctx.Err() != nil {
		return
	}
	// Deliberately do not read fitnessTrack or fitnessActivityType from the URL.
	track := dp.Attr.Get(nodeattr.FitnessTrack)
	sport := fitnessSport(dp.Attr.Get(nodeattr.FitnessActivityType))
	key := fitnessCacheKey(track, sport, w, h)
	etag := strconv.Quote(blob.RefFromString(key).String())
	head := rw.Header()
	head.Set("Cache-Control", "private, max-age=0, must-revalidate")
	head.Set("ETag", etag)
	head.Set("Content-Type", "image/png")
	head.Set("X-Content-Type-Options", "nosniff")
	// No Last-Modified/Expires: the PN may point at a different track tomorrow.
	if !disableThumbCache && fitnessIfNoneMatch(req.Header.Get("If-None-Match"), etag) {
		thumbCacheHeader304.Add(1)
		rw.WriteHeader(http.StatusNotModified)
		return
	}

	var data []byte
	if ih.Cache != nil && ih.ThumbMeta != nil && !disableThumbCache {
		if br, err := ih.ThumbMeta.Get(key); err == nil {
			if rc, size, err := ih.Cache.Fetch(ctx, br); err == nil {
				// Keep corrupted cache entries bounded as well.
				limit := int64(w*h*4 + (1 << 20))
				if int64(size) <= limit {
					data, err = io.ReadAll(io.LimitReader(fitnessContextReader{ctx, rc}, limit+1))
				}
				rc.Close()
				cfg, pngErr := png.DecodeConfig(bytes.NewReader(data))
				if err != nil || int64(len(data)) > limit || pngErr != nil || cfg.Width != w || cfg.Height != h {
					data = nil
				} else {
					thumbCacheHitFull.Add(1)
				}
			}
		}
	}
	if len(data) == 0 {
		thumbCacheMiss.Add(1)
		var err error
		data, err = fitnessRenderOnce(ctx, key, func() ([]byte, error) {
			return ih.renderFitnessThumbnail(ctx, track, sport, w, h)
		})
		missing := errors.Is(err, errFitnessMissing)
		if missing {
			head.Del("ETag")
			head.Set("Cache-Control", "private, no-store")
		}
		if err != nil && !missing {
			if ctx.Err() != nil {
				return
			}
			// Parser and storage errors may contain private coordinates or names.
			head.Del("ETag")
			head.Set("Cache-Control", "private, no-store")
			head.Del("Content-Type")
			http.Error(rw, "fitness thumbnail unavailable", http.StatusUnprocessableEntity)
			return
		}
		if !missing && ih.Cache != nil && ih.ThumbMeta != nil && !disableThumbCache {
			// Caching is best-effort. Never log the route or its source metadata.
			_ = ih.cacheScaled(ctx, data, key)
		}
	}
	if ctx.Err() != nil {
		return
	}
	head.Set("Content-Length", strconv.Itoa(len(data)))
	if req.Method == http.MethodGet {
		n, _ := rw.Write(data)
		imageBytesServedVar.Add(int64(n))
	}
}

// acquireFitnessMemory adapts the legacy semaphore (which lacks cancellation).
// A canceled wait returns promptly; its bounded worker releases any eventual
// acquisition, without leaking permits or an unbounded number of goroutines.
func (ih *ImageHandler) acquireFitnessMemory(ctx context.Context, n int64) (func(), error) {
	select {
	case fitnessRenderSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	sem := ih.ResizeSem
	if sem == nil {
		sem = fitnessFallbackSem
	}
	ready := make(chan error)
	go func() {
		err := sem.Acquire(n)
		select {
		case ready <- err:
		case <-ctx.Done():
			if err == nil {
				sem.Release(n)
			}
			<-fitnessRenderSlots
		}
	}()
	select {
	case err := <-ready:
		if err != nil {
			<-fitnessRenderSlots
			return nil, err
		}
		return func() { sem.Release(n); <-fitnessRenderSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (ih *ImageHandler) renderFitnessThumbnail(ctx context.Context, track, sport string, w, h int) ([]byte, error) {
	// RGBA + vector rasterizer + PNG buffers + bounded XML and point storage.
	release, err := ih.acquireFitnessMemory(ctx, int64(w)*int64(h)*16+fitnessMaxGPXBytes*4+fitnessMaxPoints*64)
	if err != nil {
		return nil, err
	}
	defer release()
	var segments [][]fitnessPoint
	var missing error
	if track != "" {
		ref, ok := blob.Parse(track)
		if !ok || ih.Fetcher == nil {
			return nil, errFitnessGPX
		}
		// FileReader uses background contexts for chunk reads; bind every
		// fetch to this request instead, including those content chunks.
		fr, err := schema.NewFileReader(ctx, fitnessRequestFetcher{ctx, ih.Fetcher}, ref)
		if ctx.Err() != nil {
			if fr != nil {
				fr.Close()
			}
			return nil, ctx.Err()
		}
		if errors.Is(err, os.ErrNotExist) {
			missing = errFitnessMissing
		} else if err != nil {
			return nil, errFitnessGPX
		} else {
			segments, err = parseFitnessGPX(ctx, fr)
			fr.Close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, os.ErrNotExist) {
				segments, missing = nil, errFitnessMissing
			} else if err != nil {
				return nil, err
			}
		}
	}
	img, err := drawFitnessThumbnail(ctx, segments, sport, w, h)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(fitnessContextWriter{ctx, &buf}, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), missing
}

// Request-bound blob fetching prevents FileReader's internal background
// contexts from extending a thumbnail read beyond a canceled request.
type fitnessRequestFetcher struct {
	ctx context.Context
	src blob.Fetcher
}

func (f fitnessRequestFetcher) Fetch(_ context.Context, ref blob.Ref) (io.ReadCloser, uint32, error) {
	if err := f.ctx.Err(); err != nil {
		return nil, 0, err
	}
	return f.src.Fetch(f.ctx, ref)
}

// Unlike the legacy singleflight group, fitness waiters can cancel promptly.
// If a leader was canceled, remaining requests retry with their own context.
var fitnessFlights = struct {
	sync.Mutex
	m map[string]*fitnessFlight
}{m: make(map[string]*fitnessFlight)}

type fitnessFlight struct {
	done chan struct{}
	data []byte
	err  error
}

func fitnessRenderOnce(ctx context.Context, key string, render func() ([]byte, error)) ([]byte, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fitnessFlights.Lock()
		if pending := fitnessFlights.m[key]; pending != nil {
			fitnessFlights.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending.done:
				if errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded) {
					continue
				}
				return pending.data, pending.err
			}
		}
		pending := &fitnessFlight{done: make(chan struct{})}
		fitnessFlights.m[key] = pending
		fitnessFlights.Unlock()
		pending.data, pending.err = render()
		fitnessFlights.Lock()
		delete(fitnessFlights.m, key)
		close(pending.done)
		fitnessFlights.Unlock()
		return pending.data, pending.err
	}
}

type fitnessContextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w fitnessContextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(p)
}

type fitnessContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r fitnessContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

type fitnessPoint struct{ x, y float64 }

// parseFitnessGPX streams only standard track segments. Invalid coordinates
// make a gap instead of inventing a line across missing GPS samples. XML entity
// references are not resolved externally; neither GPX nor the renderer uses HTTP.
func parseFitnessGPX(ctx context.Context, r io.Reader) ([][]fitnessPoint, error) {
	limited := &io.LimitedReader{R: fitnessContextReader{ctx, r}, N: fitnessMaxGPXBytes + 1}
	dec := xml.NewDecoder(limited)
	var path []string
	var segments [][]fitnessPoint
	var segment []fitnessPoint
	root, closed := false, false
	points, segmentCount := 0, 0
	flush := func() {
		if len(segment) != 0 {
			segments = append(segments, segment)
			segment = nil
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tok, err := dec.Token()
		if limited.N <= 0 {
			return nil, errFitnessLimit
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			return nil, errFitnessGPX
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(path) == 0 {
				if root || closed || t.Name.Local != "gpx" {
					return nil, errFitnessGPX
				}
				root = true
			}
			path = append(path, t.Name.Local)
			if len(path) > 64 {
				return nil, errFitnessLimit
			}
			if len(path) == 3 && path[1] == "trk" && path[2] == "trkseg" {
				segmentCount++
				if segmentCount > fitnessMaxPoints {
					return nil, errFitnessLimit
				}
			}
			if len(path) != 4 || path[1] != "trk" || path[2] != "trkseg" || path[3] != "trkpt" {
				continue
			}
			points++
			if points > fitnessMaxPoints {
				return nil, errFitnessLimit
			}
			lat, lon := math.NaN(), math.NaN()
			valid, seenLat, seenLon := true, false, false
			for _, a := range t.Attr {
				if a.Name.Space != "" || (a.Name.Local != "lat" && a.Name.Local != "lon") {
					continue
				}
				value, err := strconv.ParseFloat(a.Value, 64)
				if err != nil {
					valid = false
				}
				if a.Name.Local == "lat" {
					if seenLat {
						valid = false
					}
					seenLat, lat = true, value
				} else {
					if seenLon {
						valid = false
					}
					seenLon, lon = true, value
				}
			}
			if !valid || math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
				flush()
				continue
			}
			segment = append(segment, fitnessPoint{lon, lat})
		case xml.EndElement:
			if len(path) == 3 && path[1] == "trk" && path[2] == "trkseg" {
				flush()
			}
			path = path[:len(path)-1]
			if len(path) == 0 {
				closed = true
			}
		case xml.CharData:
			if len(path) == 0 && strings.TrimSpace(string(t)) != "" {
				return nil, errFitnessGPX
			}
		}
	}
	if !root || !closed {
		return nil, errFitnessGPX
	}
	return segments, nil
}

// fitnessProject unwraps longitude across the antimeridian, then fits a
// conformal Web Mercator projection with a single scale for both dimensions.
func fitnessProject(segments [][]fitnessPoint, w, h int) [][]fitnessPoint {
	var projected [][]fitnessPoint
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	anchor := math.NaN()
	for _, segment := range segments {
		var ps []fitnessPoint
		prev := anchor
		for _, p := range segment {
			x := p.x * math.Pi / 180
			if !math.IsNaN(prev) {
				x = prev + math.Remainder(x-prev, 2*math.Pi)
			}
			if math.IsNaN(anchor) {
				anchor = x
			}
			prev = x
			lat := math.Max(-85.05112878, math.Min(85.05112878, p.y)) * math.Pi / 180
			y := -math.Log(math.Tan(math.Pi/4 + lat/2))
			ps = append(ps, fitnessPoint{x, y})
			minX, maxX = math.Min(minX, x), math.Max(maxX, x)
			minY, maxY = math.Min(minY, y), math.Max(maxY, y)
		}
		if len(ps) != 0 {
			projected = append(projected, ps)
		}
	}
	if len(projected) == 0 {
		return nil
	}
	pad := math.Max(12, math.Min(float64(w), float64(h))*.13)
	aw, ah := math.Max(1, float64(w)-2*pad), math.Max(1, float64(h)-2*pad)
	scale := math.Inf(1)
	if maxX > minX {
		scale = math.Min(scale, aw/(maxX-minX))
	}
	if maxY > minY {
		scale = math.Min(scale, ah/(maxY-minY))
	}
	if math.IsInf(scale, 0) {
		scale = 1
	}
	cx, cy := (minX+maxX)/2, (minY+maxY)/2
	for _, ps := range projected {
		for i, p := range ps {
			ps[i] = fitnessPoint{float64(w)/2 + (p.x-cx)*scale, float64(h)/2 + (p.y-cy)*scale}
		}
	}
	return projected
}

// A vector path accumulates round caps and joins, rasterized with antialiasing.
type fitnessPen struct{ z *vector.Rasterizer }

func (p fitnessPen) circle(x, y, r float64) {
	const k = .5522847498
	p.z.MoveTo(float32(x+r), float32(y))
	p.z.CubeTo(float32(x+r), float32(y+k*r), float32(x+k*r), float32(y+r), float32(x), float32(y+r))
	p.z.CubeTo(float32(x-k*r), float32(y+r), float32(x-r), float32(y+k*r), float32(x-r), float32(y))
	p.z.CubeTo(float32(x-r), float32(y-k*r), float32(x-k*r), float32(y-r), float32(x), float32(y-r))
	p.z.CubeTo(float32(x+k*r), float32(y-r), float32(x+r), float32(y-k*r), float32(x+r), float32(y))
	p.z.ClosePath()
}
func (p fitnessPen) line(a, b fitnessPoint, radius float64) {
	dx, dy := b.x-a.x, b.y-a.y
	length := math.Hypot(dx, dy)
	if length == 0 {
		p.circle(a.x, a.y, radius)
		return
	}
	x, y := -dy/length*radius, dx/length*radius
	p.z.MoveTo(float32(a.x+x), float32(a.y+y))
	p.z.LineTo(float32(a.x-x), float32(a.y-y))
	p.z.LineTo(float32(b.x-x), float32(b.y-y))
	p.z.LineTo(float32(b.x+x), float32(b.y+y))
	p.z.ClosePath()
	p.circle(a.x, a.y, radius)
	p.circle(b.x, b.y, radius)
}
func (p fitnessPen) paint(img *image.RGBA, c color.NRGBA) {
	p.z.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{})
	p.z.Reset(img.Bounds().Dx(), img.Bounds().Dy())
}

func drawFitnessThumbnail(ctx context.Context, segments [][]fitnessPoint, sport string, w, h int) (*image.RGBA, error) {
	if w <= 0 || h <= 0 || w > search.MaxImageSize || h > search.MaxImageSize {
		return nil, errFitnessLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{246, 248, 250, 255}), image.Point{}, draw.Src)
	// A subtle non-geographic grid, not fabricated map roads.
	step := int(math.Max(24, float64(min(w, h))/5))
	for x := step; x < w; x += step {
		for y := 0; y < h; y++ {
			img.SetRGBA(x, y, color.RGBA{235, 239, 243, 255})
		}
	}
	for y := step; y < h; y += step {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{235, 239, 243, 255})
		}
	}
	ps := fitnessProject(segments, w, h)
	pen := fitnessPen{vector.NewRasterizer(w, h)}
	stroke := math.Max(1.4, math.Min(3.2, float64(min(w, h))*.013))
	for pass := 0; pass < 2; pass++ {
		radius := stroke / 2
		c := color.NRGBA{224, 53, 62, 255}
		if pass == 0 {
			radius += 1.5
			c = color.NRGBA{255, 255, 255, 240}
		}
		for _, seg := range ps {
			pen.circle(seg[0].x, seg[0].y, radius)
			last := seg[0]
			for i, point := range seg[1:] {
				if i%128 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				if math.Hypot(point.x-last.x, point.y-last.y) < .6 && i != len(seg)-2 {
					continue
				}
				pen.line(last, point, radius)
				last = point
			}
		}
		pen.paint(img, c)
	}
	if len(ps) > 0 {
		start, end := ps[0][0], ps[len(ps)-1][len(ps[len(ps)-1])-1]
		pen.circle(start.x, start.y, stroke+2)
		pen.circle(end.x, end.y, stroke+2)
		pen.paint(img, color.NRGBA{255, 255, 255, 255})
		pen.circle(start.x, start.y, stroke)
		pen.paint(img, color.NRGBA{44, 140, 103, 255})
		pen.circle(end.x, end.y, stroke)
		pen.paint(img, color.NRGBA{184, 37, 49, 255})
	}
	drawFitnessIcon(img, pen, sport)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return img, nil
}

// The badge uses only local vector geometry, at the bottom left of every card.
func drawFitnessIcon(img *image.RGBA, pen fitnessPen, sport string) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	size := math.Min(46, float64(min(w, h))*.27)
	margin := math.Max(2, float64(min(w, h))*.055)
	cx, cy := margin+size/2, float64(h)-margin-size/2
	pen.circle(cx, cy+1, size/2+1)
	pen.paint(img, color.NRGBA{211, 218, 227, 200})
	pen.circle(cx, cy, size/2)
	pen.paint(img, color.NRGBA{255, 255, 255, 250})
	unit := size / 32
	point := func(x, y float64) fitnessPoint { return fitnessPoint{cx + x*unit, cy + y*unit} }
	line := func(x1, y1, x2, y2 float64) { pen.line(point(x1, y1), point(x2, y2), 1.05*unit) }
	circle := func(x, y, r float64) { p := point(x, y); pen.circle(p.x, p.y, r*unit) }
	switch sport {
	case "ride":
		// Hollow wheels and recognizable bicycle frame.
		for _, x := range []float64{-8, 8} {
			for i := 0; i < 16; i++ {
				a, b := float64(i)*math.Pi/8, float64(i+1)*math.Pi/8
				line(x+5*math.Cos(a), 5+5*math.Sin(a), x+5*math.Cos(b), 5+5*math.Sin(b))
			}
		}
		line(-8, 5, -3, -5)
		line(-3, -5, 3, 5)
		line(3, 5, -8, 5)
		line(-3, -5, 5, -5)
		line(5, -5, 3, 5)
		line(5, -5, 8, 5)
		line(5, -5, 4, -9)
		line(4, -9, 8, -9)
		line(-5, -7, -1, -7)
	case "run", "walk", "hike":
		circle(1, -9, 2.5)
		line(0, -4, -3, 3)
		if sport == "run" {
			line(0, -3, 6, 0)
			line(6, 0, 9, -3)
			line(-1, -2, -7, -3)
			line(-7, -3, -9, 1)
			line(-3, 3, 3, 6)
			line(3, 6, 1, 11)
			line(-3, 3, -8, 8)
			line(-8, 8, -12, 6)
		} else {
			line(0, -3, 6, 2)
			line(-1, -2, -6, 2)
			line(-3, 3, 2, 10)
			line(-3, 3, -8, 10)
		}
		if sport == "hike" {
			line(8, -2, 8, 11)
			line(-5, -4, -7, 0)
			line(-7, 0, -4, 2)
		}
	case "swim":
		circle(7, -5, 2.4)
		line(3, -3, -3, 0)
		line(-3, 0, -8, -5)
		line(-8, -5, -3, -9)
		for y := 4.0; y <= 9; y += 5 {
			line(-11, y, -6, y-2)
			line(-6, y-2, 0, y)
			line(0, y, 6, y-2)
			line(6, y-2, 11, y)
		}
	case "row":
		circle(0, -8, 2.5)
		line(-1, -4, -3, 2)
		line(-3, 2, 4, 4)
		line(-1, -3, 5, 0)
		line(-9, -4, 10, 10)
		line(-10, 5, -7, 9)
		line(-7, 9, 8, 9)
		line(8, 9, 11, 5)
	case "ski":
		circle(2, -9, 2.5)
		line(1, -4, -4, 2)
		line(-4, 2, 3, 5)
		line(3, 5, -1, 9)
		line(0, -3, 7, 0)
		line(7, 0, 9, 8)
		line(-11, 9, 9, 11)
		line(-8, 6, 12, 8)
	case "workout":
		line(-8, 0, 8, 0)
		line(-9, -6, -9, 6)
		line(-6, -4, -6, 4)
		line(9, -6, 9, 6)
		line(6, -4, 6, 4)
	default:
		// Generic fitness pulse; not an invented route.
		line(-11, 0, -6, 0)
		line(-6, 0, -3, -7)
		line(-3, -7, 1, 8)
		line(1, 8, 5, -2)
		line(5, -2, 8, 0)
		line(8, 0, 11, 0)
	}
	pen.paint(img, color.NRGBA{63, 80, 101, 255})
}
