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
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/image/font"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema/nodeattr"
)

// Every map, track, name and coordinate here is synthetic. Tile HTTP uses fake
// transports or httptest localhost servers, never OSM or owner data.
type fitnessBasemapClock struct{ nanos atomic.Int64 }

func (c *fitnessBasemapClock) now() time.Time          { return time.Unix(0, c.nanos.Load()) }
func (c *fitnessBasemapClock) advance(d time.Duration) { c.nanos.Add(int64(d)) }

func fitnessBasemapEnvSource(t *testing.T, handler http.HandlerFunc) (*fitnessTileSource, *fitnessBasemapClock) {
	t.Helper()
	clock := new(fitnessBasemapClock)
	clock.nanos.Store(time.Now().UnixNano())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Keep HTTP Date consistent with the injectable freshness clock.
		w.Header().Set("Date", clock.now().UTC().Format(http.TimeFormat))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("PERKEEP_FITNESS_TILE_URL", srv.URL+"/{z}/{x}/{y}.png")
	t.Setenv("PERKEEP_FITNESS_TILE_CACHE_DIR", t.TempDir())
	t.Setenv("PERKEEP_FITNESS_TILE_USER_AGENT", "Perkeep synthetic map tests")
	source, err := fitnessTileSourceFromEnv(blob.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	// Isolate provider cooldowns without changing the global production limiter.
	source.limiter = &fitnessTileLimiter{slots: make(chan struct{}, 16)}
	source.now = clock.now
	return source, clock
}

func fitnessBasemapCoords(t *testing.T, r *http.Request) (z, x, y int) {
	t.Helper()
	if _, err := fmt.Sscanf(r.URL.Path, "/%d/%d/%d.png", &z, &x, &y); err != nil {
		t.Errorf("bad tile path %q", r.URL.Path)
	}
	n := 1 << z
	if r.Method != "GET" || z < 0 || z > 18 || x < 0 || x >= n || y < 0 || y >= n {
		t.Errorf("invalid tile coordinates/method: %s %d/%d/%d", r.Method, z, x, y)
	}
	return
}

func fitnessBasemapTilePNG(t *testing.T, z, tx, ty int, version uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), uint8(tx*41+ty*17+z) + version, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFitnessBasemapDefaultReplacesRouteOnlyCache(t *testing.T) {
	t.Setenv("PERKEEP_FITNESS_TILE_URL", "off")
	t.Setenv("PERKEEP_FITNESS_TILE_CACHE_DIR", t.TempDir())
	t.Setenv("PERKEEP_FITNESS_TILE_USER_AGENT", "Perkeep synthetic default map test")
	ih, dp, _ := fitnessTestHandler(t, fitnessTestGPX)
	routeOnly := fitnessRequest(ih, dp, "GET", "", "")
	if routeOnly.Code != 200 || routeOnly.Header().Get("ETag") == "" {
		t.Fatal("route-only preview failed")
	}
	track := dp.Attr.Get(nodeattr.FitnessTrack)
	if _, err := ih.ThumbMeta.Get(fitnessCacheKey(track, "run", 320, 180)); err != nil {
		t.Fatal("route-only preview was not cached", err)
	}

	t.Setenv("PERKEEP_FITNESS_TILE_URL", "")
	source, err := fitnessTileSourceFromEnv(blob.Ref{})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the default endpoint without contacting the public service.
	var calls atomic.Int64
	source.client.Transport = fitnessTileTestTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.Scheme != "https" || r.URL.Host != "tile.openstreetmap.org" {
			t.Errorf("default tile endpoint = %s", r.URL)
		}
		z, x, y := fitnessBasemapCoords(t, r)
		return fitnessTileTestResponse(200, http.Header{"Cache-Control": {"max-age=3600"}}, fitnessBasemapTilePNG(t, z, x, y, 0)), nil
	})
	source.limiter = &fitnessTileLimiter{slots: make(chan struct{}, 16)}

	// Neither the old server-side PNG nor its browser validator may hide maps.
	mapped := fitnessRequest(ih, dp, "GET", "", routeOnly.Header().Get("ETag"))
	if mapped.Code != 200 || mapped.Header().Get("ETag") == "" || mapped.Header().Get("ETag") == routeOnly.Header().Get("ETag") || bytes.Equal(mapped.Body.Bytes(), routeOnly.Body.Bytes()) {
		t.Fatal("default map did not replace cached route-only preview")
	}
	if calls.Load() == 0 {
		t.Fatal("default map did not request tiles")
	}
	firstCalls := calls.Load()
	cached := fitnessRequest(ih, dp, "GET", "", "")
	if cached.Code != 200 || !bytes.Equal(cached.Body.Bytes(), mapped.Body.Bytes()) || calls.Load() != firstCalls {
		t.Fatal("default map did not reuse its fresh composite cache")
	}
	conditional := fitnessRequest(ih, dp, "GET", "", mapped.Header().Get("ETag"))
	if conditional.Code != 304 || calls.Load() != firstCalls {
		t.Fatal("default map did not reuse its browser validator")
	}
}

