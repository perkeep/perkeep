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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"

	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	fitnessTileDefaultURL    = "https://tile.openstreetmap.org/{z}/{x}/{y}.png"
	fitnessTileUserAgent     = "Perkeep/fitness-thumbnails (+https://perkeep.org/)"
	fitnessTileMaxBytes      = 1 << 20
	fitnessThumbnailMaxBytes = 20 << 20
	fitnessTileMetaMaxBytes  = 32 << 10
	fitnessTileDefaultTTL    = 7 * 24 * time.Hour
	fitnessTileTimeout       = 5 * time.Second
)

// No underlying error, URL, coordinates, credentials or upstream response body
// is included in these errors. In particular, http.Client errors contain URLs.
type fitnessTileError string

func (e fitnessTileError) Error() string { return string(e) }

const (
	errFitnessTileConfig      fitnessTileError = "invalid fitness tile configuration"
	errFitnessTileCoordinates fitnessTileError = "invalid fitness tile request"
	errFitnessTileMiss        fitnessTileError = "fitness tile not cached"
	errFitnessTileUpstream    fitnessTileError = "fitness tile provider unavailable"
	errFitnessTileCooldown    fitnessTileError = "fitness tile provider cooling down"
	errFitnessTileInvalid     fitnessTileError = "invalid fitness tile image"
	errFitnessTileStorage     fitnessTileError = "fitness tile cache unavailable"
)

type fitnessTile struct {
	Image     image.Image
	Digest    string
	Expires   time.Time
	Cacheable bool
}

type fitnessTileSource struct {
	template, dir, userAgent, fingerprint string
	client                                *http.Client
	now                                   func() time.Time
	limiter                               *fitnessTileLimiter
	mu                                    sync.Mutex
	flights                               map[string]*fitnessTileFlight
}

type fitnessTileFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	tile    fitnessTile
	err     error
}

var fitnessTileSources = struct {
	sync.Mutex
	m map[string]*fitnessTileSource
}{m: make(map[string]*fitnessTileSource)}

// The limiter is shared by all providers/profiles in this application process.
// It does not run any background jobs and never reserves future request bursts.
var fitnessTilesLimiter = &fitnessTileLimiter{slots: make(chan struct{}, 2), interval: time.Second}

type fitnessTileLimiter struct {
	mu             sync.Mutex
	slots          chan struct{}
	interval       time.Duration
	last, cooldown time.Time
}

func (l *fitnessTileLimiter) acquire(ctx context.Context) (func(), error) {
	select {
	case l.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-l.slots }
	for {
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		l.mu.Lock()
		now := time.Now()
		if now.Before(l.cooldown) {
			l.mu.Unlock()
			release()
			return nil, errFitnessTileCooldown
		}
		wait := l.last.Add(l.interval).Sub(now)
		if wait <= 0 {
			l.last = now
			l.mu.Unlock()
			return release, nil
		}
		l.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			release()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *fitnessTileLimiter) backoff(retryAfter string) {
	now := time.Now()
	until := now.Add(time.Minute)
	if seconds, err := strconv.ParseInt(strings.TrimSpace(retryAfter), 10, 64); err == nil && seconds >= 0 && seconds <= int64((365*24*time.Hour)/time.Second) {
		until = now.Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(retryAfter); err == nil && date.After(now) {
		until = date
	}
	// A zero/past Retry-After still must stop an immediate burst, not retry.
	if until.Before(now.Add(time.Second)) {
		until = now.Add(time.Second)
	}
	l.mu.Lock()
	if until.After(l.cooldown) {
		l.cooldown = until
	}
	l.mu.Unlock()
}

// OpenStreetMap is the default; "off" disables tiles. Construction only touches
// the private filesystem: importing tracks and indexing do not fetch maps.
func fitnessTileSourceFromEnv() (*fitnessTileSource, error) {
	template := os.Getenv("PERKEEP_FITNESS_TILE_URL")
	switch template {
	case "":
		template = fitnessTileDefaultURL
	case "off":
		return nil, nil
	}
	dir := os.Getenv("PERKEEP_FITNESS_TILE_CACHE_DIR")
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil, errFitnessTileStorage
		}
		dir = filepath.Join(base, "perkeep", "fitness-tiles")
	}
	ua := os.Getenv("PERKEEP_FITNESS_TILE_USER_AGENT")
	if ua == "" {
		ua = fitnessTileUserAgent
	}
	key := fitnessTileHash(template + "\x00" + dir + "\x00" + ua)
	fitnessTileSources.Lock()
	defer fitnessTileSources.Unlock()
	if source := fitnessTileSources.m[key]; source != nil {
		return source, nil
	}
	source, err := newFitnessTileSource(template, dir, ua, nil, nil)
	if err != nil {
		return nil, err
	}
	fitnessTileSources.m[key] = source
	return source, nil
}

