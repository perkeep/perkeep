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
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/blob"
)

// Tests use private temporary directories and fake transports/local httptest
// servers for requests, never an owner's cache or public tiles.
type fitnessTileTestTransport func(*http.Request) (*http.Response, error)

func (f fitnessTileTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fitnessTileTestClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fitnessTileTestClock) now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fitnessTileTestClock) advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func fitnessTileTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	im.Set(0, 0, color.NRGBA{R: 23, G: 97, B: 140, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, im); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func fitnessTileTestResponse(status int, headers http.Header, data []byte) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
}

func fitnessTileTestSource(t *testing.T, transport fitnessTileTestTransport) (*fitnessTileSource, *fitnessTileTestClock) {
	t.Helper()
	clock := &fitnessTileTestClock{t: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	s, err := newFitnessTileSource("https://tiles.invalid/{z}/{x}/{y}.png", t.TempDir(), "", &http.Client{Transport: transport}, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	// Isolate cooldowns without changing the process-global application limiter.
	s.limiter = &fitnessTileLimiter{slots: make(chan struct{}, 16)}
	return s, clock
}

func fitnessTileTestSymlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		// ERROR_PRIVILEGE_NOT_HELD is not classified as os.ErrPermission.
		const windowsPrivilegeNotHeld = syscall.Errno(1314)
		if runtime.GOOS == "windows" && (os.IsPermission(err) || errors.Is(err, windowsPrivilegeNotHeld)) {
			t.Skipf("Windows symlink privileges unavailable: %v", err)
		}
		t.Fatal(err)
	}
}

func TestFitnessTileEnvironment(t *testing.T) {
	t.Setenv("PERKEEP_FITNESS_TILE_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("PERKEEP_FITNESS_TILE_USER_AGENT", "")
	var defaultSource *fitnessTileSource
	for _, tc := range []struct {
		name, value, want string
		unset             bool
	}{
		{name: "unset", want: fitnessTileDefaultURL, unset: true},
		{name: "empty", want: fitnessTileDefaultURL},
		{name: "explicit_default", value: fitnessTileDefaultURL, want: fitnessTileDefaultURL},
		{name: "custom", value: "https://tiles.invalid/{z}/{x}/{y}.png", want: "https://tiles.invalid/{z}/{x}/{y}.png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PERKEEP_FITNESS_TILE_URL", tc.value)
			if tc.unset {
				if err := os.Unsetenv("PERKEEP_FITNESS_TILE_URL"); err != nil {
					t.Fatal(err)
				}
			}
			source, err := fitnessTileSourceFromEnv(blob.Ref{})
			if err != nil {
				t.Fatal(err)
			}
			if source == nil || source.template != tc.want {
				t.Fatalf("source = %v, want template %q", source, tc.want)
			}
			if tc.want == fitnessTileDefaultURL {
				if defaultSource == nil {
					defaultSource = source
				} else if source != defaultSource {
					t.Fatal("implicit and explicit defaults did not reuse the same cache source")
				}
			}
		})
	}
}

func TestFitnessTileDisabledAndTemplate(t *testing.T) {
	t.Setenv("PERKEEP_FITNESS_TILE_URL", "off")
	t.Setenv("PERKEEP_FITNESS_TILE_CACHE_DIR", filepath.Join(t.TempDir(), "must-not-create"))
	if s, err := fitnessTileSourceFromEnv(blob.Ref{}); s != nil || err != nil {
		t.Fatalf("disabled = %v, %v", s, err)
	}
	if _, err := os.Stat(os.Getenv("PERKEEP_FITNESS_TILE_CACHE_DIR")); !os.IsNotExist(err) {
		t.Fatalf("disabled touched cache: %v", err)
	}
	for _, template := range []string{
		"https://tile.openstreetmap.org/{z}/{x}/{y}.png",
		"http://127.0.0.1:1234/{z}/{x}/{y}.png",
		"https://custom.invalid/map/{z}/{x}/{y}.png?key=adminsecret",
	} {
		if !fitnessTileTemplateValid(template) {
			t.Errorf("valid template rejected: %q", template)
		}
	}
	for _, template := range []string{
		"", "https://custom.invalid/{z}/{x}.png", "ftp://custom.invalid/{z}/{x}/{y}.png",
		"https://custom.invalid/{z}/{x}/{y}/{unknown}.png", "https://custom.invalid/{z}/{x}/{y}/{x}.png",
		"https://user:secret@custom.invalid/{z}/{x}/{y}.png", "https://{z}.invalid/{x}/{y}.png",
		"https://custom.invalid/{z}/{x}/{y}.png#secret", "http://tile.openstreetmap.org/{z}/{x}/{y}.png",
		"https://tile.openstreetmap.org/{z}/{x}/{y}.png?token=secret",
	} {
		if fitnessTileTemplateValid(template) {
			t.Errorf("invalid template accepted: %q", template)
		}
	}
	_, err := newFitnessTileSource("https://tiles.invalid/{z}/{x}/{y}.png", t.TempDir(), "unsafe\nvalue", &http.Client{}, time.Now)
	if !errors.Is(err, errFitnessTileConfig) {
		t.Fatalf("bad UA = %v", err)
	}
}

