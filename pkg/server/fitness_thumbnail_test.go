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
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go4.org/syncutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/test"
)

// All routes in these tests are synthetic; no user/importer data is accessed.
const fitnessTestGPX = `<gpx xmlns="http://www.topografix.com/GPX/1/1"><trk><trkseg><trkpt lat="40" lon="-100"/><trkpt lat="40.001" lon="-100.001"/><trkpt lat="40.002" lon="-99.999"/></trkseg></trk></gpx>`

func TestFitnessParseGPX(t *testing.T) {
	for _, tc := range []struct {
		name, gpx        string
		segments, points int
		wantErr          bool
	}{
		{"namespace", fitnessTestGPX, 1, 3, false},
		{"prefix", `<p:gpx xmlns:p="http://www.topografix.com/GPX/1/1"><p:trk><p:trkseg><p:trkpt lat="1" lon="2"/></p:trkseg></p:trk></p:gpx>`, 1, 1, false},
		{"empty", `<gpx/>`, 0, 0, false},
		{"empty_segments", `<gpx><trk><trkseg/><trkseg/></trk></gpx>`, 0, 0, false},
		{"multiple_tracks", `<gpx><trk><trkseg><trkpt lat="1" lon="2"/></trkseg></trk><trk><trkseg><trkpt lat="3" lon="4"/></trkseg></trk></gpx>`, 2, 2, false},
		{"bad_coordinates_make_gap", `<gpx><trk><trkseg><trkpt lat="1" lon="2"/><trkpt lat="NaN" lon="2"/><trkpt lat="2" lon="3"/><trkpt lat="91" lon="3"/><trkpt lat="x" lon="3"/><trkpt lat="2" lon="Infinity"/><trkpt lon="3"/><trkpt lat="2" lon="181"/></trkseg></trk></gpx>`, 2, 2, false},
		{"not_gpx", `<html/>`, 0, 0, true},
		{"truncated", `<gpx><trk>`, 0, 0, true},
		{"two_roots", `<gpx/><gpx/>`, 0, 0, true},
		{"broken_attribute", `<gpx><trk><trkseg><trkpt lat="123SECRET></trkpt></trkseg></trk></gpx>`, 0, 0, true},
		{"external_entity", `<!DOCTYPE gpx [<!ENTITY e SYSTEM "http://127.0.0.1/private">]><gpx>&e;</gpx>`, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			segments, err := parseFitnessGPX(context.Background(), strings.NewReader(tc.gpx))
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v", err)
			}
			if err != nil {
				if strings.Contains(err.Error(), "SECRET") {
					t.Fatal("private input in error")
				}
				return
			}
			n := 0
			for _, seg := range segments {
				n += len(seg)
			}
			if len(segments) != tc.segments || n != tc.points {
				t.Fatalf("segments/points = %d/%d, want %d/%d", len(segments), n, tc.segments, tc.points)
			}
		})
	}
}

func TestFitnessGPXLimitsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := parseFitnessGPX(ctx, strings.NewReader(fitnessTestGPX)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	cases := []string{
		`<gpx><!--` + strings.Repeat("x", fitnessMaxGPXBytes) + `--></gpx>`,
		`<gpx><trk><trkseg>` + strings.Repeat(`<trkpt lat="1" lon="2"/>`, fitnessMaxPoints+1) + `</trkseg></trk></gpx>`,
		`<gpx>` + strings.Repeat(`<x>`, 65) + strings.Repeat(`</x>`, 65) + `</gpx>`,
	}
	for i, gpx := range cases {
		if _, err := parseFitnessGPX(context.Background(), strings.NewReader(gpx)); !errors.Is(err, errFitnessLimit) {
			t.Errorf("limit %d = %v", i, err)
		}
	}
}