func TestFitnessBasemapProjectionAndViewport(t *testing.T) {
	cases := []struct {
		name     string
		segments [][]fitnessPoint
		w, h     int
	}{
		{"local", [][]fitnessPoint{{{-100, 40}, {-100.001, 40.001}, {-99.999, 40.002}}}, 320, 180},
		{"dateline", [][]fitnessPoint{{{179.999, 1}, {-179.999, 1.003}}}, 320, 180},
		{"north_pole", [][]fitnessPoint{{{12, 90}, {12.001, 90}}}, 320, 180},
		{"south_pole", [][]fitnessPoint{{{12, -90}}}, 320, 180},
		{"stationary", [][]fitnessPoint{{{12, 40}, {12, 40}}}, 320, 180},
		{"zoom_reduction", [][]fitnessPoint{{{12, 40}}}, 2000, 2000},
		{"world", [][]fitnessPoint{{{-170, 80}, {-90, 20}, {0, -80}, {90, 20}, {170, 80}}}, 2000, 2000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			requested := map[[3]int]bool{}
			source, _ := fitnessBasemapEnvSource(t, func(w http.ResponseWriter, r *http.Request) {
				z, x, y := fitnessBasemapCoords(t, r)
				mu.Lock()
				requested[[3]int{z, x, y}] = true
				mu.Unlock()
				w.Header().Set("Cache-Control", "max-age=3600")
				w.Write(fitnessBasemapTilePNG(t, z, x, y, 0))
			})
			plan := fitnessProjectionFor(tc.segments, tc.w, tc.h)
			vp := fitnessBasemapViewport(plan)
			count := (vp.maxX - vp.minX + 1) * (vp.maxY - vp.minY + 1)
			if count < 1 || count > 16 {
				t.Fatalf("viewport count = %d", count)
			}
			if tc.name == "stationary" && (vp.zoom < 16 || vp.zoom > 17) {
				t.Fatalf("stationary zoom = %d", vp.zoom)
			}
			if tc.name == "zoom_reduction" && vp.zoom >= 16 {
				t.Fatal("large canvas did not reduce zoom")
			}
			if tc.name == "dateline" && vp.maxX < 1<<vp.zoom {
				t.Fatal("dateline projection lost unwrapped x")
			}
			img, expires, cacheable, err := fitnessDrawBasemap(context.Background(), plan, source, "")
			if err != nil || !cacheable || !expires.After(source.now()) {
				t.Fatalf("map draw = %v", err)
			}
			// Independent canonical Web Mercator pixel formula, including subpixel
			// viewport origins. Compare many pixels, not just tile centers.
			for py := 3; py < tc.h; py += max(1, tc.h/17) {
				for px := 3; px < tc.w; px += max(1, tc.w/19) {
					mx := plan.centreMercatorX + (float64(px)+.5-float64(tc.w)/2)/plan.scalePixelsPerRad
					my := plan.centreMercatorY + (float64(py)+.5-float64(tc.h)/2)/plan.scalePixelsPerRad
					if my < -math.Pi || my >= math.Pi {
						continue
					}
					wx := int(math.Floor((mx + math.Pi) / (2 * math.Pi) * vp.worldPixels))
					wy := int(math.Floor((my + math.Pi) / (2 * math.Pi) * vp.worldPixels))
					world := int(vp.worldPixels)
					wx = ((wx % world) + world) % world
					want := color.NRGBA{uint8(wx % 256), uint8(wy % 256), uint8((wx/256)*41 + (wy/256)*17 + vp.zoom), 255}
					if got := color.NRGBAModel.Convert(img.At(px, py)).(color.NRGBA); got != want {
						t.Fatalf("map/route transform at %d,%d = %v, want %v", px, py, got, want)
					}
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requested) > 16 {
				t.Fatal("unbounded tile requests")
			}
			expected := map[[3]int]bool{}
			for y := vp.minY; y <= vp.maxY; y++ {
				for x := vp.minX; x <= vp.maxX; x++ {
					n := 1 << vp.zoom
					expected[[3]int{vp.zoom, ((x % n) + n) % n, y}] = true
				}
			}
			if len(requested) != len(expected) {
				t.Fatalf("requests %d, exact viewport %d", len(requested), len(expected))
			}
			for coord := range requested {
				if !expected[coord] {
					t.Fatal("prefetched outside viewport")
				}
			}
		})
	}
}

func fitnessBasemapTestSixteenTilePlan() fitnessProjection {
	return fitnessProjection{
		width: 1024, height: 1024,
		scalePixelsPerRad: math.Ldexp(256, 8) / (2 * math.Pi),
	}
}