func TestFitnessTileIdentityAndFreshDiskCache(t *testing.T) {
	data := fitnessTileTestPNG(t, 256, 256)
	calls := 0
	s, clock := fitnessTileTestSource(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != "GET" || req.URL.Path != "/3/4/5.png" || req.URL.RawQuery != "" {
			t.Errorf("unexpected request: %v", req.URL)
		}
		if req.Header.Get("User-Agent") != fitnessTileUserAgent || req.Header.Get("Accept") != "image/png" {
			t.Errorf("identity headers: %v", req.Header)
		}
		if req.Header.Get("Referer") != "https://pkdev.example/" {
			t.Errorf("Referer = %q", req.Header.Get("Referer"))
		}
		if req.Header.Get("Cookie") != "" || req.Header.Get("Authorization") != "" {
			t.Errorf("private headers: %v", req.Header)
		}
		return fitnessTileTestResponse(200, http.Header{"Cache-Control": {"max-age=3600"}, "Etag": {`"opaque-validator"`}}, data), nil
	})
	first, err := s.tile(context.Background(), 3, 4, 5, "https://pkdev.example/#fragment", true)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Cacheable || first.Digest != fitnessTileDigest(data) || !first.Expires.Equal(clock.now().Add(time.Hour)) {
		t.Fatalf("tile = %#v", first)
	}
	if first.Image.Bounds() != image.Rect(0, 0, 256, 256) {
		t.Fatal("wrong dimensions")
	}
	for _, network := range []bool{true, false} {
		got, err := s.tile(context.Background(), 3, 4, 5, "", network)
		if err != nil || got.Digest != first.Digest {
			t.Fatalf("cache = %#v, %v", got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("upstream calls = %d", calls)
	}
	// The cache survives source replacement and a non-networking client.
	reopened, err := newFitnessTileSource(s.template, filepath.Dir(s.dir), "", &http.Client{Transport: fitnessTileTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("fresh disk tile fetched")
		return nil, errors.New("blocked")
	})}, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.tile(context.Background(), 3, 4, 5, "", false); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Dir(s.dir), s.dir} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0700) {
			t.Fatalf("directory mode: %v, %v", info, err)
		}
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries = %v, %v", entries, err)
	}
	info, err := entries[0].Info()
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) || strings.Contains(entries[0].Name(), "3/4/5") {
		t.Fatalf("file mode/name = %v, %v", info, err)
	}
}

func TestFitnessTileConditional304(t *testing.T) {
	data := fitnessTileTestPNG(t, 256, 256)
	calls := 0
	s, clock := fitnessTileTestSource(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return fitnessTileTestResponse(200, http.Header{"Cache-Control": {"max-age=600"}, "Etag": {`"validator"`}, "Last-Modified": {"Mon, 12 Oct 2026 12:00:00 GMT"}, "Date": {"Tue, 13 Oct 2026 12:00:00 GMT"}, "Age": {"30"}}, data), nil
		}
		if req.Header.Get("If-None-Match") != `"validator"` || req.Header.Get("If-Modified-Since") == "" {
			t.Errorf("conditional headers = %v", req.Header)
		}
		if calls == 2 {
			return fitnessTileTestResponse(304, nil, nil), nil
		}
		return fitnessTileTestResponse(304, http.Header{"Cache-Control": {"max-age=120"}, "Age": {"20"}}, nil), nil
	})
	first, err := s.tile(context.Background(), 2, 1, 1, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Expires.Equal(clock.now().Add(570 * time.Second)) {
		t.Fatalf("first expiry = %v", first.Expires)
	}
	clock.advance(571 * time.Second)
	if _, err := s.tile(context.Background(), 2, 1, 1, "", false); !errors.Is(err, errFitnessTileMiss) {
		t.Fatalf("stale no-network = %v", err)
	}
	second, err := s.tile(context.Background(), 2, 1, 1, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if second.Digest != first.Digest || !second.Expires.Equal(clock.now().Add(600*time.Second)) {
		t.Fatalf("304 missing directives = %#v", second)
	}
	if _, err := s.tile(context.Background(), 2, 1, 1, "", true); err != nil || calls != 2 {
		t.Fatalf("304 fresh cache: %v, calls=%d", err, calls)
	}
	clock.advance(601 * time.Second)
	third, err := s.tile(context.Background(), 2, 1, 1, "", true)
	if err != nil || !third.Expires.Equal(clock.now().Add(100*time.Second)) {
		t.Fatalf("304 updated directives = %#v, %v", third, err)
	}
}

func TestFitnessTileNoCacheNoStore(t *testing.T) {
	data := fitnessTileTestPNG(t, 256, 256)
	for _, directive := range []string{"no-cache, max-age=3600", "no-store, max-age=3600"} {
		t.Run(directive, func(t *testing.T) {
			var calls int
			s, clock := fitnessTileTestSource(t, func(req *http.Request) (*http.Response, error) {
				calls++
				if calls > 1 && strings.HasPrefix(directive, "no-cache") {
					if req.Header.Get("If-None-Match") != `"tag"` {
						t.Error("no-cache not revalidated")
					}
					return fitnessTileTestResponse(304, nil, nil), nil
				}
				return fitnessTileTestResponse(200, http.Header{"Cache-Control": {directive}, "Etag": {`"tag"`}}, data), nil
			})
			for i := 0; i < 2; i++ {
				got, err := s.tile(context.Background(), 0, 0, 0, "", true)
				if err != nil {
					t.Fatal(err)
				}
				if got.Expires.After(clock.now()) || got.Cacheable {
					t.Fatalf("directive result = %#v", got)
				}
				if err := s.storeThumbnail("composite", data, got.Expires); err == nil {
					t.Fatal("nonfresh composite stored")
				}
				if _, err := s.tile(context.Background(), 0, 0, 0, "", false); !errors.Is(err, errFitnessTileMiss) {
					t.Fatalf("non-network = %v", err)
				}
			}
			if calls != 2 {
				t.Fatalf("calls = %d", calls)
			}
			entries, _ := os.ReadDir(s.dir)
			want := 0
			if strings.HasPrefix(directive, "no-cache") {
				want = 1
			}
			if len(entries) != want {
				t.Fatalf("disk entries = %d, want %d", len(entries), want)
			}
		})
	}
	// A 304 changing a formerly stored response to no-store removes it.
	var calls int
	s, _ := fitnessTileTestSource(t, func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return fitnessTileTestResponse(200, http.Header{"Cache-Control": {"max-age=0"}, "Etag": {`"tag"`}}, data), nil
		}
		return fitnessTileTestResponse(304, http.Header{"Cache-Control": {"no-store"}}, nil), nil
	})
	if _, err := s.tile(context.Background(), 0, 0, 0, "", true); err != nil {
		t.Fatal(err)
	}
	got, err := s.tile(context.Background(), 0, 0, 0, "", true)
	if err != nil || got.Cacheable {
		t.Fatalf("304 no-store = %#v, %v", got, err)
	}
	entries, _ := os.ReadDir(s.dir)
	if len(entries) != 0 {
		t.Fatal("304 no-store retained disk entry")
	}
}