func TestFitnessProjection(t *testing.T) {
	cases := [][][]fitnessPoint{
		{{{179.99, 1}, {-179.99, 1.01}}},
		{{{1, 90}, {1, -90}}},
		{{{2, 3}}},
		{{{2, 3}, {2, 3}}},
		{{{-180, 0}, {180, 0}}},
	}
	for i, segments := range cases {
		ps := fitnessProject(segments, 300, 180)
		for _, seg := range ps {
			for _, p := range seg {
				if math.IsNaN(p.x) || math.IsNaN(p.y) || math.IsInf(p.x, 0) || math.IsInf(p.y, 0) || p.x < 0 || p.x > 300 || p.y < 0 || p.y > 180 {
					t.Fatalf("case %d projection out of bounds", i)
				}
			}
		}
	}
	// 179.99 -> -179.99 is a tiny antimeridian crossing, not 360 degrees.
	ps := fitnessProject(cases[0], 300, 180)[0]
	if math.Abs(ps[1].y-ps[0].y) < 40 {
		t.Fatal("dateline route was incorrectly flattened")
	}
	// Equal Web Mercator x/y distance must use one scale, not stretch to fill.
	lat := 180 / math.Pi * (2*math.Atan(math.Exp(math.Pi/180)) - math.Pi/2)
	ps = fitnessProject([][]fitnessPoint{{{0, 0}, {1, lat}}}, 300, 180)[0]
	if math.Abs(math.Abs(ps[1].x-ps[0].x)-math.Abs(ps[1].y-ps[0].y)) > 1e-6 {
		t.Fatal("projection aspect ratio distorted")
	}
}

func fitnessRedPixels(img image.Image) int {
	n := 0
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r > 150*257 && g < 110*257 && b < 120*257 {
				n++
			}
		}
	}
	return n
}

func TestFitnessRenderer(t *testing.T) {
	segments, err := parseFitnessGPX(context.Background(), strings.NewReader(fitnessTestGPX))
	if err != nil {
		t.Fatal(err)
	}
	img, err := drawFitnessThumbnail(context.Background(), segments, "run", 320, 180)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 320 || img.Bounds().Dy() != 180 || fitnessRedPixels(img) < 80 {
		t.Fatal("wrong dimensions or no red route")
	}
	icons := map[string][]byte{}
	for _, sport := range []string{"run", "walk", "hike", "ride", "swim", "row", "ski", "workout", "other"} {
		img, err := drawFitnessThumbnail(context.Background(), nil, sport, 160, 160)
		if err != nil {
			t.Fatal(err)
		}
		if fitnessRedPixels(img) != 0 {
			t.Fatal("invented a route for missing GPS")
		}
		for old, pix := range icons {
			if bytes.Equal(pix, img.Pix) {
				t.Fatalf("icons %s and %s identical", old, sport)
			}
		}
		icons[sport] = img.Pix
		// Badge is visibly bottom-left, even without a route.
		r, g, b, _ := img.At(10, 130).RGBA()
		if r < 250*257 || g < 250*257 || b < 250*257 {
			t.Fatal("bottom-left badge missing")
		}
	}
	for _, size := range []image.Point{{1, 1}, {1, 2000}, {2000, 1}, {2000, 2000}} {
		if _, err := drawFitnessThumbnail(context.Background(), [][]fitnessPoint{{{1, 1}}}, "run", size.X, size.Y); err != nil {
			t.Fatalf("degenerate dimensions %v: %v", size, err)
		}
	}
	// A disconnected pair of tracks must not acquire a bridge across the center.
	img, err = drawFitnessThumbnail(context.Background(), [][]fitnessPoint{{{0, 0}, {1, 0}}, {{9, 0}, {10, 0}}}, "ride", 300, 180)
	if err != nil {
		t.Fatal(err)
	}
	crop := img.SubImage(image.Rect(120, 75, 180, 105))
	if fitnessRedPixels(crop) != 0 {
		t.Fatal("connected separate GPX segments")
	}
}

type fitnessCountingFetcher struct {
	src   blob.Fetcher
	calls atomic.Int64
}

func (f *fitnessCountingFetcher) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	f.calls.Add(1)
	return f.src.Fetch(ctx, br)
}

func fitnessTestHandler(t *testing.T, gpx string) (*ImageHandler, *search.DescribedPermanode, *fitnessCountingFetcher) {
	t.Helper()
	sto := new(memory.Storage)
	ref, err := schema.WriteFileFromReader(context.Background(), sto, "synthetic.gpx", strings.NewReader(gpx))
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &fitnessCountingFetcher{src: sto}
	ih := &ImageHandler{Fetcher: fetcher, Cache: new(memory.Storage), ThumbMeta: NewThumbMeta(nil), MaxWidth: 320, MaxHeight: 180, ResizeSem: syncutil.NewSem(128 << 20)}
	dp := &search.DescribedPermanode{Attr: url.Values{nodeattr.Type: {nodeattr.FitnessActivity}, nodeattr.FitnessTrack: {ref.String()}, nodeattr.FitnessActivityType: {"run"}}}
	return ih, dp, fetcher
}