func TestFitnessBasemapConcurrentTiles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		plan := fitnessBasemapTestSixteenTilePlan()
		vp := fitnessBasemapViewport(plan)
		if (vp.maxX-vp.minX+1)*(vp.maxY-vp.minY+1) != 16 {
			t.Fatalf("test viewport is not sixteen tiles: %+v", vp)
		}
		data := fitnessTileTestPNG(t, 256, 256)
		started, release := make(chan image.Point, 16), make(chan struct{})
		source, _ := fitnessTileTestSource(t, func(req *http.Request) (*http.Response, error) {
			_, x, y := fitnessBasemapCoords(t, req)
			started <- image.Pt(x, y)
			select {
			case <-release:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			policy := fmt.Sprintf("max-age=%d", 10+10*(x-vp.minX))
			if x == vp.minX && y == vp.minY {
				policy += ", no-cache"
			}
			return fitnessTileTestResponse(200, http.Header{"Cache-Control": {policy}}, data), nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		type mapResult struct {
			img       *image.RGBA
			expires   time.Time
			cacheable bool
			err       error
		}
		done := make(chan mapResult, 1)
		start := time.Now()
		go func() {
			img, expires, cacheable, err := fitnessDrawBasemap(ctx, plan, source, "")
			done <- mapResult{img, expires, cacheable, err}
		}()
		synctest.Wait()
		if len(started) != 16 || !time.Now().Equal(start) {
			t.Fatalf("thumbnail started %d tiles after %v; want sixteen without spacing", len(started), time.Since(start))
		}
		requested := make(map[image.Point]bool)
		for range 16 {
			coord := <-started
			if requested[coord] || coord.X < vp.minX || coord.X > vp.maxX || coord.Y < vp.minY || coord.Y > vp.maxY {
				t.Fatalf("duplicate or prefetched tile: %v", coord)
			}
			requested[coord] = true
		}
		if len(done) != 0 {
			t.Fatal("thumbnail completed before tiles arrived")
		}
		close(release)
		got := <-done
		if got.err != nil || got.img == nil || got.img.Bounds() != image.Rect(0, 0, plan.width, plan.height) {
			t.Fatalf("concurrent render = %+v", got)
		}
		if got.cacheable || !got.expires.Equal(source.now()) {
			t.Fatalf("aggregated cacheability/expiry = %v, %v", got.cacheable, got.expires)
		}
		// All fresh tiles are reused; only the no-cache tile is revalidated.
		if _, _, cacheable, err := fitnessDrawBasemap(ctx, plan, source, ""); err != nil || cacheable {
			t.Fatalf("second render = cacheable %v, error %v", cacheable, err)
		}
		if len(started) != 1 || <-started != image.Pt(vp.minX, vp.minY) {
			t.Fatal("concurrent fetch lost raw-tile cache semantics")
		}
	})
}

func TestFitnessBasemapConcurrentThumbnailsGlobalLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := fitnessTileTestPNG(t, 256, 256)
		var active, peak, calls atomic.Int32
		release := make(chan struct{})
		// A bubble-local channel makes queued acquisition durably block for
		// synctest.Wait; check production wiring before injecting it.
		limiter := &fitnessTileLimiter{slots: make(chan struct{}, 16)}
		transport := fitnessTileTestTransport(func(req *http.Request) (*http.Response, error) {
			calls.Add(1)
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			select {
			case <-release:
				return fitnessTileTestResponse(200, nil, data), nil
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 2)
		for i := range 2 {
			source, err := newFitnessTileSource(fmt.Sprintf("https://maps%d.invalid/{z}/{x}/{y}.png", i), t.TempDir(), "", &http.Client{Transport: transport}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if source.limiter != fitnessTilesLimiter || cap(source.limiter.slots) != 16 {
				t.Fatal("thumbnail provider does not use the global sixteen-request limiter")
			}
			source.limiter = limiter
			go func() {
				_, _, _, err := fitnessDrawBasemap(ctx, fitnessBasemapTestSixteenTilePlan(), source, "")
				done <- err
			}()
		}
		synctest.Wait()
		if calls.Load() != 16 || active.Load() != 16 {
			t.Fatalf("concurrent thumbnails started %d requests with %d active, want sixteen", calls.Load(), active.Load())
		}
		close(release)
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if calls.Load() != 32 || peak.Load() != 16 || len(limiter.slots) != 0 {
			t.Fatalf("total=%d peak=%d outstanding=%d, want 32/16/0", calls.Load(), peak.Load(), len(limiter.slots))
		}
	})
}

func TestFitnessBasemapConcurrentFailureCancellation(t *testing.T) {
	for _, mode := range []string{"upstream", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls, canceled atomic.Int32
				fail := make(chan struct{})
				source, _ := fitnessTileTestSource(t, func(req *http.Request) (*http.Response, error) {
					if calls.Add(1) == 1 {
						select {
						case <-fail:
							return fitnessTileTestResponse(404, nil, nil), nil
						case <-req.Context().Done():
						}
					} else {
						<-req.Context().Done()
					}
					canceled.Add(1)
					return nil, req.Context().Err()
				})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() {
					img, _, cacheable, err := fitnessDrawBasemap(ctx, fitnessBasemapTestSixteenTilePlan(), source, "")
					if img != nil || cacheable {
						t.Error("failed render returned a partial/cacheable basemap")
					}
					done <- err
				}()
				synctest.Wait()
				if calls.Load() != 16 {
					t.Fatalf("started %d requests, want sixteen", calls.Load())
				}
				wantErr, wantCanceled := error(errFitnessTileUpstream), int32(15)
				if mode == "canceled" {
					cancel()
					wantErr, wantCanceled = context.Canceled, 16
				} else {
					close(fail)
				}
				if err := <-done; !errors.Is(err, wantErr) {
					t.Fatalf("failed render = %v, want %v", err, wantErr)
				}
				synctest.Wait()
				source.mu.Lock()
				flights := len(source.flights)
				source.mu.Unlock()
				if canceled.Load() != wantCanceled || len(source.limiter.slots) != 0 || flights != 0 {
					t.Fatalf("canceled=%d permits=%d flights=%d; want %d/0/0", canceled.Load(), len(source.limiter.slots), flights, wantCanceled)
				}
			})
		})
	}
}

