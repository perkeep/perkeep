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

package strava

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	rateLimitInterval              = 15 * time.Minute
	defaultOverallShortLimit int64 = 200
	defaultOverallDailyLimit int64 = 2000
	defaultReadShortLimit    int64 = 100
	defaultReadDailyLimit    int64 = 1000
)

// Strava quotas are per application, not per athlete, client, or import run.
// Keep this map for the life of the process, including across importer hosts.
var appRateLimiters = struct {
	sync.Mutex
	byID map[string]*rateLimiter
}{byID: make(map[string]*rateLimiter)}

func rateLimiterForApp(clientID string) *rateLimiter {
	appRateLimiters.Lock()
	defer appRateLimiters.Unlock()
	if limiter := appRateLimiters.byID[clientID]; limiter != nil {
		return limiter
	}
	limiter := newRateLimiter(time.Now)
	appRateLimiters.byID[clientID] = limiter
	return limiter
}

type rateLimitWindow struct {
	Start        time.Time `json:"start"`
	OverallLimit int64     `json:"overallLimit"`
	OverallUsage int64     `json:"overallUsage"`
	ReadLimit    int64     `json:"readLimit"`
	ReadUsage    int64     `json:"readUsage"`
}

// Persistence deliberately contains no credentials, athlete data, or ClientID.
// The caller associates it with its application and restores once at run start.
type rateLimitState struct {
	Version       int             `json:"version"`
	Short         rateLimitWindow `json:"short"`
	Daily         rateLimitWindow `json:"daily"`
	CooldownUntil time.Time       `json:"cooldownUntil"`
}

type rateLimiter struct {
	// Serialize snapshot plus persistent cache write without holding mu over
	// storage I/O. Otherwise an older snapshot could finish writing after a
	// newer one and weaken the application's restart budget.
	persistMu sync.Mutex
	// A semaphore rather than a mutex lets requests cancel while waiting for
	// another account's response. State and now are accessed under this lock.
	mu    chan struct{}
	now   func() time.Time
	state rateLimitState
}

func newRateLimiter(now func() time.Time) *rateLimiter {
	if now == nil {
		now = time.Now
	}
	t := now().UTC()
	limiter := &rateLimiter{
		mu: make(chan struct{}, 1), now: now,
		state: rateLimitState{
			Version: 1,
			Short: rateLimitWindow{
				Start: shortBucket(t), OverallLimit: defaultOverallShortLimit, ReadLimit: defaultReadShortLimit,
			},
			Daily: rateLimitWindow{
				Start: dailyBucket(t), OverallLimit: defaultOverallDailyLimit, ReadLimit: defaultReadDailyLimit,
			},
		},
	}
	limiter.mu <- struct{}{}
	return limiter
}

func shortBucket(t time.Time) time.Time { return t.UTC().Truncate(rateLimitInterval) }

func dailyBucket(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func (l *rateLimiter) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.mu:
		// Cancellation and lock readiness may race in select. Do not reserve
		// anything for an already canceled request, even if the lock won.
		if err := ctx.Err(); err != nil {
			l.unlock()
			return err
		}
		return nil
	}
}

func (l *rateLimiter) unlock() { l.mu <- struct{}{} }

func (l *rateLimiter) advance(now time.Time) {
	// A clock moving backwards must not reopen a quota already consumed.
	if start := shortBucket(now); start.After(l.state.Short.Start) {
		l.state.Short.Start = start
		l.state.Short.OverallUsage, l.state.Short.ReadUsage = 0, 0
	}
	if start := dailyBucket(now); start.After(l.state.Daily.Start) {
		l.state.Daily.Start = start
		l.state.Daily.OverallUsage, l.state.Daily.ReadUsage = 0, 0
	}
	if !now.Before(l.state.CooldownUntil) {
		l.state.CooldownUntil = time.Time{}
	}
}

// rateLimitError only includes local, validated data; never response text,
// headers, request URLs, application identifiers, or credentials.
type rateLimitError struct {
	reset  time.Time
	status int
}

func (e *rateLimitError) Error() string {
	reason := "rate limit reached for this application"
	if e.status == http.StatusTooManyRequests {
		reason = "rate limit exceeded (HTTP 429)"
	}
	return fmt.Sprintf("strava: %s; retry import at or after %s (UTC); check Strava application API settings",
		reason, e.reset.UTC().Format(time.RFC3339))
}

// RetryAfter returns when a run can go on; see importer.RetryAfterError.
func (e *rateLimitError) RetryAfter() time.Time { return e.reset }

