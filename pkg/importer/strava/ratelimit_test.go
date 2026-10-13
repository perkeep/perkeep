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
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type rateTestTransport func(*http.Request) (*http.Response, error)

var rateTestAppSequence atomic.Int64

func (f rateTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func rateTestResponse(status int, headers http.Header) *http.Response {
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(`{"id":42}`))}
}

func rateTestTime() time.Time { return time.Date(2026, 1, 2, 12, 7, 0, 0, time.UTC) }

func rateTestState(t *testing.T, l *rateLimiter) rateLimitState {
	t.Helper()
	var state rateLimitState
	if err := json.Unmarshal([]byte(l.snapshot()), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func rateTestRoundTrip(l *rateLimiter, base http.RoundTripper) error {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, apiBaseURL+"/activities/42", nil)
	res, err := (&rateLimitedTransport{limiter: l, base: base}).RoundTrip(req)
	if res != nil {
		res.Body.Close()
	}
	return err
}

func rateTestQuotaError(t *testing.T, err error, reset time.Time) {
	t.Helper()
	var quota *rateLimitError
	if !errors.As(err, &quota) || !quota.reset.Equal(reset) {
		t.Fatalf("error = %v, want rateLimitError resetting %s", err, reset)
	}
	if !strings.Contains(err.Error(), reset.UTC().Format(time.RFC3339)) ||
		!strings.Contains(err.Error(), "retry import") || !strings.Contains(err.Error(), "application") {
		t.Errorf("error not actionable: %v", err)
	}
}

func TestRateLimiterDefaultQuotaWithoutHeaders(t *testing.T) {
	now := rateTestTime()
	l := newRateLimiter(func() time.Time { return now })
	calls := 0
	base := rateTestTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return rateTestResponse(200, nil), nil
	})
	for i := 0; i < 100; i++ {
		if err := rateTestRoundTrip(l, base); err != nil {
			t.Fatalf("request %d failed: %v", i+1, err)
		}
	}
	rateTestQuotaError(t, rateTestRoundTrip(l, base), shortBucket(now).Add(rateLimitInterval))
	state := rateTestState(t, l)
	if calls != 100 || state.Short.OverallLimit != 200 || state.Daily.OverallLimit != 2000 ||
		state.Short.ReadLimit != 100 || state.Daily.ReadLimit != 1000 ||
		state.Short.OverallUsage != 100 || state.Short.ReadUsage != 100 ||
		state.Daily.OverallUsage != 100 || state.Daily.ReadUsage != 100 {
		t.Fatalf("calls = %d, state = %+v", calls, state)
	}
}