func TestFitnessBasemapConcurrentWrappedTileDeduplication(t *testing.T) {
	data := fitnessTileTestPNG(t, 256, 256)
	var calls atomic.Int32
	source, _ := fitnessTileTestSource(t, func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.URL.Path != "/0/0/0.png" {
			t.Errorf("wrapped world tile = %q", req.URL.Path)
		}
		return fitnessTileTestResponse(200, nil, data), nil
	})
	plan := fitnessProjection{width: 1024, height: 256, scalePixelsPerRad: 256 / (2 * math.Pi)}
	vp := fitnessBasemapViewport(plan)
	if vp.maxX-vp.minX < 2 || vp.zoom != 0 {
		t.Fatalf("test viewport does not repeat a wrapped tile: %+v", vp)
	}
	if _, _, _, err := fitnessDrawBasemap(context.Background(), plan, source, ""); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("wrapped viewport fetched %d tiles, want one", calls.Load())
	}
}

func TestFitnessBasemapHTTPFreshnessAndContentETag(t *testing.T) {
	var calls, conditional atomic.Int64
	var version atomic.Int32
	version.Store(1)
	source, clock := fitnessBasemapEnvSource(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		z, x, y := fitnessBasemapCoords(t, r)
		current := fmt.Sprintf("\"synthetic-%d\"", version.Load())
		ttl := 120
		if x%2 == 0 {
			ttl = 30
		}
		w.Header().Set("Cache-Control", fmt.Sprintf("max-age=%d", ttl))
		w.Header().Set("ETag", current)
		if r.Header.Get("If-None-Match") == current {
			conditional.Add(1)
			w.WriteHeader(304)
			return
		}
		w.Write(fitnessBasemapTilePNG(t, z, x, y, uint8(version.Load())))
	})
	ih, dp, fetcher := fitnessTestHandler(t, fitnessTestGPX)
	key := fitnessMapCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 320, 180, source.profile())
	// No cache means HEAD cannot trigger GPX reads or tile HTTP, including '*'.
	head := fitnessRequest(ih, dp, "HEAD", "", "*")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("ETag") != "" || head.Header().Get("Cache-Control") != "private, no-store" || calls.Load() != 0 || fetcher.calls.Load() != 0 {
		t.Fatal("HEAD miss rendered, fetched, or claimed a validator")
	}
	first := fitnessRequest(ih, dp, "GET", "?fitnessTrack=SECRET&token=SECRET", "")
	if first.Code != 200 || first.Header().Get("ETag") == "" {
		t.Fatalf("first map = %d: %s", first.Code, first.Body.String())
	}
	if first.Header().Get("Cache-Control") != "private, max-age=0, must-revalidate" {
		t.Fatal("map response cache policy")
	}
	oldETag := first.Header().Get("ETag")
	pngImage, err := png.Decode(bytes.NewReader(first.Body.Bytes()))
	if err != nil || fitnessRedPixels(pngImage) < 80 {
		t.Fatal("composite lacks red track")
	}
	cached, expires, ok := source.cachedThumbnail(key)
	if !ok || !bytes.Equal(cached, first.Body.Bytes()) {
		t.Fatal("fresh filesystem composite missing")
	}
	if expires.After(clock.now().Add(30 * time.Second)) {
		t.Fatal("composite outlives earliest tile")
	}
	if _, err := ih.ThumbMeta.Get(fitnessCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 320, 180)); !errors.Is(err, errCacheMiss) {
		t.Fatal("map stored in eternal ThumbMeta")
	}
	firstCalls := calls.Load()
	for _, method := range []string{"GET", "HEAD"} {
		for _, validator := range []string{oldETag, "W/" + oldETag, `"other", W/` + oldETag, "*"} {
			rec := fitnessRequest(ih, dp, method, "", validator)
			if rec.Code != 304 || rec.Body.Len() != 0 || rec.Header().Get("ETag") != oldETag {
				t.Fatalf("fresh %s conditional = %d", method, rec.Code)
			}
		}
	}
	if calls.Load() != firstCalls {
		t.Fatal("fresh composite fetched upstream")
	}
	// Expired composites cannot answer HEAD 304 or initiate tile revalidation.
	clock.advance(31 * time.Second)
	head = fitnessRequest(ih, dp, "HEAD", "", oldETag)
	if head.Code != 200 || head.Header().Get("ETag") != "" || calls.Load() != firstCalls {
		t.Fatal("HEAD stale composite fetched or returned 304")
	}
	same := fitnessRequest(ih, dp, "GET", "", oldETag)
	if same.Code != 304 || calls.Load() == firstCalls || conditional.Load() == 0 {
		t.Fatal("expired composite was not revalidated before 304")
	}
	// Once all tiles expire and their content changes, same GPX/dimensions must
	// produce a new PNG hash and 200, not the old input-key 304.
	clock.advance(121 * time.Second)
	version.Store(2)
	changed := fitnessRequest(ih, dp, "GET", "", oldETag)
	if changed.Code != 200 || changed.Header().Get("ETag") == oldETag || bytes.Equal(changed.Body.Bytes(), first.Body.Bytes()) {
		t.Fatal("map content change did not invalidate ETag")
	}
	// Bad dimensions in a fresh composite cannot masquerade as the requested PNG.
	var tiny bytes.Buffer
	png.Encode(&tiny, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	if err := source.storeThumbnail(key, tiny.Bytes(), clock.now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rec := fitnessRequest(ih, dp, "GET", "", "")
	cfg, err := png.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
	if err != nil || cfg.Width != 320 || cfg.Height != 180 {
		t.Fatal("wrong-size composite served")
	}
}

func TestFitnessBasemapPrivacyAndNoGPS(t *testing.T) {
	var calls atomic.Int64
	source, _ := fitnessBasemapEnvSource(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		z, x, y := fitnessBasemapCoords(t, r)
		if r.Header.Get("Referer") != "https://synthetic-ui.example/" {
			t.Errorf("private referer escaped: %q", r.Header.Get("Referer"))
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" {
			t.Error("private credentials/query escaped")
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "Perkeep") {
			t.Error("tile request unidentified")
		}
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write(fitnessBasemapTilePNG(t, z, x, y, 0))
	})
	ih, dp, _ := fitnessTestHandler(t, fitnessTestGPX)
	req := httptest.NewRequest("GET", "/ui/thumbnail/PRIVATE.png?token=SECRET", nil)
	req.Header.Set("Referer", "https://synthetic-ui.example/private-route?name=PRIVATE&token=SECRET#fragment")
	req.Header.Set("Authorization", "Bearer SECRET")
	req.Header.Set("Cookie", "private=SECRET")
	rec := httptest.NewRecorder()
	ih.serveFitnessThumbnail(rec, req, blob.Ref{}, dp)
	if rec.Code != 200 || calls.Load() == 0 {
		t.Fatal("synthetic map did not render")
	}
	for _, raw := range []string{"https://user:SECRET@synthetic-ui.example/private", "file:///private", "/private", "http://", ":invalid"} {
		if fitnessMapReferer(raw) != "" {
			t.Fatal("invalid referer accepted")
		}
	}
	if fitnessMapReferer("") != "" {
		t.Fatal("invented referer for native client")
	}
	before := calls.Load()
	ih, dp, _ = fitnessTestHandler(t, `<gpx><trk><name>PRIVATE</name><trkseg/></trk></gpx>`)
	noGPS := fitnessRequest(ih, dp, "GET", "", "")
	img, err := png.Decode(bytes.NewReader(noGPS.Body.Bytes()))
	if noGPS.Code != 200 || err != nil || fitnessRedPixels(img) != 0 || calls.Load() != before {
		t.Fatal("GPS-less activity fetched tiles or invented route")
	}
	routeOnly, err := drawFitnessThumbnail(context.Background(), nil, "run", 320, 180)
	if err != nil {
		t.Fatal(err)
	}
	var neutral bytes.Buffer
	png.Encode(&neutral, routeOnly)
	if !bytes.Equal(noGPS.Body.Bytes(), neutral.Bytes()) {
		t.Fatal("GPS-less card has map attribution or changed appearance")
	}
	key := fitnessMapCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 320, 180, source.profile())
	if _, _, ok := source.cachedThumbnail(key); !ok {
		t.Fatal("neutral card not cached")
	}
}