func TestFitnessTileCachePolicy(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	date := now.Add(-30 * time.Second).Format(http.TimeFormat)
	for _, tt := range []struct {
		name             string
		h                fitnessTileHeaders
		ttl              time.Duration
		noCache, noStore bool
	}{
		{"missing", fitnessTileHeaders{}, 7 * 24 * time.Hour, false, false},
		{"max-age", fitnessTileHeaders{CacheControl: "private, max-age=600, s-maxage=1"}, 600 * time.Second, false, false},
		{"age", fitnessTileHeaders{CacheControl: "max-age=600", Date: date, Age: "90"}, 510 * time.Second, false, false},
		{"apparent-age", fitnessTileHeaders{CacheControl: "max-age=600", Date: date, Age: "5"}, 570 * time.Second, false, false},
		{"expires", fitnessTileHeaders{Date: date, Expires: now.Add(time.Hour).Format(http.TimeFormat), Age: "90"}, 3540 * time.Second, false, false},
		{"expires-no-date", fitnessTileHeaders{Expires: now.Add(time.Hour).Format(http.TimeFormat), Age: "90"}, 3510 * time.Second, false, false},
		{"past-expires", fitnessTileHeaders{Expires: date}, 0, false, false},
		{"expired-age", fitnessTileHeaders{CacheControl: "max-age=30", Age: "90"}, 0, false, false},
		{"no-cache", fitnessTileHeaders{CacheControl: "max-age=600, no-cache"}, 0, true, false},
		{"no-store", fitnessTileHeaders{CacheControl: "max-age=600, NO-STORE"}, 0, false, true},
		{"zero", fitnessTileHeaders{CacheControl: "max-age=0"}, 0, false, false},
		{"quoted", fitnessTileHeaders{CacheControl: `max-age="123"`}, 123 * time.Second, false, false},
		{"bad-max-age", fitnessTileHeaders{CacheControl: "max-age=broken"}, 0, false, false},
		{"negative-max-age", fitnessTileHeaders{CacheControl: "max-age=-1"}, 0, false, false},
		{"duplicate-max-age", fitnessTileHeaders{CacheControl: "max-age=300, max-age=30"}, 30 * time.Second, false, false},
		{"bad-expires", fitnessTileHeaders{Expires: "broken"}, 0, false, false},
		{"bad-age", fitnessTileHeaders{Age: "broken", Expires: now.Add(time.Hour).Format(http.TimeFormat)}, 0, false, false},
		{"future-date", fitnessTileHeaders{CacheControl: "max-age=300", Date: now.Add(time.Hour).Format(http.TimeFormat)}, 300 * time.Second, false, false},
		{"vary-star", fitnessTileHeaders{Vary: "*"}, 0, false, true},
		{"vary-referer", fitnessTileHeaders{Vary: "Referer"}, 0, false, true},
		{"vary-constant", fitnessTileHeaders{Vary: "Accept, User-Agent, Accept-Encoding"}, 7 * 24 * time.Hour, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := fitnessTilePolicy(tt.h, now)
			if got.Expires.Sub(now) != tt.ttl || got.NoCache != tt.noCache || got.NoStore != tt.noStore {
				t.Fatalf("policy = %#v; ttl=%v, want %v", got, got.Expires.Sub(now), tt.ttl)
			}
		})
	}
}