// The injectable constructor never reads environment or host configuration.
// Callers may supply a fake transport and clock for entirely offline tests.
func newFitnessTileSource(template, dir, ua string, client *http.Client, now func() time.Time) (*fitnessTileSource, error) {
	if ua == "" {
		ua = fitnessTileUserAgent
	}
	if strings.ContainsAny(ua, "\r\n\x00") || !fitnessTileTemplateValid(template) || dir == "" {
		return nil, errFitnessTileConfig
	}
	if now == nil {
		now = time.Now
	}
	fingerprint := fitnessTileHash("fitness-raster-v1:256:png\x00" + template + "\x00" + ua)
	// Clone the client; never inherit a cookie jar, redirect credentials or a
	// caller's timeout. Even same-host redirects are rejected (no auto-retries).
	c := http.Client{}
	if client != nil {
		c = *client
	}
	c.Timeout = fitnessTileTimeout
	c.Jar = nil
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	s := &fitnessTileSource{template: template, dir: filepath.Join(dir, fingerprint), userAgent: ua, fingerprint: fingerprint, client: &c, now: now, limiter: fitnessTilesLimiter, flights: make(map[string]*fitnessTileFlight)}
	if fitnessTilePrivateDir(dir) != nil || fitnessTilePrivateDir(s.dir) != nil {
		return nil, errFitnessTileStorage
	}
	return s, nil
}

func fitnessTileTemplateValid(template string) bool {
	check := template
	for _, field := range []string{"z", "x", "y"} {
		placeholder := "{" + field + "}"
		if strings.Count(check, placeholder) != 1 {
			return false
		}
		check = strings.ReplaceAll(check, placeholder, "0")
	}
	if strings.ContainsAny(check, "{}\r\n\x00") {
		return false
	}
	u, err := url.Parse(check)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	// Placeholder substitution must never choose a host or authentication data.
	original, err := url.Parse(template)
	if err != nil || original.Host != u.Host || original.Scheme != u.Scheme {
		return false
	}
	// The public OSM service specifically requires its canonical HTTPS URL.
	if strings.EqualFold(u.Hostname(), "tile.openstreetmap.org") && template != fitnessTileDefaultURL {
		return false
	}
	return true
}

func (s *fitnessTileSource) profile() string { return s.fingerprint }
func fitnessTileHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
func fitnessTileDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *fitnessTileSource) tile(ctx context.Context, z, x, y int, referer string, allowNetwork bool) (fitnessTile, error) {
	if err := ctx.Err(); err != nil {
		return fitnessTile{}, err
	}
	if z < 0 || z > 18 || x < 0 || y < 0 || x >= 1<<uint(z) || y >= 1<<uint(z) {
		return fitnessTile{}, errFitnessTileCoordinates
	}
	key := fitnessTileHash(fmt.Sprintf("%d/%d/%d", z, x, y))
	if rec, data, ok := s.readRecord("tile", key, fitnessTileMaxBytes); ok && !rec.NoCache && s.now().Before(rec.Expires) {
		return fitnessTileDecode(data, rec)
	}
	if !allowNetwork {
		return fitnessTile{}, errFitnessTileMiss
	}
	// A shared request belongs to its remaining waiters, not to the first
	// thumbnail's context. Cancel upstream as soon as the last waiter leaves.
	s.mu.Lock()
	f := s.flights[key]
	if f == nil {
		fetchCtx, cancel := context.WithCancel(context.Background())
		f = &fitnessTileFlight{done: make(chan struct{}), cancel: cancel}
		s.flights[key] = f
		go func() {
			result, err := s.fetch(fetchCtx, key, z, x, y, referer)
			s.mu.Lock()
			f.tile, f.err = result, err
			if s.flights[key] == f {
				delete(s.flights, key)
			}
			close(f.done)
			cancel()
			s.mu.Unlock()
		}()
	}
	f.waiters++
	s.mu.Unlock()
	select {
	case <-f.done:
		if err := ctx.Err(); err != nil {
			return fitnessTile{}, err
		}
		return f.tile, f.err
	case <-ctx.Done():
		s.mu.Lock()
		f.waiters--
		if f.waiters == 0 {
			f.cancel()
			if s.flights[key] == f {
				delete(s.flights, key)
			}
		}
		s.mu.Unlock()
		return fitnessTile{}, ctx.Err()
	}
}