// blockedUntil returns the latest applicable reset, since all four quotas and
// the cooldown must permit a request. Equality permits the last request.
func (l *rateLimiter) blockedUntil() time.Time {
	reset := l.state.CooldownUntil
	for _, w := range []struct {
		window rateLimitWindow
		period time.Duration
	}{
		{l.state.Short, rateLimitInterval},
		{l.state.Daily, 24 * time.Hour},
	} {
		if w.window.OverallUsage >= w.window.OverallLimit || w.window.ReadUsage >= w.window.ReadLimit {
			reset = later(reset, w.window.Start.Add(w.period))
		}
	}
	return reset
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

type rateLimitedTransport struct {
	limiter *rateLimiter
	base    http.RoundTripper
}

func (t *rateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	l := t.limiter
	if err := l.lock(req.Context()); err != nil {
		return nil, err
	}
	defer l.unlock()
	l.advance(l.now().UTC())
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if reset := l.blockedUntil(); !reset.IsZero() {
		return nil, &rateLimitError{reset: reset}
	}
	shortStart, dailyStart := l.state.Short.Start, l.state.Daily.Start
	l.state.Short.OverallUsage++
	l.state.Short.ReadUsage++
	l.state.Daily.OverallUsage++
	l.state.Daily.ReadUsage++
	// Serialize reservation through response headers, not just reservation.
	// Unknown usage from outside this process is discovered by the first
	// response; no local limiter can avoid that discovery request completely.
	if req.Method == http.MethodGet && (req.Body == nil || req.Body == http.NoBody) {
		// The standard Transport can otherwise silently replay a bodyless GET
		// on a reused connection after an EOF, consuming two server requests
		// for one reservation. A non-rewindable empty body disables replay:
		// Request.isReplayable is false and outgoingLength is unknown (-1).
		// HTTP/1's transferWriter probes the empty body and sends neither a
		// payload nor chunked framing; HTTP/2 also sends no payload bytes.
		// Keep the caller's request unchanged. Custom transports must likewise
		// honor this non-replayable request rather than retrying internally.
		singleAttempt := *req
		singleAttempt.Body = io.NopCloser(strings.NewReader(""))
		singleAttempt.GetBody = nil
		singleAttempt.ContentLength = 0
		req = &singleAttempt
	}
	res, err := t.base.RoundTrip(req)
	now := l.now().UTC()
	l.advance(now)
	// A request spanning a reset may have reached Strava in the new bucket,
	// even when the transport returns no response. Reserve there too before
	// merging any headers: failed requests must not reopen capacity.
	if !l.state.Short.Start.Equal(shortStart) {
		l.state.Short.OverallUsage++
		l.state.Short.ReadUsage++
	}
	if !l.state.Daily.Start.Equal(dailyStart) {
		l.state.Daily.OverallUsage++
		l.state.Daily.ReadUsage++
	}
	if res != nil {
		l.observeHeaders(res.Header)
		if res.StatusCode == http.StatusTooManyRequests {
			reset, ok := retryAfter(res.Header.Get("Retry-After"), now)
			if !ok {
				reset = l.state.Short.Start.Add(rateLimitInterval)
			}
			l.state.CooldownUntil = later(l.state.CooldownUntil, reset)
			if res.Body != nil {
				res.Body.Close()
			}
			return nil, &rateLimitError{reset: l.blockedUntil(), status: http.StatusTooManyRequests}
		}
		if err != nil {
			if res.Body != nil {
				res.Body.Close()
			}
			return nil, err
		}
	}
	// Transport failures retain the reservation until its natural reset: the
	// request may have reached Strava, even without any response.
	return res, err
}

func (l *rateLimiter) observeHeaders(h http.Header) {
	if short, daily, ok := headerPair(h, "X-RateLimit-Limit", true); ok {
		l.state.Short.OverallLimit, l.state.Daily.OverallLimit = short, daily
	} else {
		l.state.Short.OverallLimit = min(l.state.Short.OverallLimit, defaultOverallShortLimit)
		l.state.Daily.OverallLimit = min(l.state.Daily.OverallLimit, defaultOverallDailyLimit)
	}
	if short, daily, ok := headerPair(h, "X-ReadRateLimit-Limit", true); ok {
		l.state.Short.ReadLimit, l.state.Daily.ReadLimit = short, daily
	} else {
		l.state.Short.ReadLimit = min(l.state.Short.ReadLimit, defaultReadShortLimit)
		l.state.Daily.ReadLimit = min(l.state.Daily.ReadLimit, defaultReadDailyLimit)
	}
	if short, daily, ok := headerPair(h, "X-RateLimit-Usage", false); ok {
		l.state.Short.OverallUsage = max(l.state.Short.OverallUsage, short)
		l.state.Daily.OverallUsage = max(l.state.Daily.OverallUsage, daily)
	}
	if short, daily, ok := headerPair(h, "X-ReadRateLimit-Usage", false); ok {
		l.state.Short.ReadUsage = max(l.state.Short.ReadUsage, short)
		l.state.Daily.ReadUsage = max(l.state.Daily.ReadUsage, daily)
	}
}

func headerPair(h http.Header, key string, positive bool) (int64, int64, bool) {
	values := h.Values(key)
	if len(values) != 1 {
		return 0, 0, false
	}
	parts := strings.Split(values[0], ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	a, okA := unsignedDecimal(strings.TrimSpace(parts[0]))
	b, okB := unsignedDecimal(strings.TrimSpace(parts[1]))
	if !okA || !okB || (positive && (a == 0 || b == 0)) {
		return 0, 0, false
	}
	return a, b, true
}

func unsignedDecimal(s string) (int64, bool) {
	if len(s) == 0 || len(s) > 19 {
		return 0, false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

func retryAfter(raw string, now time.Time) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if seconds, ok := unsignedDecimal(raw); ok {
		if seconds > 0 && seconds <= int64((1<<63-1)/time.Second) {
			reset := now.Add(time.Duration(seconds) * time.Second)
			if reset.Year() <= 9999 {
				return reset, true
			}
		}
		return time.Time{}, false
	}
	reset, err := http.ParseTime(raw)
	if err == nil && reset.After(now) {
		return reset.UTC(), true
	}
	return time.Time{}, false
}

// snapshot is suitable for the shared importer scratch cache. Save after every
// run, including failed runs. A missing cache or abrupt process termination
// before a save loses local reservations; the next response must rediscover
// server usage, just as for external applications sharing the same ClientID.
func (l *rateLimiter) snapshot() string {
	<-l.mu
	defer l.unlock()
	l.advance(l.now().UTC())
	raw, _ := json.Marshal(l.state) // Validated state contains only integers and UTC times.
	return string(raw)
}

// restore is a conservative, idempotent merge, not replacement. Old buckets
// cannot restore counters into a new window; concurrent accounts' snapshots
// cannot lower current counters or erase cooldowns. The caller must verify
// that the state belongs to its current ClientID before calling restore.
func (l *rateLimiter) restore(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	invalid := errors.New("strava: invalid cached rate limit state")
	if len(raw) > 4096 {
		return invalid
	}
	var state rateLimitState
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&state); err != nil {
		return invalid
	}
	if d.Decode(new(any)) != io.EOF {
		return invalid
	}
	if state.Version != 1 || !validWindow(state.Short, true) || !validWindow(state.Daily, false) ||
		!dailyBucket(state.Short.Start).Equal(state.Daily.Start) ||
		(!state.CooldownUntil.IsZero() && state.CooldownUntil.Year() < 1) {
		return invalid
	}
	<-l.mu
	defer l.unlock()
	now := l.now().UTC()
	if state.Short.Start.After(shortBucket(now)) || state.Daily.Start.After(dailyBucket(now)) {
		return invalid
	}
	l.advance(now)
	mergeWindow := func(dst *rateLimitWindow, src rateLimitWindow) {
		if !dst.Start.Equal(src.Start) {
			return
		}
		dst.OverallLimit = min(dst.OverallLimit, src.OverallLimit)
		dst.ReadLimit = min(dst.ReadLimit, src.ReadLimit)
		dst.OverallUsage = max(dst.OverallUsage, src.OverallUsage)
		dst.ReadUsage = max(dst.ReadUsage, src.ReadUsage)
	}
	mergeWindow(&l.state.Short, state.Short)
	mergeWindow(&l.state.Daily, state.Daily)
	if state.CooldownUntil.After(now) {
		l.state.CooldownUntil = later(l.state.CooldownUntil, state.CooldownUntil.UTC())
	}
	return nil
}

func validWindow(w rateLimitWindow, short bool) bool {
	if w.Start.IsZero() || w.Start.Year() < 1 || w.OverallLimit <= 0 || w.ReadLimit <= 0 || w.OverallUsage < 0 || w.ReadUsage < 0 {
		return false
	}
	if short {
		return w.Start.Equal(shortBucket(w.Start))
	}
	return w.Start.Equal(dailyBucket(w.Start))
}