func TestFitnessTileInvalidImagesAndSanitizedErrors(t *testing.T) {
	good := fitnessTileTestPNG(t, 256, 256)
	badCRC := append([]byte(nil), good...)
	badCRC[len(badCRC)-1] ^= 1
	for _, tt := range []struct {
		name          string
		data          []byte
		status        int
		contentLength int64
	}{
		{"html", []byte("<html>private upstream diagnostic</html>"), 200, -1},
		{"255px", fitnessTileTestPNG(t, 255, 256), 200, -1},
		{"512px", fitnessTileTestPNG(t, 512, 512), 200, -1},
		{"truncated", good[:len(good)-15], 200, -1},
		{"bad-crc", badCRC, 200, -1},
		{"large-chunked", bytes.Repeat([]byte("x"), fitnessTileMaxBytes+1), 200, -1},
		{"large-declared", good, 200, fitnessTileMaxBytes + 1},
		{"unsolicited-304", nil, 304, -1},
		{"404", good, 404, -1},
		{"redirect", good, 302, -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			s, _ := fitnessTileTestSource(t, func(*http.Request) (*http.Response, error) {
				calls++
				resp := fitnessTileTestResponse(tt.status, nil, tt.data)
				resp.ContentLength = tt.contentLength
				return resp, nil
			})
			_, err := s.tile(context.Background(), 3, 4, 5, "", true)
			var typed fitnessTileError
			if !errors.As(err, &typed) {
				t.Fatalf("error not sanitized/typed: %v", err)
			}
			entries, _ := os.ReadDir(s.dir)
			if len(entries) != 0 || calls != 1 {
				t.Fatalf("invalid image cached or retried: entries=%d calls=%d", len(entries), calls)
			}
		})
	}
	s, _ := fitnessTileTestSource(t, func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("https://private.invalid/3/4/5.png?api_key=TOPSECRET")
	})
	_, err := s.tile(context.Background(), 3, 4, 5, "", true)
	if !errors.Is(err, errFitnessTileUpstream) || strings.Contains(err.Error(), "TOPSECRET") || strings.Contains(err.Error(), "/3/4/5") {
		t.Fatalf("unsafe error = %v", err)
	}
}

func TestFitnessTileNoRedirectOrCookies(t *testing.T) {
	var upstream, redirected atomic.Int32
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(500) }))
	defer dest.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("leaked headers: %v", r.Header)
		}
		http.Redirect(w, r, dest.URL+"/secret", http.StatusFound)
	}))
	defer origin.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(origin.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "private", Value: "SECRET"}})
	client := origin.Client()
	client.Jar = jar
	client.Timeout = time.Minute
	s, err := newFitnessTileSource(origin.URL+"/{z}/{x}/{y}.png", t.TempDir(), "test/application (+https://perkeep.org/)", client, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	s.limiter = &fitnessTileLimiter{slots: make(chan struct{}, 2)}
	if s.client.Timeout != 5*time.Second || s.client.Jar != nil || client.Jar == nil || client.Timeout != time.Minute {
		t.Fatal("client not safely cloned")
	}
	if _, err := s.tile(context.Background(), 0, 0, 0, "https://pkdev.example/", true); !errors.Is(err, errFitnessTileUpstream) {
		t.Fatalf("redirect error = %v", err)
	}
	if upstream.Load() != 1 || redirected.Load() != 0 {
		t.Fatalf("requests: origin=%d redirected=%d", upstream.Load(), redirected.Load())
	}
}

func TestFitnessTileDisablesStandardTransportWireReplay(t *testing.T) {
	// A standard nil-body GET would be sent again after the second request
	// reaches a reused connection but that connection closes before response
	// headers. Every actual upstream request must have its own limiter slot.
	data := fitnessTileTestPNG(t, 256, 256)
	var wireCalls, transportCalls, connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil || len(body) != 0 || r.Method != http.MethodGet ||
			r.ContentLength != 0 || len(r.TransferEncoding) != 0 ||
			r.Header.Get("Content-Length") != "" || r.Header.Get("Transfer-Encoding") != "" {
			t.Errorf("bodyless GET framing: bytes=%d length=%d transfer=%v headers=%v err=%v",
				len(body), r.ContentLength, r.TransferEncoding, r.Header, err)
		}
		if r.Header.Get("User-Agent") != fitnessTileUserAgent ||
			r.Header.Get("Referer") != "https://pkdev.example/" ||
			r.Header.Get("Cache-Control") != "" || r.Header.Get("Pragma") != "" {
			t.Errorf("identity/cache headers changed: %v", r.Header)
		}
		if wireCalls.Add(1) == 2 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			conn.Close() // Received GET, deliberately no response.
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Header().Set("Cache-Control", "max-age=600")
		w.Write(data)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	client := &http.Client{Transport: fitnessTileTestTransport(func(req *http.Request) (*http.Response, error) {
		transportCalls.Add(1)
		if req.Body == nil || req.Body == http.NoBody || req.GetBody != nil || req.ContentLength != 0 {
			t.Error("request permits an internal replay")
		}
		return base.RoundTrip(req)
	})}
	source, err := newFitnessTileSource(server.URL+"/{z}/{x}/{y}.png", t.TempDir(), "", client, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	source.limiter = &fitnessTileLimiter{slots: make(chan struct{}, 2)}
	if _, err := source.tile(context.Background(), 1, 0, 0, "https://pkdev.example/", true); err != nil {
		t.Fatal(err)
	}
	if _, err := source.tile(context.Background(), 1, 1, 0, "https://pkdev.example/", true); !errors.Is(err, errFitnessTileUpstream) {
		t.Fatalf("EOF should fail, not silently retry: %v", err)
	}
	if wireCalls.Load() != 2 || transportCalls.Load() != 2 || connections.Load() != 1 {
		t.Fatalf("wire=%d transport=%d connections=%d; want 2/2/1",
			wireCalls.Load(), transportCalls.Load(), connections.Load())
	}
	if len(source.limiter.slots) != 0 {
		t.Fatal("failed fetch leaked a limiter permit")
	}
	if _, err := source.tile(context.Background(), 1, 1, 0, "", false); !errors.Is(err, errFitnessTileMiss) {
		t.Fatalf("failed tile was cached: %v", err)
	}
}

func TestFitnessTileCooldownNoRetry(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			s, _ := fitnessTileTestSource(t, func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return fitnessTileTestResponse(status, http.Header{"Retry-After": {"120"}}, nil), nil
			})
			if _, err := s.tile(context.Background(), 2, 0, 0, "", true); !errors.Is(err, errFitnessTileCooldown) {
				t.Fatalf("cooldown = %v", err)
			}
			other, _ := fitnessTileTestSource(t, func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return fitnessTileTestResponse(200, nil, nil), nil
			})
			other.limiter = s.limiter
			if _, err := other.tile(context.Background(), 2, 1, 0, "", true); !errors.Is(err, errFitnessTileCooldown) {
				t.Fatalf("global cooldown = %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := s.tile(ctx, 2, 2, 0, "", true); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled = %v", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("retried %d times", calls.Load())
			}
		})
	}
	for _, retryAfter := range []string{"", "broken", "0", time.Now().Add(time.Minute).Format(http.TimeFormat)} {
		l := &fitnessTileLimiter{slots: make(chan struct{}, 2)}
		l.backoff(retryAfter)
		if !l.cooldown.After(time.Now()) {
			t.Fatalf("invalid cooldown for %q", retryAfter)
		}
	}
}