func (s *fitnessTileSource) fetch(ctx context.Context, key string, z, x, y int, referer string) (fitnessTile, error) {
	previous, data, cached := s.readRecord("tile", key, fitnessTileMaxBytes)
	if cached && !previous.NoCache && s.now().Before(previous.Expires) {
		return fitnessTileDecode(data, previous)
	}
	release, err := s.limiter.acquire(ctx)
	if err != nil {
		return fitnessTile{}, err
	}
	defer release()
	urlString := strings.NewReplacer("{z}", strconv.Itoa(z), "{x}", strconv.Itoa(x), "{y}", strconv.Itoa(y)).Replace(s.template)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlString, nil)
	if err != nil {
		return fitnessTile{}, errFitnessTileConfig
	}
	// A nil-body GET is replayable by net/http after a reused connection
	// fails, silently making multiple upstream requests for one rate-limit
	// acquisition. This empty, non-rewindable body disables that replay.
	// HTTP/1 probes the body and emits no payload or chunked framing; HTTP/2
	// likewise emits no payload. Custom transports must not retry it either.
	req.Body = io.NopCloser(strings.NewReader(""))
	req.GetBody = nil
	req.ContentLength = 0
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("Accept", "image/png")
	if referer != "" {
		u, parseErr := url.Parse(referer)
		if parseErr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			return fitnessTile{}, errFitnessTileConfig
		}
		// Preserve the accurate UI Referer, including its normal query. Fragments
		// are never part of an HTTP Referer. The caller supplies a UI page URL,
		// not a GPX/Strava URL, activity name, token, or track coordinates.
		u.Fragment = ""
		req.Header.Set("Referer", u.String())
	}
	if cached {
		if previous.Headers.ETag != "" {
			req.Header.Set("If-None-Match", previous.Headers.ETag)
		}
		if previous.Headers.LastModified != "" {
			req.Header.Set("If-Modified-Since", previous.Headers.LastModified)
		}
	}
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fitnessTile{}, ctx.Err()
		}
		return fitnessTile{}, errFitnessTileUpstream
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		s.limiter.backoff(resp.Header.Get("Retry-After"))
		return fitnessTile{}, errFitnessTileCooldown
	}
	now := s.now()
	var rec fitnessTileRecord
	switch resp.StatusCode {
	case http.StatusNotModified:
		if !cached {
			return fitnessTile{}, errFitnessTileUpstream
		}
		headers := previous.Headers.merge304(resp.Header)
		rec = fitnessTilePolicy(headers, now)
	case http.StatusOK:
		if resp.ContentLength > fitnessTileMaxBytes {
			return fitnessTile{}, errFitnessTileInvalid
		}
		data, err = io.ReadAll(io.LimitReader(resp.Body, fitnessTileMaxBytes+1))
		if err != nil {
			if ctx.Err() != nil {
				return fitnessTile{}, ctx.Err()
			}
			return fitnessTile{}, errFitnessTileUpstream
		}
		if len(data) > fitnessTileMaxBytes {
			return fitnessTile{}, errFitnessTileInvalid
		}
		rec = fitnessTilePolicy(fitnessTileHeadersFromHTTP(resp.Header), now)
	default:
		return fitnessTile{}, errFitnessTileUpstream
	}
	rec.Digest = fitnessTileDigest(data)
	result, err := fitnessTileDecode(data, rec)
	if err != nil {
		return fitnessTile{}, err
	}
	if rec.NoStore {
		_ = os.Remove(s.recordPath("tile", key))
	} else if err := s.writeRecord("tile", key, rec, data); err != nil {
		// Do not repeatedly download uncached tiles from a broken/full cache.
		return fitnessTile{}, errFitnessTileStorage
	}
	return result, nil
}

func fitnessTileDecode(data []byte, rec fitnessTileRecord) (fitnessTile, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != 256 || cfg.Height != 256 {
		return fitnessTile{}, errFitnessTileInvalid
	}
	im, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return fitnessTile{}, errFitnessTileInvalid
	}
	// Cacheable describes derived thumbnails, not the raw validation cache.
	// A no-cache tile may retain its bytes/validators on disk, but a composite
	// must never bypass that mandatory upstream validation.
	return fitnessTile{Image: im, Digest: rec.Digest, Expires: rec.Expires, Cacheable: !rec.NoStore && !rec.NoCache}, nil
}