func TestRateLimiterEachQuotaAndLastRequestSucceeds(t *testing.T) {
	for _, quota := range []string{"overallShort", "readShort", "overallDaily", "readDaily"} {
		t.Run(quota, func(t *testing.T) {
			now := rateTestTime()
			l := newRateLimiter(func() time.Time { return now })
			reset := shortBucket(now).Add(rateLimitInterval)
			switch quota {
			case "overallShort":
				l.state.Short.OverallLimit = 2
			case "readShort":
				l.state.Short.ReadLimit = 2
			case "overallDaily":
				l.state.Daily.OverallLimit = 2
				reset = dailyBucket(now).Add(24 * time.Hour)
			case "readDaily":
				l.state.Daily.ReadLimit = 2
				reset = dailyBucket(now).Add(24 * time.Hour)
			}
			calls := 0
			c := &apiClient{accessToken: "access-secret", limiter: l, httpClient: &http.Client{
				Transport: rateTestTransport(func(*http.Request) (*http.Response, error) {
					calls++
					return rateTestResponse(200, nil), nil
				}),
			}}
			for i := 0; i < 2; i++ {
				if _, _, err := c.getActivity(context.Background(), 42); err != nil {
					t.Fatalf("allowed request %d: %v", i+1, err)
				}
			}
			_, _, err := c.getActivity(context.Background(), 42)
			rateTestQuotaError(t, err, reset)
			if calls != 2 || strings.Contains(err.Error(), "secret") {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
}

func TestRateLimiterHeaderUpdatesAndExternalUsage(t *testing.T) {
	now := rateTestTime()
	l := newRateLimiter(func() time.Time { return now })
	header := make(http.Header)
	header.Set("X-RateLimit-Limit", "7, 80")
	header.Set("X-RateLimit-Usage", "4,21")
	header.Set("X-ReadRateLimit-Limit", "3,30")
	header.Set("X-ReadRateLimit-Usage", "2,18")
	calls := 0
	base := rateTestTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return rateTestResponse(200, header), nil
	})
	if err := rateTestRoundTrip(l, base); err != nil {
		t.Fatal(err)
	}
	state := rateTestState(t, l)
	if state.Short.OverallLimit != 7 || state.Daily.OverallLimit != 80 ||
		state.Short.ReadLimit != 3 || state.Daily.ReadLimit != 30 ||
		state.Short.OverallUsage != 4 || state.Daily.OverallUsage != 21 ||
		state.Short.ReadUsage != 2 || state.Daily.ReadUsage != 18 {
		t.Fatalf("state = %+v", state)
	}
	// Lower observed usage cannot cancel our reservations.
	header.Set("X-RateLimit-Usage", "0,0")
	header.Set("X-ReadRateLimit-Usage", "0,0")
	if err := rateTestRoundTrip(l, base); err != nil {
		t.Fatal(err)
	}
	rateTestQuotaError(t, rateTestRoundTrip(l, base), shortBucket(now).Add(rateLimitInterval))
	state = rateTestState(t, l)
	if calls != 2 || state.Short.ReadUsage != 3 || state.Short.OverallUsage != 5 ||
		state.Daily.ReadUsage != 19 || state.Daily.OverallUsage != 22 {
		t.Fatalf("calls = %d, state = %+v", calls, state)
	}
}

func TestRateLimiterAcceptsAnnouncedIncreases(t *testing.T) {
	l := newRateLimiter(rateTestTime)
	h := make(http.Header)
	h.Set("X-RateLimit-Limit", "400,4000")
	h.Set("X-ReadRateLimit-Limit", "200,2000")
	l.observeHeaders(h)
	state := rateTestState(t, l)
	if state.Short.OverallLimit != 400 || state.Daily.OverallLimit != 4000 ||
		state.Short.ReadLimit != 200 || state.Daily.ReadLimit != 2000 {
		t.Fatalf("state = %+v", state)
	}
	// Missing limits fall back to defaults, never indefinite old higher limits.
	l.observeHeaders(nil)
	state = rateTestState(t, l)
	if state.Short.OverallLimit != 200 || state.Daily.OverallLimit != 2000 ||
		state.Short.ReadLimit != 100 || state.Daily.ReadLimit != 1000 {
		t.Fatalf("missing header state = %+v", state)
	}
}

func TestRateLimiterMalformedHeadersAreConservative(t *testing.T) {
	for _, raw := range []string{"", "0,0", "-1,2", "1,-2", "+1,2", "1", "1,2,3",
		"NaN,Infinity", "1.5,2", "9223372036854775808,1", "1,999999999999999999999", "secret,2"} {
		t.Run(raw, func(t *testing.T) {
			l := newRateLimiter(rateTestTime)
			h := make(http.Header)
			for _, key := range []string{"X-RateLimit-Limit", "X-RateLimit-Usage", "X-ReadRateLimit-Limit", "X-ReadRateLimit-Usage"} {
				h.Set(key, raw)
			}
			if err := rateTestRoundTrip(l, rateTestTransport(func(*http.Request) (*http.Response, error) {
				return rateTestResponse(200, h), nil
			})); err != nil {
				t.Fatal(err)
			}
			s := rateTestState(t, l)
			if s.Short.OverallLimit != 200 || s.Daily.OverallLimit != 2000 ||
				s.Short.ReadLimit != 100 || s.Daily.ReadLimit != 1000 ||
				s.Short.OverallUsage != 1 || s.Short.ReadUsage != 1 || s.Daily.OverallUsage != 1 || s.Daily.ReadUsage != 1 {
				t.Fatalf("state = %+v", s)
			}
		})
	}
	// Invalid limits must not suppress independently valid usage headers.
	l := newRateLimiter(rateTestTime)
	h := make(http.Header)
	h.Set("X-RateLimit-Limit", "-5,2000")
	h.Set("X-RateLimit-Usage", "199,1500")
	h.Set("X-ReadRateLimit-Limit", "0,1000")
	h.Set("X-ReadRateLimit-Usage", "99,800")
	l.observeHeaders(h)
	s := rateTestState(t, l)
	if s.Short.OverallUsage != 199 || s.Daily.OverallUsage != 1500 || s.Short.ReadUsage != 99 || s.Daily.ReadUsage != 800 {
		t.Fatalf("state = %+v", s)
	}
	// Multiple ambiguous limit header fields are not accepted.
	h["X-Ratelimit-Limit"] = []string{"500,5000", "10000,100000"}
	l.observeHeaders(h)
	if l.state.Short.OverallLimit != 200 {
		t.Fatal("duplicate limit headers accepted")
	}
}

func TestRateLimiterNaturalQuarterResets(t *testing.T) {
	base := rateTestTransport(func(*http.Request) (*http.Response, error) { return rateTestResponse(200, nil), nil })
	for _, minute := range []int{0, 15, 30, 45} {
		t.Run(time.Date(2026, 1, 2, 12, minute, 0, 0, time.UTC).Format("15:04"), func(t *testing.T) {
			now := time.Date(2026, 1, 2, 12, minute+7, 0, 0, time.FixedZone("offset", -7*3600))
			l := newRateLimiter(func() time.Time { return now })
			l.state.Short.ReadLimit = 1
			if err := rateTestRoundTrip(l, base); err != nil {
				t.Fatal(err)
			}
			reset := shortBucket(now).Add(rateLimitInterval)
			now = reset.Add(-time.Nanosecond)
			rateTestQuotaError(t, rateTestRoundTrip(l, base), reset)
			now = reset
			if err := rateTestRoundTrip(l, base); err != nil {
				t.Fatal(err)
			}
			s := rateTestState(t, l)
			if s.Short.ReadUsage != 1 || s.Daily.ReadUsage != 2 || !s.Short.Start.Equal(reset) {
				t.Fatalf("state after reset = %+v", s)
			}
		})
	}
}

func TestRateLimiterUTCDateResetAndDailyExhaustion(t *testing.T) {
	now := time.Date(2026, 1, 2, 23, 44, 0, 0, time.UTC).In(time.FixedZone("offset", 5*3600))
	l := newRateLimiter(func() time.Time { return now })
	l.state.Daily.ReadLimit = 1
	reset := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	base := rateTestTransport(func(*http.Request) (*http.Response, error) { return rateTestResponse(200, nil), nil })
	if err := rateTestRoundTrip(l, base); err != nil {
		t.Fatal(err)
	}
	rateTestQuotaError(t, rateTestRoundTrip(l, base), reset)
	now = now.Add(2 * time.Minute) // A quarter reset does not reopen daily quota.
	rateTestQuotaError(t, rateTestRoundTrip(l, base), reset)
	now = reset.Add(-time.Nanosecond)
	rateTestQuotaError(t, rateTestRoundTrip(l, base), reset)
	now = reset
	if err := rateTestRoundTrip(l, base); err != nil {
		t.Fatal(err)
	}
	s := rateTestState(t, l)
	if s.Short.ReadUsage != 1 || s.Daily.ReadUsage != 1 || !s.Daily.Start.Equal(reset) {
		t.Fatalf("state = %+v", s)
	}
}

func TestRateLimiterBackwardClockDoesNotFreeQuota(t *testing.T) {
	now := rateTestTime()
	l := newRateLimiter(func() time.Time { return now })
	l.state.Short.ReadLimit = 1
	base := rateTestTransport(func(*http.Request) (*http.Response, error) { return rateTestResponse(200, nil), nil })
	if err := rateTestRoundTrip(l, base); err != nil {
		t.Fatal(err)
	}
	reset := shortBucket(now).Add(rateLimitInterval)
	now = now.Add(-time.Hour)
	rateTestQuotaError(t, rateTestRoundTrip(l, base), reset)
}

func TestRateLimiterFailureReservationAndBoundaryResponse(t *testing.T) {
	now := rateTestTime()
	l := newRateLimiter(func() time.Time { return now })
	l.state.Short.ReadLimit = 1
	if err := rateTestRoundTrip(l, rateTestTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport access-secret")
	})); err == nil {
		t.Fatal("transport failure ignored")
	}
	base := rateTestTransport(func(*http.Request) (*http.Response, error) { return rateTestResponse(200, nil), nil })
	reset := shortBucket(now).Add(rateLimitInterval)
	rateTestQuotaError(t, rateTestRoundTrip(l, base), reset)
	now = reset
	if err := rateTestRoundTrip(l, rateTestTransport(func(*http.Request) (*http.Response, error) {
		now = dailyBucket(now).Add(24 * time.Hour)
		return rateTestResponse(200, nil), nil
	})); err != nil {
		t.Fatal(err)
	}
	s := rateTestState(t, l)
	if s.Short.ReadUsage != 1 || s.Short.OverallUsage != 1 || s.Daily.ReadUsage != 1 || s.Daily.OverallUsage != 1 {
		t.Fatalf("spanning request not reserved in new buckets: %+v", s)
	}
}