func TestFitnessTileConcurrentCoalescingAndCancellation(t *testing.T) {
	synctest.Test(t, testFitnessTileConcurrentCoalescingAndCancellation)
}

func testFitnessTileConcurrentCoalescingAndCancellation(t *testing.T) {
	data := fitnessTileTestPNG(t, 256, 256)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s, _ := fitnessTileTestSource(t, func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return fitnessTileTestResponse(200, nil, data), nil
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := s.tile(ctx, 4, 2, 3, "", true); first <- err }()
	<-started
	const waiters = 20
	results := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			got, err := s.tile(context.Background(), 4, 2, 3, "", true)
			if err == nil && got.Digest != fitnessTileDigest(data) {
				err = errors.New("wrong digest")
			}
			results <- err
		}()
	}
	// Wait for all to join the same flight.
	synctest.Wait()
	s.mu.Lock()
	n := 0
	for _, f := range s.flights {
		n += f.waiters
	}
	s.mu.Unlock()
	if n != waiters+1 {
		t.Fatalf("waiters joined = %d; want %d", n, waiters+1)
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader canceled = %v", err)
	}
	close(release)
	for i := 0; i < waiters; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("coalesced calls = %d", calls.Load())
	}

	// Last-waiter cancellation must promptly cancel the network request.
	started2, canceled := make(chan struct{}), make(chan struct{})
	s2, _ := fitnessTileTestSource(t, func(req *http.Request) (*http.Response, error) {
		close(started2)
		<-req.Context().Done()
		close(canceled)
		return nil, req.Context().Err()
	})
	ctx2, cancel2 := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s2.tile(ctx2, 0, 0, 0, "", true); done <- err }()
	<-started2
	cancel2()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream not canceled")
	}
}

func TestFitnessTileGlobalConcurrency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		if cap(fitnessTilesLimiter.slots) != 16 {
			t.Fatal("production limiter must allow sixteen outstanding requests")
		}
		// Blocking on a package-initialized channel is not a durable block in a
		// synctest bubble. Verify production wiring, then share a bubble-local one.
		limiter := &fitnessTileLimiter{slots: make(chan struct{}, 16)}
		data := fitnessTileTestPNG(t, 256, 256)
		started, release := make(chan struct{}, 17), make(chan struct{})
		transport := fitnessTileTestTransport(func(req *http.Request) (*http.Response, error) {
			started <- struct{}{}
			select {
			case <-release:
				return fitnessTileTestResponse(200, nil, data), nil
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		})
		var sources [2]*fitnessTileSource
		for i := range sources {
			var err error
			sources[i], err = newFitnessTileSource(fmt.Sprintf("https://provider%d.invalid/{z}/{x}/{y}.png", i), t.TempDir(), "", &http.Client{Transport: transport}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if sources[i].limiter != fitnessTilesLimiter {
				t.Fatal("provider does not use the process-wide limiter")
			}
			sources[i].limiter = limiter
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 17)
		fetch := func(i int) {
			_, err := sources[i%len(sources)].tile(ctx, 5, i, 0, "", true)
			done <- err
		}
		start := time.Now()
		for i := range 16 {
			go fetch(i)
		}
		synctest.Wait()
		if len(started) != 16 || !time.Now().Equal(start) {
			t.Fatalf("started %d requests after %v; want sixteen without spacing", len(started), time.Since(start))
		}
		go fetch(16)
		synctest.Wait()
		if len(started) != 16 || len(limiter.slots) != 16 {
			t.Fatal("seventeenth request bypassed the global limit")
		}
		release <- struct{}{}
		synctest.Wait()
		if len(started) != 17 || len(limiter.slots) != 16 {
			t.Fatal("queued request did not start when one slot was released")
		}
		close(release)
		for range 17 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if len(limiter.slots) != 0 {
			t.Fatal("completed requests leaked global permits")
		}
	})
}

func TestFitnessTileLimiterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := &fitnessTileLimiter{slots: make(chan struct{}, 16)}
		releases := make([]func(), 16)
		for i := range releases {
			var err error
			releases[i], err = l.acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			release, err := l.acquire(ctx)
			if err == nil {
				release()
			}
			done <- err
		}()
		synctest.Wait()
		if len(done) != 0 {
			t.Fatal("queued acquire did not block")
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("queued cancellation = %v", err)
		}
		if len(l.slots) != 16 {
			t.Fatal("queued cancellation released another request's permit")
		}
		for _, release := range releases {
			release()
		}
		// Even when a canceled context wins a ready slot, it must give it back.
		if release, err := l.acquire(ctx); !errors.Is(err, context.Canceled) {
			if release != nil {
				release()
			}
			t.Fatalf("already-canceled acquire = %v", err)
		}
		if len(l.slots) != 0 {
			t.Fatal("cancellation leaked a permit")
		}
	})
}

func TestFitnessTileQueuedRequestsObserveCooldown(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				data := fitnessTileTestPNG(t, 256, 256)
				var calls atomic.Int32
				backoff, release := make(chan struct{}), make(chan struct{})
				source, _ := fitnessTileTestSource(t, func(req *http.Request) (*http.Response, error) {
					calls.Add(1)
					wait := release
					if req.URL.Path == "/5/0/0.png" {
						wait = backoff
					}
					select {
					case <-wait:
					case <-req.Context().Done():
						return nil, req.Context().Err()
					}
					if wait == backoff {
						return fitnessTileTestResponse(status, http.Header{"Retry-After": {"120"}}, nil), nil
					}
					return fitnessTileTestResponse(200, nil, data), nil
				})
				other, _ := fitnessTileTestSource(t, func(*http.Request) (*http.Response, error) {
					calls.Add(1)
					return fitnessTileTestResponse(200, nil, data), nil
				})
				other.limiter = source.limiter
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 16)
				for i := range 16 {
					go func() {
						_, err := source.tile(ctx, 5, i, 0, "", true)
						done <- err
					}()
				}
				synctest.Wait()
				if calls.Load() != 16 {
					t.Fatalf("started %d requests, want sixteen", calls.Load())
				}
				queued := make(chan error, 1)
				go func() {
					_, err := other.tile(ctx, 5, 16, 0, "", true)
					queued <- err
				}()
				synctest.Wait()
				if len(queued) != 0 {
					t.Fatal("request did not queue behind occupied permits")
				}
				close(backoff)
				if err := <-done; !errors.Is(err, errFitnessTileCooldown) {
					t.Fatalf("upstream backoff = %v", err)
				}
				if err := <-queued; !errors.Is(err, errFitnessTileCooldown) {
					t.Fatalf("queued request ignored shared cooldown: %v", err)
				}
				if calls.Load() != 16 {
					t.Fatal("queued request contacted upstream during cooldown")
				}
				close(release)
				for range 15 {
					if err := <-done; err != nil {
						t.Fatal(err)
					}
				}
				synctest.Wait()
				if len(source.limiter.slots) != 0 {
					t.Fatal("cooldown leaked permits")
				}
				time.Sleep(121 * time.Second)
				if _, err := other.tile(ctx, 5, 16, 0, "", true); err != nil {
					t.Fatalf("request after Retry-After = %v", err)
				}
			})
		})
	}
}

func TestFitnessTileCacheCorruptionAndAtomicWrites(t *testing.T) {
	data := fitnessTileTestPNG(t, 256, 256)
	var calls int
	s, _ := fitnessTileTestSource(t, func(*http.Request) (*http.Response, error) {
		calls++
		return fitnessTileTestResponse(200, nil, data), nil
	})
	if _, err := s.tile(context.Background(), 0, 0, 0, "", true); err != nil {
		t.Fatal(err)
	}
	path := s.recordPath("tile", fitnessTileHash("0/0/0"))
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), valid...)
	corrupt[len(corrupt)-1] ^= 1
	badMeta := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(badMeta[:4], fitnessTileMetaMaxBytes+1)
	// PNG hashes alone must not permit a corrupted expiry to live forever.
	badExpiry := bytes.Replace(valid, []byte("2026-10-20"), []byte("2027-10-20"), 1)
	if bytes.Equal(valid, badExpiry) {
		t.Fatal("could not construct metadata corruption")
	}
	for _, content := range [][]byte{nil, {0, 0}, badMeta, corrupt, badExpiry, bytes.Repeat([]byte("x"), fitnessTileMaxBytes+fitnessTileMetaMaxBytes+5)} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.tile(context.Background(), 0, 0, 0, "", false); !errors.Is(err, errFitnessTileMiss) {
			t.Fatalf("corruption served: %v", err)
		}
		if _, err := s.tile(context.Background(), 0, 0, 0, "", true); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 7 {
		t.Fatalf("corrupt misses should refetch: calls=%d", calls)
	}
	t.Run("unix-permissions", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows cache privacy uses directory ACLs, not Unix mode bits")
		}
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.tile(context.Background(), 0, 0, 0, "", false); !errors.Is(err, errFitnessTileMiss) {
			t.Fatalf("public cache accepted: %v", err)
		}
		if _, err := s.tile(context.Background(), 0, 0, 0, "", true); err != nil {
			t.Fatal(err)
		}
	})
	entries, _ := os.ReadDir(s.dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".fitness-tile-") {
			t.Fatal("temporary file leaked")
		}
	}
	t.Run("symlink", func(t *testing.T) {
		// Symlinks do not expose arbitrary filesystem data through the tile cache.
		other := filepath.Join(t.TempDir(), "external")
		if err := os.WriteFile(other, valid, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		fitnessTileTestSymlink(t, other, path)
		if _, err := s.tile(context.Background(), 0, 0, 0, "", false); !errors.Is(err, errFitnessTileMiss) {
			t.Fatalf("symlink served: %v", err)
		}
		if _, err := s.tile(context.Background(), 0, 0, 0, "", true); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("atomic replacement followed symlink: %v, %v", info, err)
		}
	})
}