type fitnessTileHeaders struct {
	CacheControl string
	Expires      string
	Date         string
	Age          string
	ETag         string
	LastModified string
	Vary         string
}

func fitnessTileHeadersFromHTTP(h http.Header) fitnessTileHeaders {
	return fitnessTileHeaders{CacheControl: strings.Join(h.Values("Cache-Control"), ","), Expires: h.Get("Expires"), Date: h.Get("Date"), Age: h.Get("Age"), ETag: h.Get("ETag"), LastModified: h.Get("Last-Modified"), Vary: strings.Join(h.Values("Vary"), ",")}
}

func (h fitnessTileHeaders) merge304(update http.Header) fitnessTileHeaders {
	// Freshness directives and validators survive an otherwise empty 304. Date
	// and Age describe this response, not the age of the original response.
	n := fitnessTileHeadersFromHTTP(update)
	if _, ok := update["Cache-Control"]; ok {
		h.CacheControl = n.CacheControl
	}
	if _, ok := update["Expires"]; ok {
		h.Expires = n.Expires
	}
	if _, ok := update["ETag"]; ok {
		h.ETag = n.ETag
	}
	if _, ok := update["Last-Modified"]; ok {
		h.LastModified = n.LastModified
	}
	if _, ok := update["Vary"]; ok {
		h.Vary = n.Vary
	}
	h.Date, h.Age = n.Date, n.Age
	return h
}

type fitnessTileRecord struct {
	Version   int
	Kind      string
	Digest    string
	Integrity string
	Expires   time.Time
	NoCache   bool
	NoStore   bool
	Headers   fitnessTileHeaders
}

func fitnessTileRecordIntegrity(rec fitnessTileRecord) string {
	rec.Integrity = ""
	meta, err := json.Marshal(rec)
	if err != nil {
		return ""
	}
	return fitnessTileDigest(meta)
}

// This is a private HTTP cache, so max-age (not s-maxage) controls freshness.
// Honor server lifetime, validators and Age; only absent lifetime information
// uses OSM's minimum seven-day heuristic. No-cache is stored for validation but
// is never served without validation, and no-store is never persisted.
func fitnessTilePolicy(h fitnessTileHeaders, now time.Time) fitnessTileRecord {
	r := fitnessTileRecord{Headers: h}
	ttl := fitnessTileDefaultTTL
	maxAge := false
	for _, item := range strings.Split(h.CacheControl, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		switch strings.ToLower(parts[0]) {
		case "no-store":
			r.NoStore = true
		case "no-cache":
			r.NoCache = true
		case "max-age":
			seconds := int64(0)
			if len(parts) == 2 {
				value, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(parts[1]), "\""), 10, 64)
				if err == nil && value >= 0 && value <= int64((100*365*24*time.Hour)/time.Second) {
					seconds = value
				}
			}
			candidate := time.Duration(seconds) * time.Second
			if !maxAge || candidate < ttl {
				ttl = candidate
			}
			maxAge = true
		}
	}
	date, dateErr := http.ParseTime(h.Date)
	age := time.Duration(0)
	invalidAge := false
	if h.Age != "" {
		seconds, err := strconv.ParseInt(h.Age, 10, 64)
		if err != nil || seconds < 0 || seconds > int64((100*365*24*time.Hour)/time.Second) {
			invalidAge = true
		} else {
			age = time.Duration(seconds) * time.Second
		}
	}
	if dateErr == nil && now.Sub(date) > age {
		age = now.Sub(date)
	}
	if !maxAge && h.Expires != "" {
		if expires, err := http.ParseTime(h.Expires); err == nil {
			if dateErr == nil {
				ttl = expires.Sub(date)
			} else {
				ttl = expires.Sub(now)
			}
		} else {
			ttl = 0
		}
	}
	for _, field := range strings.Split(h.Vary, ",") {
		// These request headers are constant within this provider profile.
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "", "accept", "accept-encoding", "user-agent":
		default:
			r.NoStore = true
		}
	}
	ttl -= age
	if ttl < 0 || invalidAge || r.NoCache || r.NoStore {
		ttl = 0
	}
	r.Expires = now.Add(ttl)
	return r
}

// Unix cache directories and records use owner-only permission bits. Windows
// Chmod only controls the read-only attribute, not access permissions: the
// administrator must place the cache in a directory with a private Windows ACL.
func fitnessTilePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errFitnessTileStorage
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errFitnessTileStorage
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return errFitnessTileStorage
	}
	return nil
}