func TestFitnessBasemapFallbackRecoveryAndUncacheable(t *testing.T) {
	for _, mode := range []string{"404", "429", "timeout", "no-store", "no-cache"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int64
			var recovered atomic.Bool
			source, _ := fitnessBasemapEnvSource(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				z, x, y := fitnessBasemapCoords(t, r)
				if !recovered.Load() {
					switch mode {
					case "404":
						w.WriteHeader(404)
						return
					case "429":
						w.Header().Set("Retry-After", "1")
						w.WriteHeader(429)
						return
					case "timeout":
						<-r.Context().Done()
						return
					case "no-store", "no-cache":
						w.Header().Set("Cache-Control", mode)
					}
				} else {
					w.Header().Set("Cache-Control", "max-age=3600")
				}
				w.Write(fitnessBasemapTilePNG(t, z, x, y, 0))
			})
			if mode == "timeout" {
				source.client.Timeout = 20 * time.Millisecond
			}
			ih, dp, _ := fitnessTestHandler(t, fitnessTestGPX)
			rec := fitnessRequest(ih, dp, "GET", "", "*")
			cacheKey := fitnessMapCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 320, 180, source.profile())
			if _, _, ok := source.cachedThumbnail(cacheKey); ok {
				t.Fatal("fallback/uncacheable composite stored")
			}
			if _, err := ih.ThumbMeta.Get(fitnessCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 320, 180)); !errors.Is(err, errCacheMiss) {
				t.Fatal("fallback cached eternally")
			}
			if rec.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("fallback/uncacheable HTTP cached")
			}
			if mode == "no-store" || mode == "no-cache" {
				if rec.Code != 304 || rec.Header().Get("ETag") == "" {
					t.Fatal("validated uncacheable map lost conditional semantics")
				}
				before := calls.Load()
				second := fitnessRequest(ih, dp, "GET", "", "")
				if second.Code != 200 || calls.Load() == before {
					t.Fatal("uncacheable map reused without upstream validation")
				}
			} else {
				img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
				if rec.Code != 200 || err != nil || fitnessRedPixels(img) < 80 || rec.Header().Get("ETag") != "" {
					t.Fatal("map failure hid valid route or returned unsafe 304")
				}
				routeOnly, err := drawFitnessThumbnail(context.Background(), fitnessProjectionInput(t), "run", 320, 180)
				if err != nil {
					t.Fatal(err)
				}
				var fallback bytes.Buffer
				png.Encode(&fallback, routeOnly)
				if !bytes.Equal(rec.Body.Bytes(), fallback.Bytes()) {
					t.Fatal("partial map/attribution leaked into failure fallback")
				}
			}
			recovered.Store(true)
			// This test exercises recovery without wall-clock waiting. The production
			// limiter's retry-after behavior itself belongs to tile-source tests.
			if mode == "429" {
				source.limiter.mu.Lock()
				source.limiter.cooldown = time.Time{}
				source.limiter.mu.Unlock()
			}
			if mode == "timeout" {
				source.client.Timeout = time.Second
			}
			good := fitnessRequest(ih, dp, "GET", "", "")
			if good.Code != 200 || good.Header().Get("ETag") == "" || good.Header().Get("Cache-Control") != "private, max-age=0, must-revalidate" {
				t.Fatal("fallback did not recover on next view")
			}
			if _, _, ok := source.cachedThumbnail(cacheKey); !ok {
				t.Fatal("recovered map not cached")
			}
		})
	}
}