func TestFitnessTileCacheWriteFailure(t *testing.T) {
	data := fitnessTileTestPNG(t, 256, 256)
	s, _ := fitnessTileTestSource(t, func(*http.Request) (*http.Response, error) {
		return fitnessTileTestResponse(200, nil, data), nil
	})
	// Replace the private directory with a regular file: deterministic even
	// when the test runner has permission to write read-only directories.
	if err := os.Remove(s.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.dir, []byte("unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.tile(context.Background(), 0, 0, 0, "", true); !errors.Is(err, errFitnessTileStorage) {
		t.Fatalf("uncached tile after failed write = %v", err)
	}
	if _, err := s.tile(context.Background(), 0, 0, 0, "", false); !errors.Is(err, errFitnessTileMiss) {
		t.Fatalf("failed write served = %v", err)
	}
}

func TestFitnessTileAtomicConcurrentCompositeWrites(t *testing.T) {
	s, clock := fitnessTileTestSource(t, func(*http.Request) (*http.Response, error) {
		t.Error("composite fetched upstream")
		return nil, errors.New("blocked")
	})
	data := [][]byte{fitnessTileTestPNG(t, 100, 80), fitnessTileTestPNG(t, 120, 60)}
	expires := []time.Time{clock.now().Add(time.Hour), clock.now().Add(2 * time.Hour)}
	var writers sync.WaitGroup
	done := make(chan struct{})
	failures := make(chan error, 1)
	for i := 0; i < 4; i++ {
		writers.Add(1)
		go func(i int) {
			defer writers.Done()
			for j := 0; j < 12; j++ {
				n := (i + j) % 2
				if err := s.storeThumbnail("atomic", data[n], expires[n]); err != nil {
					select {
					case failures <- err:
					default:
					}
					return
				}
			}
		}(i)
	}
	go func() { writers.Wait(); close(done) }()
	for {
		got, expiry, ok := s.cachedThumbnail("atomic")
		if ok {
			match := false
			for i := range data {
				match = match || (bytes.Equal(got, data[i]) && expiry.Equal(expires[i]))
			}
			if !match {
				t.Fatal("torn metadata/body cache envelope")
			}
		}
		select {
		case <-done:
			select {
			case err := <-failures:
				t.Fatal(err)
			default:
			}
			return
		default:
		}
	}
}

func TestFitnessTileOpenReaderAllowsReplacement(t *testing.T) {
	s, clock := fitnessTileTestSource(t, func(*http.Request) (*http.Response, error) {
		t.Error("composite fetched upstream")
		return nil, errors.New("blocked")
	})
	data := [][]byte{fitnessTileTestPNG(t, 32, 24), fitnessTileTestPNG(t, 24, 32)}
	expiry := clock.now().Add(time.Hour)
	if err := s.storeThumbnail("open-reader", data[0], expiry); err != nil {
		t.Fatal(err)
	}
	path := s.recordPath("thumbnail", "open-reader")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := fitnessTileOpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	opened, err := reader.Stat()
	if err != nil || !os.SameFile(info, opened) {
		t.Fatalf("opened cache file identity: %v, %v", opened, err)
	}
	// Exercise the platform publisher directly so any native rename error is
	// visible in test diagnostics (these paths contain only synthetic data).
	// Production writeRecord deliberately returns a sanitized storage error.
	replacement, err := os.CreateTemp(s.dir, ".fitness-tile-rename-test-")
	if err != nil {
		t.Fatal(err)
	}
	name := replacement.Name()
	defer os.Remove(name)
	if _, err := replacement.Write(original); err != nil {
		replacement.Close()
		t.Fatal(err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fitnessTileReplaceFile(s.dir, filepath.Base(name), filepath.Base(path)); err != nil {
		var errno syscall.Errno
		if errors.As(err, &errno) {
			t.Fatalf("platform replacement with an open destination: %T: %v (native error %d)", err, err, errno)
		}
		t.Fatalf("platform replacement with an open destination: %T: %v", err, err)
	}
	// Keep the old envelope open for every replacement. On Windows os.Open
	// lacks delete sharing and os.Rename lacks POSIX replacement semantics.
	for i := 0; i < 500; i++ {
		want := data[(i+1)%len(data)]
		if err := s.storeThumbnail("open-reader", want, expiry); err != nil {
			t.Fatalf("replacement %d: %v", i, err)
		}
		got, expires, ok := s.cachedThumbnail("open-reader")
		if !ok || !bytes.Equal(got, want) || !expires.Equal(expiry) {
			t.Fatalf("replacement %d: incomplete cache envelope", i)
		}
	}
	got, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("replacement modified the open envelope: %v", err)
	}
}

func TestFitnessTileLongCachePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("a", 80), strings.Repeat("b", 80))
	s, err := newFitnessTileSource("https://tiles.invalid/{z}/{x}/{y}.png", dir, "", &http.Client{Transport: fitnessTileTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("composite fetched upstream")
		return nil, errors.New("blocked")
	})}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	data := fitnessTileTestPNG(t, 32, 24)
	expiry := time.Now().Add(time.Hour)
	if len(s.recordPath("thumbnail", "long-path")) <= 260 {
		t.Fatal("test cache path does not exceed MAX_PATH")
	}
	if err := s.storeThumbnail("long-path", data, expiry); err != nil {
		t.Fatal(err)
	}
	got, expires, ok := s.cachedThumbnail("long-path")
	if !ok || !bytes.Equal(got, data) || !expires.Equal(expiry) {
		t.Fatal("cache record could not be read from a long path")
	}
}