func TestRateLimiterFailureSpanningResetRemainsReserved(t *testing.T) {
	now := time.Date(2026, 1, 2, 23, 59, 0, 0, time.UTC)
	l := newRateLimiter(func() time.Time { return now })
	l.state.Short.ReadLimit = 1
	l.state.Daily.ReadLimit = 1
	if err := rateTestRoundTrip(l, rateTestTransport(func(*http.Request) (*http.Response, error) {
		now = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
		return nil, errors.New("request may have reached Strava")
	})); err == nil {
		t.Fatal("transport failure ignored")
	}
	s := rateTestState(t, l)
	if s.Short.ReadUsage != 1 || s.Short.OverallUsage != 1 || s.Daily.ReadUsage != 1 || s.Daily.OverallUsage != 1 {
		t.Fatalf("failure spanning reset lost reservations: %+v", s)
	}
	rateTestQuotaError(t, rateTestRoundTrip(l, rateTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("uncertain failed request reopened quota")
		return rateTestResponse(200, nil), nil
	})), dailyBucket(now).Add(24*time.Hour))
}

func TestRateLimiterSharedAppConcurrency(t *testing.T) {
	appID := "rate-test-" + t.Name() + "-" + strconv.FormatInt(rateTestAppSequence.Add(1), 10)
	l := rateLimiterForApp(appID)
	if l != rateLimiterForApp(appID) || l == rateLimiterForApp("different-"+appID) {
		t.Fatal("limiter not scoped by application")
	}
	l.now = rateTestTime
	l.state = newRateLimiter(rateTestTime).state
	l.state.Short.ReadLimit = 2
	var calls, active, maxActive, successes atomic.Int32
	base := rateTestTransport(func(*http.Request) (*http.Response, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := maxActive.Load(); current > old && !maxActive.CompareAndSwap(old, current); old = maxActive.Load() {
		}
		calls.Add(1)
		h := make(http.Header)
		h.Set("X-ReadRateLimit-Limit", "2,1000")
		h.Set("X-ReadRateLimit-Usage", "0,0") // Stale observations cannot oversubscribe.
		return rateTestResponse(200, h), nil
	})
	clients := []*apiClient{
		{accessToken: "athlete-one", limiter: l, httpClient: &http.Client{Transport: base}},
		{accessToken: "athlete-two", limiter: rateLimiterForApp(appID), httpClient: &http.Client{Transport: base}},
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(c *apiClient) {
			defer wg.Done()
			_, _, err := c.getActivity(context.Background(), 42)
			if err == nil {
				successes.Add(1)
			} else {
				var quota *rateLimitError
				if !errors.As(err, &quota) {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}(clients[i%2])
	}
	wg.Wait()
	if calls.Load() != 2 || successes.Load() != 2 || maxActive.Load() != 1 {
		t.Fatalf("calls=%d successes=%d maxInFlight=%d", calls.Load(), successes.Load(), maxActive.Load())
	}
}

func TestRateLimiterCancellationBeforeAndDuringLock(t *testing.T) {
	l := newRateLimiter(rateTestTime)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiBaseURL, nil)
	before := l.snapshot()
	trans := &rateLimitedTransport{limiter: l, base: rateTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("canceled request sent")
		return rateTestResponse(200, nil), nil
	})}
	if _, err := trans.RoundTrip(req); !errors.Is(err, context.Canceled) || before != l.snapshot() {
		t.Fatalf("canceled request err=%v state=%s", err, l.snapshot())
	}
	started, release := make(chan struct{}), make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- rateTestRoundTrip(l, rateTestTransport(func(*http.Request) (*http.Response, error) {
			close(started)
			<-release
			return rateTestResponse(200, nil), nil
		}))
	}()
	<-started
	ctx, cancel = context.WithCancel(context.Background())
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, apiBaseURL, nil)
	secondDone := make(chan error, 1)
	go func() {
		_, err := trans.RoundTrip(req)
		secondDone <- err
	}()
	cancel()
	select {
	case err := <-secondDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("waiting cancellation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("cancellation did not interrupt semaphore wait")
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if s := rateTestState(t, l); s.Short.ReadUsage != 1 || s.Daily.OverallUsage != 1 {
		t.Fatalf("waiting canceled request consumed quota: %+v", s)
	}
}

func TestRateLimiter429Cooldown(t *testing.T) {
	now := rateTestTime()
	for _, test := range []struct {
		name, retry string
		reset       time.Time
	}{
		{"seconds", "30", now.Add(30 * time.Second)},
		{"date", now.Add(2 * time.Minute).Format(http.TimeFormat), now.Add(2 * time.Minute)},
		{"missing", "", shortBucket(now).Add(rateLimitInterval)},
		{"malformed", "access-secret", shortBucket(now).Add(rateLimitInterval)},
		{"negative", "-1", shortBucket(now).Add(rateLimitInterval)},
		{"overflow", "9223372036854775807", shortBucket(now).Add(rateLimitInterval)},
		{"expiredDate", now.Add(-time.Hour).Format(http.TimeFormat), shortBucket(now).Add(rateLimitInterval)},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := now
			l := newRateLimiter(func() time.Time { return clock })
			calls := 0
			h := make(http.Header)
			h.Set("Retry-After", test.retry)
			base := rateTestTransport(func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return rateTestResponse(429, h), nil
				}
				return rateTestResponse(200, nil), nil
			})
			err := rateTestRoundTrip(l, base)
			rateTestQuotaError(t, err, test.reset)
			if !strings.Contains(err.Error(), "HTTP 429") || strings.Contains(err.Error(), "secret") {
				t.Errorf("429 error = %v", err)
			}
			rateTestQuotaError(t, rateTestRoundTrip(l, base), test.reset)
			if calls != 1 {
				t.Errorf("429 automatically retried: %d calls", calls)
			}
			clock = test.reset
			if err := rateTestRoundTrip(l, base); err != nil || calls != 2 {
				t.Fatalf("request at reset err=%v calls=%d", err, calls)
			}
		})
	}
	// Daily exhaustion overrides a shorter Retry-After delay.
	l := newRateLimiter(func() time.Time { return now })
	h := make(http.Header)
	h.Set("Retry-After", "30")
	h.Set("X-ReadRateLimit-Usage", "10,1000")
	rateTestQuotaError(t, rateTestRoundTrip(l, rateTestTransport(func(*http.Request) (*http.Response, error) {
		return rateTestResponse(429, h), nil
	})), dailyBucket(now).Add(24*time.Hour))
}