func fitnessRequest(ih *ImageHandler, dp *search.DescribedPermanode, method, query, validator string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/ui/thumbnail/unused/track.png"+query, nil)
	if validator != "" {
		req.Header.Set("If-None-Match", validator)
	}
	rec := httptest.NewRecorder()
	ih.serveFitnessThumbnail(rec, req, blob.Ref{}, dp)
	return rec
}

func TestFitnessHTTPAndCache(t *testing.T) {
	ih, dp, fetcher := fitnessTestHandler(t, fitnessTestGPX)
	first := fitnessRequest(ih, dp, "GET", "?fitnessTrack=http://127.0.0.1/private&fitnessActivityType=ride&ftv=999", "")
	if first.Code != 200 || first.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("response = %d, %s", first.Code, first.Body.String())
	}
	img, err := png.Decode(bytes.NewReader(first.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds() != image.Rect(0, 0, 320, 180) || fitnessRedPixels(img) < 80 {
		t.Fatal("invalid PNG")
	}
	etag := first.Header().Get("ETag")
	if etag == "" || first.Header().Get("Cache-Control") != "private, max-age=0, must-revalidate" || first.Header().Get("Last-Modified") != "" || first.Header().Get("Expires") != "" {
		t.Fatal("mutable PN has unsafe validators")
	}
	calls := fetcher.calls.Load()
	second := fitnessRequest(ih, dp, "GET", "", "")
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) || calls != fetcher.calls.Load() {
		t.Fatal("thumbnail did not hit derived cache")
	}
	for _, validator := range []string{etag, "W/" + etag, `"not-current", W/` + etag, "*"} {
		rec := fitnessRequest(ih, dp, "GET", "", validator)
		if rec.Code != 304 || rec.Body.Len() != 0 || rec.Header().Get("ETag") != etag {
			t.Fatalf("conditional GET = %d", rec.Code)
		}
	}
	head := fitnessRequest(ih, dp, "HEAD", "", "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != fmt.Sprint(first.Body.Len()) {
		t.Fatal("HEAD headers/body invalid")
	}
	// Modifying actual PN attrs invalidates old validators, irrespective of URL.
	dp.Attr.Set(nodeattr.FitnessActivityType, "ride")
	changed := fitnessRequest(ih, dp, "GET", "?fitnessActivityType=run", etag)
	if changed.Code != 200 || changed.Header().Get("ETag") == etag || bytes.Equal(changed.Body.Bytes(), first.Body.Bytes()) {
		t.Fatal("sport change did not invalidate thumbnail")
	}
	dp.Attr.Del(nodeattr.FitnessTrack)
	missing := fitnessRequest(ih, dp, "GET", "", changed.Header().Get("ETag"))
	if missing.Code != 200 || missing.Header().Get("ETag") == changed.Header().Get("ETag") {
		t.Fatal("track change did not invalidate thumbnail")
	}
	img, err = png.Decode(bytes.NewReader(missing.Body.Bytes()))
	if err != nil || fitnessRedPixels(img) != 0 {
		t.Fatal("missing track did not render neutral card")
	}
	ih.MaxWidth = 321
	if rec := fitnessRequest(ih, dp, "GET", "", missing.Header().Get("ETag")); rec.Code != 200 || rec.Header().Get("ETag") == missing.Header().Get("ETag") {
		t.Fatal("dimensions not part of ETag")
	}
	if rec := fitnessRequest(ih, dp, "POST", "", ""); rec.Code != 400 {
		t.Fatal("POST was accepted")
	}
	for _, w := range []int{-1, 0, 2001} {
		ih.MaxWidth = w
		if rec := fitnessRequest(ih, dp, "GET", "", ""); rec.Code != 400 {
			t.Fatal("invalid dimensions accepted")
		}
	}
}