func fitnessProjectionInput(t *testing.T) [][]fitnessPoint {
	t.Helper()
	segments, err := parseFitnessGPX(context.Background(), strings.NewReader(fitnessTestGPX))
	if err != nil {
		t.Fatal(err)
	}
	return segments
}

func TestFitnessBasemapCancellationDoesNotCache(t *testing.T) {
	entered := make(chan struct{}, fitnessMaxBasemapTiles)
	source, _ := fitnessBasemapEnvSource(t, func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-r.Context().Done()
	})
	ih, dp, _ := fitnessTestHandler(t, fitnessTestGPX)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/synthetic.png", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); ih.serveFitnessThumbnail(rec, req, blob.Ref{}, dp) }()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("map cancellation blocked")
	}
	if rec.Body.Len() != 0 || rec.Header().Get("ETag") != "" {
		t.Fatal("canceled render served fallback")
	}
	key := fitnessMapCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 320, 180, source.profile())
	if _, _, ok := source.cachedThumbnail(key); ok {
		t.Fatal("canceled render stored composite")
	}
}

func TestFitnessBasemapAttributionAndSeparateSegments(t *testing.T) {
	source, _ := fitnessBasemapEnvSource(t, func(w http.ResponseWriter, r *http.Request) {
		img := image.NewRGBA(image.Rect(0, 0, 256, 256))
		for y := 0; y < 256; y++ {
			for x := 0; x < 256; x++ {
				img.SetRGBA(x, y, color.RGBA{80, 170, 210, 255})
			}
		}
		w.Header().Set("Cache-Control", "max-age=3600")
		png.Encode(w, img)
	})
	segments := [][]fitnessPoint{{{0, 0}, {1, 0}}, {{9, 0}, {10, 0}}}
	plan := fitnessProjectionFor(segments, 320, 180)
	background, _, _, err := fitnessDrawBasemap(context.Background(), plan, source, "")
	if err != nil {
		t.Fatal(err)
	}
	img, err := drawFitnessProjectedThumbnail(context.Background(), plan, "ride", background)
	if err != nil {
		t.Fatal(err)
	}
	if fitnessRedPixels(img.SubImage(image.Rect(130, 75, 190, 105))) != 0 {
		t.Fatal("map joined disconnected GPX segments")
	}
	for i, p := range []fitnessPoint{plan.projectedSegments[0][0], plan.projectedSegments[1][1]} {
		c := color.NRGBAModel.Convert(img.At(int(p.x), int(p.y))).(color.NRGBA)
		if i == 0 && (c.G <= c.R || c.G <= c.B) {
			t.Fatal("map/start marker misaligned")
		}
		if i == 1 && c.R < 150 {
			t.Fatal("map/end marker misaligned")
		}
	}
	before := append([]byte(nil), img.Pix...)
	drawFitnessAttribution(img)
	changed := 0
	dark := 0
	for y := 150; y < 180; y++ {
		for x := 110; x < 320; x++ {
			offset := img.PixOffset(x, y)
			if !bytes.Equal(before[offset:offset+4], img.Pix[offset:offset+4]) {
				changed++
			}
			c := img.RGBAAt(x, y)
			if c.R < 80 && c.G < 80 && c.B < 80 {
				dark++
			}
		}
	}
	if changed < 1000 || dark < 100 {
		t.Fatal("readable PNG attribution missing")
	}
	// Rendering an independent cached/bigger PNG must be safe with per-call font faces.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); drawFitnessAttribution(image.NewRGBA(image.Rect(0, 0, 320, 180))) }()
	}
	wg.Wait()
}