func TestRateLimiterPersistenceRoundTripConservativeMerge(t *testing.T) {
	now := rateTestTime()
	original := newRateLimiter(func() time.Time { return now })
	original.state.Short.ReadLimit = 9
	original.state.Short.ReadUsage = 5
	original.state.Short.OverallUsage = 7
	original.state.Daily.ReadUsage = 50
	original.state.Daily.OverallUsage = 70
	original.state.CooldownUntil = now.Add(time.Hour)
	raw := original.snapshot()
	restored := newRateLimiter(func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if err := restored.restore(raw); err != nil {
			t.Fatal(err)
		}
	}
	if restored.snapshot() != raw {
		t.Fatalf("restore was not idempotent: got %s want %s", restored.snapshot(), raw)
	}
	restored.state.Short.ReadUsage = 8
	restored.state.Daily.ReadLimit = 3
	restored.state.CooldownUntil = now.Add(2 * time.Hour)
	if err := restored.restore(raw); err != nil {
		t.Fatal(err)
	}
	s := rateTestState(t, restored)
	if s.Short.ReadUsage != 8 || s.Short.ReadLimit != 9 || s.Daily.ReadLimit != 3 || !s.CooldownUntil.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("restore weakened current state: %+v", s)
	}
	// The short bucket has expired but the daily bucket and cooldown have not.
	now = now.Add(rateLimitInterval)
	fresh := newRateLimiter(func() time.Time { return now })
	if err := fresh.restore(raw); err != nil {
		t.Fatal(err)
	}
	s = rateTestState(t, fresh)
	if s.Short.ReadUsage != 0 || s.Short.ReadLimit != 100 || s.Daily.ReadUsage != 50 || !s.CooldownUntil.Equal(original.state.CooldownUntil) {
		t.Fatalf("old short bucket merge = %+v", s)
	}
	now = now.Add(24 * time.Hour)
	fresh = newRateLimiter(func() time.Time { return now })
	before := fresh.snapshot()
	if err := fresh.restore(raw); err != nil || fresh.snapshot() != before {
		t.Fatalf("expired state restored: err=%v state=%s", err, fresh.snapshot())
	}
}