func TestFitnessMalformedHTTPSanitized(t *testing.T) {
	ih, dp, _ := fitnessTestHandler(t, `<gpx><trk><SECRET_PRIVATE_COORDINATES`)
	rec := fitnessRequest(ih, dp, "GET", "", "")
	if rec.Code != 422 || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" || strings.Contains(rec.Body.String(), "SECRET") || rec.Header().Get("ETag") != "" || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("malformed track error not sanitized")
	}
}

func TestFitnessSemaphoreCancellation(t *testing.T) {
	ih := &ImageHandler{ResizeSem: syncutil.NewSem(1)}
	if err := ih.ResizeSem.Acquire(1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := ih.acquireFitnessMemory(ctx, 1); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled semaphore wait blocked")
	}
	ih.ResizeSem.Release(1)
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	release, err := ih.acquireFitnessMemory(ctx2, 1)
	if err != nil {
		t.Fatal("leaked semaphore permit", err)
	}
	release()
	if _, err := ih.acquireFitnessMemory(context.Background(), 2); err == nil {
		t.Fatal("oversize memory acquisition accepted")
	}
}

type fitnessNoNetwork struct{ calls *atomic.Int64 }

func (n fitnessNoNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	n.calls.Add(1)
	return nil, errors.New("network forbidden")
}