func fitnessTilePrivateMode(mode os.FileMode) bool {
	return runtime.GOOS == "windows" || mode.Perm()&0077 == 0
}

func (s *fitnessTileSource) recordPath(kind, key string) string {
	return filepath.Join(s.dir, kind+"-"+fitnessTileHash(key)+".cache")
}

// One bounded, digest-checked envelope makes metadata + PNG replacement atomic.
// Cache names and metadata never persist the provider URL or its API keys.
func (s *fitnessTileSource) readRecord(kind, key string, limit int64) (fitnessTileRecord, []byte, bool) {
	var rec fitnessTileRecord
	path := s.recordPath(kind, key)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || !fitnessTilePrivateMode(info.Mode()) || info.Size() > limit+fitnessTileMetaMaxBytes+4 {
		return rec, nil, false
	}
	file, err := fitnessTileOpenFile(path)
	if err != nil {
		return rec, nil, false
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !fitnessTilePrivateMode(opened.Mode()) || opened.Size() > limit+fitnessTileMetaMaxBytes+4 || !os.SameFile(info, opened) {
		return rec, nil, false
	}
	var prefix [4]byte
	if _, err := io.ReadFull(file, prefix[:]); err != nil {
		return rec, nil, false
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || n > fitnessTileMetaMaxBytes {
		return rec, nil, false
	}
	meta := make([]byte, n)
	if _, err := io.ReadFull(file, meta); err != nil || json.Unmarshal(meta, &rec) != nil || rec.Version != 1 || rec.Kind != kind || rec.NoStore || rec.Integrity != fitnessTileRecordIntegrity(rec) {
		return rec, nil, false
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit || rec.Digest != fitnessTileDigest(data) {
		return rec, nil, false
	}
	// Validate even stale data before offering it to conditional revalidation.
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return rec, nil, false
	}
	if kind == "tile" {
		if cfg.Width != 256 || cfg.Height != 256 {
			return rec, nil, false
		}
	} else if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 2000 || cfg.Height > 2000 {
		return rec, nil, false
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return rec, nil, false
	}
	return rec, data, true
}

func (s *fitnessTileSource) writeRecord(kind, key string, rec fitnessTileRecord, data []byte) error {
	rec.Version, rec.Kind = 1, kind
	rec.Integrity = fitnessTileRecordIntegrity(rec)
	meta, err := json.Marshal(rec)
	if err != nil || len(meta) > fitnessTileMetaMaxBytes {
		return errFitnessTileStorage
	}
	file, err := os.CreateTemp(s.dir, ".fitness-tile-")
	if err != nil {
		return errFitnessTileStorage
	}
	name := file.Name()
	defer os.Remove(name)
	ok := false
	defer func() {
		if !ok {
			file.Close()
		}
	}()
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(meta)))
	for _, part := range [][]byte{prefix[:], meta, data} {
		if _, err := file.Write(part); err != nil {
			return errFitnessTileStorage
		}
	}
	if err := file.Sync(); err != nil {
		return errFitnessTileStorage
	}
	if err := file.Close(); err != nil {
		return errFitnessTileStorage
	}
	ok = true
	if err := fitnessTileReplaceFile(s.dir, filepath.Base(name), filepath.Base(s.recordPath(kind, key))); err != nil {
		return errFitnessTileStorage
	}
	return nil
}

func (s *fitnessTileSource) cachedThumbnail(key string) ([]byte, time.Time, bool) {
	rec, data, ok := s.readRecord("thumbnail", key, fitnessThumbnailMaxBytes)
	if !ok || rec.NoCache || !s.now().Before(rec.Expires) {
		return nil, time.Time{}, false
	}
	return data, rec.Expires, true
}

// The composer supplies the earliest tile expiry and must not call this when
// any tile was no-store. Expired/no-cache composites cannot be written at all.
func (s *fitnessTileSource) storeThumbnail(key string, data []byte, expires time.Time) error {
	if !s.now().Before(expires) {
		return errFitnessTileStorage
	}
	if len(data) > fitnessThumbnailMaxBytes {
		return errFitnessTileInvalid
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 2000 || cfg.Height > 2000 {
		return errFitnessTileInvalid
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return errFitnessTileInvalid
	}
	return s.writeRecord("thumbnail", key, fitnessTileRecord{Expires: expires, Digest: fitnessTileDigest(data)}, data)
}