func TestFitnessBasemapBadConfigurationAndMissingTrack(t *testing.T) {
	t.Setenv("PERKEEP_FITNESS_TILE_URL", "not-a-tile-template")
	t.Setenv("PERKEEP_FITNESS_TILE_CACHE_DIR", t.TempDir())
	ih, dp, _ := fitnessTestHandler(t, fitnessTestGPX)
	rec := fitnessRequest(ih, dp, "GET", "", "*")
	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if rec.Code != 200 || err != nil || fitnessRedPixels(img) < 80 || rec.Header().Get("ETag") != "" || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("invalid trusted configuration hid the track or allowed premature 304")
	}
	head := fitnessRequest(ih, dp, "HEAD", "", "*")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("ETag") != "" {
		t.Fatal("invalid-configuration HEAD failed")
	}
	var calls atomic.Int64
	source, _ := fitnessBasemapEnvSource(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) })
	dp.Attr.Set(nodeattr.FitnessTrack, blob.RefFromString("synthetic missing file").String())
	rec = fitnessRequest(ih, dp, "GET", "", "*")
	img, err = png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if rec.Code != 200 || err != nil || fitnessRedPixels(img) != 0 || rec.Header().Get("ETag") != "" || calls.Load() != 0 {
		t.Fatal("missing local track caused tile HTTP or invented route")
	}
	key := fitnessMapCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 320, 180, source.profile())
	if _, _, ok := source.cachedThumbnail(key); ok {
		t.Fatal("missing-track fallback was cached")
	}
}

func TestFitnessBasemapMaximumRenderMemoryAndBounds(t *testing.T) {
	var calls atomic.Int64
	source, _ := fitnessBasemapEnvSource(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		z, x, y := fitnessBasemapCoords(t, r)
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write(fitnessBasemapTilePNG(t, z, x, y, 0))
	})
	ih, dp, _ := fitnessTestHandler(t, `<gpx><trk><trkseg><trkpt lat="40" lon="12"/></trkseg></trk></gpx>`)
	// Use the default shared fallback semaphore: its budget must permit the
	// maximum allowed canvas plus bounded map images/GPX parsing reservation.
	ih.ResizeSem = nil
	ih.MaxWidth, ih.MaxHeight = 2000, 2000
	rec := fitnessRequest(ih, dp, "GET", "", "")
	cfg, err := png.DecodeConfig(bytes.NewReader(rec.Body.Bytes()))
	if rec.Code != 200 || err != nil || cfg.Width != 2000 || cfg.Height != 2000 || calls.Load() > 16 {
		t.Fatalf("max-size map render = %d (%v), %d tile calls", rec.Code, err, calls.Load())
	}
	key := fitnessMapCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 2000, 2000, source.profile())
	if _, _, ok := source.cachedThumbnail(key); !ok {
		t.Fatal("max-size composite not cached")
	}
	before := calls.Load()
	ih.MaxWidth = 2001
	if invalid := fitnessRequest(ih, dp, "GET", "", ""); invalid.Code != 400 || calls.Load() != before {
		t.Fatal("oversize canvas reached tile HTTP")
	}
}

func TestFitnessBasemapViewportExcludesBoundaryNeighbors(t *testing.T) {
	// One exact canonical tile is the whole viewport. Touching an edge must not
	// request its neighbor, even though the neighboring region is near the route.
	for _, zoom := range []int{0, 8, 18} {
		world := math.Ldexp(256, zoom)
		scale := world / (2 * math.Pi)
		tx, ty := 0, 0
		if zoom > 0 {
			tx, ty = 5, 7
		}
		plan := fitnessProjection{width: 256, height: 256, scalePixelsPerRad: scale,
			centreMercatorX: (float64(tx)*256+128)/world*2*math.Pi - math.Pi,
			centreMercatorY: (float64(ty)*256+128)/world*2*math.Pi - math.Pi}
		vp := fitnessViewportAtZoom(plan, zoom)
		if vp.minX != tx || vp.maxX != tx || vp.minY != ty || vp.maxY != ty {
			t.Fatalf("exact boundary at zoom %d requested neighbors: %+v", zoom, vp)
		}
	}
}

func TestFitnessBasemapCustomAttributionInvalidatesComposite(t *testing.T) {
	var calls atomic.Int64
	source, _ := fitnessBasemapEnvSource(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		z, x, y := fitnessBasemapCoords(t, r)
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write(fitnessBasemapTilePNG(t, z, x, y, 0))
	})
	t.Setenv("PERKEEP_FITNESS_TILE_ATTRIBUTION", "")
	ih, dp, _ := fitnessTestHandler(t, fitnessTestGPX)
	first := fitnessRequest(ih, dp, "GET", "", "")
	oldKey := fitnessMapCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 320, 180, source.profile())
	if first.Code != 200 {
		t.Fatal("default-credit map failed")
	}
	initialCalls := calls.Load()
	t.Setenv("PERKEEP_FITNESS_TILE_ATTRIBUTION", "  © Synthetic Offline Maps contributors  ")
	if fitnessMapAttributionText() != "© Synthetic Offline Maps contributors" {
		t.Fatal("custom credit not honored")
	}
	newKey := fitnessMapCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 320, 180, source.profile())
	if oldKey == newKey {
		t.Fatal("credit changed without composite invalidation")
	}
	changed := fitnessRequest(ih, dp, "GET", "", first.Header().Get("ETag"))
	if changed.Code != 200 || changed.Header().Get("ETag") == first.Header().Get("ETag") || bytes.Equal(changed.Body.Bytes(), first.Body.Bytes()) {
		t.Fatal("new provider credit missing from PNG bytes")
	}
	if calls.Load() != initialCalls {
		t.Fatal("credit-only change unnecessarily refetched fresh tiles")
	}
	t.Setenv("PERKEEP_FITNESS_TILE_ATTRIBUTION", strings.Repeat("x", 513))
	if fitnessMapAttributionText() != "© OpenStreetMap contributors" {
		t.Fatal("unbounded attribution accepted")
	}
}