func TestFitnessImageHandlerIntegrationNoNetwork(t *testing.T) {
	var calls atomic.Int64
	old := http.DefaultTransport
	http.DefaultTransport = fitnessNoNetwork{&calls}
	defer func() { http.DefaultTransport = old }()
	idx := index.NewMemoryIndex()
	idx.Logger = test.NewLogger(t, "")
	id := indextest.NewIndexDeps(idx)
	id.Fataler = t
	defer id.Index.Close()
	h := search.NewHandler(id.Index, index.NewOwner(indextest.KeyID, id.SignerBlobRef))
	pn := id.NewPermanode()
	id.SetAttribute(pn, nodeattr.Type, nodeattr.FitnessActivity)
	id.SetAttribute(pn, nodeattr.FitnessActivityType, "swim")
	track, _ := id.UploadFile("synthetic.gpx", fitnessTestGPX, time.Time{})
	id.SetAttribute(pn, nodeattr.FitnessTrack, track.String())
	ih := &ImageHandler{Search: h, Fetcher: id.BlobSource, MaxWidth: 200, MaxHeight: 120}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ui/thumbnail/unused/track.png?fitnessTrack=http://localhost/private", nil)
	ih.ServeHTTP(rec, req, pn)
	if rec.Code != 200 {
		t.Fatalf("fitness PN integration = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := png.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
		t.Fatal(err)
	}
	nonFitness := id.NewPermanode()
	rec = httptest.NewRecorder()
	ih.ServeHTTP(rec, req, nonFitness)
	if rec.Code != 404 {
		t.Fatalf("non-image PN = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	ih.ServeHTTP(rec, req, blob.RefFromString("not-in-index"))
	if rec.Code != 404 {
		t.Fatalf("missing metadata = %d", rec.Code)
	}
	ih.Search = nil
	rec = httptest.NewRecorder()
	ih.ServeHTTP(rec, req, pn)
	if rec.Code != 500 {
		t.Fatalf("nil Search = %d", rec.Code)
	}
	if calls.Load() != 0 {
		t.Fatal("renderer accessed the network")
	}
}

type fitnessCacheCountReader struct {
	r io.Reader
	n *int64
}

func (r fitnessCacheCountReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	*r.n += int64(n)
	return n, err
}

type fitnessBadCache struct {
	*memory.Storage
	ref  blob.Ref
	r    io.Reader
	size uint32
	read int64
}

func (c *fitnessBadCache) Fetch(ctx context.Context, ref blob.Ref) (io.ReadCloser, uint32, error) {
	if ref == c.ref {
		return io.NopCloser(fitnessCacheCountReader{c.r, &c.read}), c.size, nil
	}
	return c.Storage.Fetch(ctx, ref)
}

type fitnessZeros struct{}

func (fitnessZeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestFitnessCacheBoundsAndCorruption(t *testing.T) {
	const limit = int64(320*180*4 + (1 << 20))
	var smallPNG bytes.Buffer
	if err := png.Encode(&smallPNG, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		size    uint32
		reader  func() io.Reader
		maxRead int64
	}{
		{"advertised_oversize", uint32(limit + 1), func() io.Reader { return fitnessZeros{} }, 0},
		{"dishonest_size", 8, func() io.Reader { return io.MultiReader(strings.NewReader("\x89PNG\r\n\x1a\n"), fitnessZeros{}) }, limit + 1},
		{"PNG_prefix_only", 8, func() io.Reader { return strings.NewReader("\x89PNG\r\n\x1a\n") }, 8},
		{"wrong_dimensions", uint32(smallPNG.Len()), func() io.Reader { return bytes.NewReader(smallPNG.Bytes()) }, int64(smallPNG.Len())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ih, dp, fetcher := fitnessTestHandler(t, fitnessTestGPX)
			cache := &fitnessBadCache{Storage: new(memory.Storage), ref: blob.RefFromString("corrupt thumbnail"), r: tc.reader(), size: tc.size}
			ih.Cache = cache
			if err := ih.ThumbMeta.Put(fitnessCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 320, 180), cache.ref); err != nil {
				t.Fatal(err)
			}
			rec := fitnessRequest(ih, dp, "GET", "", "")
			if rec.Code != 200 {
				t.Fatalf("cache miss failed: %d", rec.Code)
			}
			cfg, err := png.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
			if err != nil || cfg.Width != 320 || cfg.Height != 180 {
				t.Fatal("corrupt cache entry served")
			}
			if cache.read > tc.maxRead {
				t.Fatalf("cache read %d bytes, bound %d", cache.read, tc.maxRead)
			}
			if fetcher.calls.Load() == 0 {
				t.Fatal("corrupt entry was not rerendered")
			}
		})
	}
}

func TestFitnessMissingBlobNeutral(t *testing.T) {
	ih, dp, _ := fitnessTestHandler(t, fitnessTestGPX)
	dp.Attr.Set(nodeattr.FitnessTrack, blob.RefFromString("missing synthetic file").String())
	rec := fitnessRequest(ih, dp, "GET", "", "")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("ETag") != "" {
		t.Fatalf("missing blob response = %d", rec.Code)
	}
	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil || fitnessRedPixels(img) != 0 {
		t.Fatal("missing file invented a route")
	}
	if _, err := ih.ThumbMeta.Get(fitnessCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 320, 180)); !errors.Is(err, errCacheMiss) {
		t.Fatal("missing-file fallback was cached")
	}
}

func TestFitnessRenderOnceCancellation(t *testing.T) {
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_, _ = fitnessRenderOnce(context.Background(), "test:blocked-flight", func() ([]byte, error) { close(started); <-release; return []byte("png"), nil })
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := fitnessRenderOnce(ctx, "test:blocked-flight", func() ([]byte, error) { t.Error("duplicate renderer called"); return nil, nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("singleflight waiter did not cancel", err)
	}
	close(release)
	<-done
}

type fitnessContextKey struct{}
type fitnessContextFetcher struct {
	blob.Fetcher
	bad   atomic.Bool
	calls atomic.Int64
}

func (f *fitnessContextFetcher) Fetch(ctx context.Context, ref blob.Ref) (io.ReadCloser, uint32, error) {
	f.calls.Add(1)
	if ctx.Value(fitnessContextKey{}) != true {
		f.bad.Store(true)
	}
	return f.Fetcher.Fetch(ctx, ref)
}
func TestFitnessContextPropagation(t *testing.T) {
	ih, dp, _ := fitnessTestHandler(t, fitnessTestGPX)
	f := &fitnessContextFetcher{Fetcher: ih.Fetcher}
	ih.Fetcher = f
	ctx := context.WithValue(context.Background(), fitnessContextKey{}, true)
	req := httptest.NewRequest("GET", "/track.png", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	ih.serveFitnessThumbnail(rec, req, blob.Ref{}, dp)
	if rec.Code != 200 || f.bad.Load() || f.calls.Load() < 2 {
		t.Fatal("file schema/content fetch did not use request context")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	before := f.calls.Load()
	rec = httptest.NewRecorder()
	ih.serveFitnessThumbnail(rec, req.WithContext(ctx), blob.Ref{}, dp)
	if rec.Body.Len() != 0 || before != f.calls.Load() {
		t.Fatal("canceled request rendered or fetched data")
	}
}