func TestFitnessTileCompositeTTLAndBoundedRead(t *testing.T) {
	s, clock := fitnessTileTestSource(t, func(*http.Request) (*http.Response, error) {
		t.Error("composite helper fetched network")
		return nil, errors.New("blocked")
	})
	data := fitnessTileTestPNG(t, 320, 180)
	expiry := clock.now().Add(time.Hour)
	if err := s.storeThumbnail("private-route-key", data, expiry); err != nil {
		t.Fatal(err)
	}
	got, expires, ok := s.cachedThumbnail("private-route-key")
	if !ok || !bytes.Equal(got, data) || !expires.Equal(expiry) {
		t.Fatalf("thumbnail = %d bytes, %v, %v", len(got), expires, ok)
	}
	path := s.recordPath("thumbnail", "private-route-key")
	if strings.Contains(path, "private-route") {
		t.Fatal("private key leaked into filename")
	}
	clock.advance(time.Hour)
	if _, _, ok := s.cachedThumbnail("private-route-key"); ok {
		t.Fatal("expired composite served")
	}
	if err := s.storeThumbnail("expired", data, expiry); err == nil {
		t.Fatal("expired composite stored")
	}
	for _, bad := range [][]byte{[]byte("HTML"), fitnessTileTestPNG(t, 2001, 1), bytes.Repeat([]byte("x"), fitnessThumbnailMaxBytes+1)} {
		if err := s.storeThumbnail("bad", bad, clock.now().Add(time.Hour)); err == nil {
			t.Fatal("invalid composite stored")
		}
	}
	clock.advance(-time.Hour)
	valid, _ := os.ReadFile(path)
	for _, bad := range [][]byte{{0, 0, 0, 0}, append(valid[:len(valid)-1:len(valid)-1], byte(0))} {
		if err := os.WriteFile(path, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, ok := s.cachedThumbnail("private-route-key"); ok {
			t.Fatal("corrupt composite served")
		}
	}
	// A sparse oversized cache file is rejected from Stat before any body read.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(fitnessThumbnailMaxBytes + fitnessTileMetaMaxBytes + 5); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, ok := s.cachedThumbnail("private-route-key"); ok {
		t.Fatal("oversized composite served")
	}
}

func TestFitnessTileProfilesValidationAndPrivateDirectory(t *testing.T) {
	transport := fitnessTileTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("validation fetched network")
		return nil, errors.New("blocked")
	})
	dir := t.TempDir()
	newSource := func(template, ua, dir string) *fitnessTileSource {
		t.Helper()
		s, err := newFitnessTileSource(template, dir, ua, &http.Client{Transport: transport}, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	template := "https://tiles.invalid/{z}/{x}/{y}.png?key=SUPERSECRET"
	s := newSource(template, "one", dir)
	if len(s.profile()) != 64 || strings.Contains(s.profile(), "SUPERSECRET") || strings.Contains(s.dir, "SUPERSECRET") {
		t.Fatal("secret in profile/path")
	}
	if s.profile() != newSource(template, "one", t.TempDir()).profile() {
		t.Fatal("profile incorrectly depends on cache directory")
	}
	if s.profile() == newSource(template, "two", dir).profile() || s.profile() == newSource(strings.Replace(template, "SUPERSECRET", "other", 1), "one", dir).profile() {
		t.Fatal("provider/UA change did not change profile")
	}
	for _, xyz := range [][3]int{{-1, 0, 0}, {19, 0, 0}, {0, 1, 0}, {0, 0, 1}, {1, -1, 0}, {1, 0, -1}, {18, 1 << 18, 0}} {
		if _, err := s.tile(context.Background(), xyz[0], xyz[1], xyz[2], "", true); !errors.Is(err, errFitnessTileCoordinates) {
			t.Fatalf("coordinates %v: %v", xyz, err)
		}
	}
	t.Run("symlink-directory", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "linked-cache")
		fitnessTileTestSymlink(t, dir, link)
		if _, err := newFitnessTileSource(template, link, "one", &http.Client{Transport: transport}, time.Now); !errors.Is(err, errFitnessTileStorage) {
			t.Fatalf("symlink directory = %v", err)
		}
	})
}