func TestFitnessBasemapPixelCenterTileSeams(t *testing.T) {
	for _, zoom := range []int{8, 18} {
		t.Run(fmt.Sprint(zoom), func(t *testing.T) {
			source, _ := fitnessBasemapEnvSource(t, func(w http.ResponseWriter, r *http.Request) {
				z, x, y := fitnessBasemapCoords(t, r)
				w.Header().Set("Cache-Control", "max-age=3600")
				w.Write(fitnessBasemapTilePNG(t, z, x, y, 0))
			})
			world := math.Ldexp(256, zoom)
			plan := fitnessProjection{width: 512, height: 256, scalePixelsPerRad: world / (2 * math.Pi),
				centreMercatorX: (6*256-.5)/world*2*math.Pi - math.Pi,
				centreMercatorY: (7*256+128)/world*2*math.Pi - math.Pi}
			img, _, _, err := fitnessDrawBasemap(context.Background(), plan, source, "")
			if err != nil {
				t.Fatal(err)
			}
			for py := 0; py < 256; py++ {
				for px := 0; px < 512; px++ {
					mx := plan.centreMercatorX + (float64(px)+.5-256)/plan.scalePixelsPerRad
					my := plan.centreMercatorY + (float64(py)+.5-128)/plan.scalePixelsPerRad
					wx := int(math.Floor((mx + math.Pi) / (2 * math.Pi) * world))
					wy := int(math.Floor((my + math.Pi) / (2 * math.Pi) * world))
					want := color.NRGBA{uint8(wx % 256), uint8(wy % 256), uint8((wx/256)*41 + (wy/256)*17 + zoom), 255}
					if got := color.NRGBAModel.Convert(img.At(px, py)).(color.NRGBA); got != want {
						t.Fatalf("tile seam %d,%d = %v, want %v", px, py, got, want)
					}
				}
			}
		})
	}
}

func TestFitnessBasemapAttributionFitsWholeCreditOrFallsBack(t *testing.T) {
	credits := []string{
		"© OpenStreetMap contributors",
		"© Synthetic Maps contributors",
		"© Synthetic Offline Maps contributors. Use subject to Attribution License and Full Provider Terms.",
		strings.Repeat("unbreakable", 40),
	}
	for _, credit := range credits {
		layout, ok := fitnessFitAttribution(credit, 64, 31)
		if !ok {
			if credit == credits[0] {
				t.Fatal("small default credit should fit without clipping")
			}
			continue
		}
		if strings.Join(layout.lines, " ") != strings.Join(strings.Fields(credit), " ") {
			t.Fatal("fit discarded provider credit words")
		}
		if layout.width > 64 || layout.height > 31 {
			t.Fatal("full credit layout exceeds thumbnail")
		}
		for i, line := range layout.lines {
			ink, _ := font.BoundString(layout.face, line)
			x := 64 - layout.width + 4 - layout.minX
			y := 31 - layout.height + 2 - layout.minY + i*layout.lineHeight
			if x+ink.Min.X.Floor() < 0 || x+ink.Max.X.Ceil() > 64 || y+ink.Min.Y.Floor() < 0 || y+ink.Max.Y.Ceil() > 31 {
				t.Fatalf("line %q clipped outside 64x31 image", line)
			}
		}
		layout.face.Close()
	}
	// A valid long custom credit cannot fit this card at a legible font size.
	// The HTTP path must omit the map entirely, rather than show a clipped footer.
	credit := credits[2]
	if layout, ok := fitnessFitAttribution(credit, 64, 31); ok {
		layout.face.Close()
		t.Fatal("test credit unexpectedly fits")
	}
	t.Setenv("PERKEEP_FITNESS_TILE_ATTRIBUTION", credit)
	var calls atomic.Int64
	source, _ := fitnessBasemapEnvSource(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		z, x, y := fitnessBasemapCoords(t, r)
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write(fitnessBasemapTilePNG(t, z, x, y, 0))
	})
	ih, dp, _ := fitnessTestHandler(t, fitnessTestGPX)
	ih.MaxWidth, ih.MaxHeight = 64, 31
	rec := fitnessRequest(ih, dp, "GET", "", "*")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("ETag") != "" || calls.Load() == 0 {
		t.Fatal("unfittable attribution did not become an honest uncached fallback")
	}
	routeOnly, err := drawFitnessThumbnail(context.Background(), fitnessProjectionInput(t), "run", 64, 31)
	if err != nil {
		t.Fatal(err)
	}
	var fallback bytes.Buffer
	png.Encode(&fallback, routeOnly)
	if !bytes.Equal(rec.Body.Bytes(), fallback.Bytes()) {
		t.Fatal("unfittable attribution retained map pixels or clipped terms")
	}
	key := fitnessMapCacheKey(dp.Attr.Get(nodeattr.FitnessTrack), "run", 64, 31, source.profile())
	if _, _, ok := source.cachedThumbnail(key); ok {
		t.Fatal("credit failure poisoned composite cache")
	}
	// Failure leaves the original canvas untouched; no partially painted footer.
	canvas := image.NewRGBA(image.Rect(0, 0, 64, 31))
	before := append([]byte(nil), canvas.Pix...)
	if drawFitnessAttribution(canvas) || !bytes.Equal(canvas.Pix, before) {
		t.Fatal("failed attribution partially drew/clipped credit")
	}
}