func TestRateLimiterPersistenceRejectsMalformed(t *testing.T) {
	now := rateTestTime()
	l := newRateLimiter(func() time.Time { return now })
	valid := l.snapshot()
	for _, raw := range []string{"null", "{", "{}", `{"version":1}`, valid + valid,
		strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, `"version":1`, `"version":1,"secret":"access-secret"`, 1),
		strings.Replace(valid, `"overallLimit":200`, `"overallLimit":0`, 1),
		strings.Replace(valid, `"readLimit":100`, `"readLimit":-1`, 1),
		strings.Replace(valid, `"readUsage":0`, `"readUsage":-1`, 1),
		strings.Replace(valid, `"overallUsage":0`, `"overallUsage":0.5`, 1),
		strings.Replace(valid, `"overallUsage":0`, `"overallUsage":9223372036854775808`, 1),
		strings.Replace(valid, "12:00:00Z", "12:01:00Z", 1),
		strings.Replace(valid, "12:00:00Z", "12:15:00Z", 1),
		strings.Replace(valid, "2026-01-02T00:00:00Z", "2026-01-01T00:00:00Z", 1),
		strings.Repeat("x", 4097),
	} {
		before := l.snapshot()
		err := l.restore(raw)
		if err == nil || strings.Contains(err.Error(), "secret") || l.snapshot() != before {
			t.Errorf("restore(%q) err=%v modified=%v", raw, err, l.snapshot() != before)
		}
	}
	for _, raw := range []string{"", " \n "} {
		if err := l.restore(raw); err != nil {
			t.Errorf("empty restore = %v", err)
		}
	}
	// An upgraded persisted limit cannot raise fresh conservative limits.
	if err := l.restore(strings.ReplaceAll(valid, `"readLimit":100`, `"readLimit":500`)); err != nil {
		t.Fatal(err)
	}
	if l.state.Short.ReadLimit != 100 {
		t.Fatal("restore raised limits")
	}
}
