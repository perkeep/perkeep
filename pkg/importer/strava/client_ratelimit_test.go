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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestAPIRateLimiterDisablesStandardTransportWireReplay(t *testing.T) {
	// Reproduce net/http's bodyless-GET replay: on the second request over a
	// reused connection, the server receives the GET then closes before any
	// response bytes. A replayable request would silently be sent a third time.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var wireCalls, accepted atomic.Int32
	var wg sync.WaitGroup
	var peers sync.Map
	wireErrors := make(chan error, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			peers.Store(conn, true)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer peers.Delete(conn)
				defer conn.Close()
				reader := bufio.NewReader(conn)
				for {
					req, err := http.ReadRequest(reader)
					if err != nil {
						return
					}
					body, err := io.ReadAll(req.Body)
					req.Body.Close()
					if err != nil || len(body) != 0 || req.ContentLength != 0 ||
						len(req.TransferEncoding) != 0 || req.Header.Get("Transfer-Encoding") != "" ||
						req.Header.Get("Content-Length") != "" || req.Method != http.MethodGet {
						wireErrors <- fmt.Errorf("GET framing: method=%s bytes=%d length=%d transfer=%v headers=%v err=%v",
							req.Method, len(body), req.ContentLength, req.TransferEncoding, req.Header, err)
						return
					}
					if n := wireCalls.Add(1); n == 2 {
						return // Request reached the server; no response.
					}
					if _, err := fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Length: 9\r\nX-ReadRateLimit-Limit: 2,1000\r\n\r\n{\"id\":42}"); err != nil {
						wireErrors <- err
						return
					}
				}
			}()
		}
	}()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer func() {
		base.CloseIdleConnections()
		ln.Close()
		<-done
		peers.Range(func(key, value any) bool {
			key.(net.Conn).Close()
			return true
		})
		wg.Wait()
		close(wireErrors)
		for err := range wireErrors {
			t.Error(err)
		}
	}()
	l := newRateLimiter(rateTestTime)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &rateLimitedTransport{limiter: l, base: base}}
	url := "http://" + ln.Addr().String() + "/mock"
	request := func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		_, _, err = readResponse(client, req, maxAPIResponseBytes, false)
		if req.Body != nil || req.GetBody != nil || req.ContentLength != 0 {
			t.Error("limiter modified the caller's request")
		}
		return err
	}
	if err := request(context.Background()); err != nil {
		t.Fatal(err)
	}
	var reused bool
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
	})
	if err := request(ctx); err == nil {
		t.Error("EOF after last allowed request was automatically retried")
	}
	if !reused || accepted.Load() != 1 || wireCalls.Load() != 2 {
		t.Fatalf("reused=%v accepted=%d wireCalls=%d; want one connection and exactly two requests",
			reused, accepted.Load(), wireCalls.Load())
	}
	rateTestQuotaError(t, request(context.Background()), shortBucket(rateTestTime()).Add(rateLimitInterval))
	state := rateTestState(t, l)
	if wireCalls.Load() != 2 || state.Short.ReadUsage != 2 || state.Daily.ReadUsage != 2 ||
		state.Short.OverallUsage != 2 || state.Daily.OverallUsage != 2 {
		t.Fatalf("wireCalls=%d state=%+v", wireCalls.Load(), state)
	}
}

func TestAPIRateLimiterEmptyBodyHTTP2(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 0 || r.ProtoMajor != 2 || len(r.TransferEncoding) != 0 {
			t.Errorf("HTTP/2 body: bytes=%d protocol=%s transfer=%v error=%v", len(body), r.Proto, r.TransferEncoding, err)
		}
		calls.Add(1)
		fmt.Fprint(w, `{"id":42}`)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	l := newRateLimiter(rateTestTime)
	client := *server.Client()
	client.Transport = &rateLimitedTransport{limiter: l, base: client.Transport}
	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := readResponse(&client, req, maxAPIResponseBytes, false); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d", calls.Load())
	}
}

func TestAPIRateLimiterObservesEveryHTTPStatus(t *testing.T) {
	for _, status := range []int{200, 301, 404, 429, 401, 403, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			now := rateTestTime()
			l := newRateLimiter(func() time.Time { return now })
			h := make(http.Header)
			h.Set("X-RateLimit-Limit", "123,1234")
			h.Set("X-RateLimit-Usage", "12,120")
			h.Set("X-ReadRateLimit-Limit", "56,567")
			h.Set("X-ReadRateLimit-Usage", "5,50")
			h.Set("Retry-After", "30")
			h.Set("Location", "https://example.invalid?access-secret")
			body := &clientTestBody{Reader: strings.NewReader("access-secret")}
			calls := 0
			c := &apiClient{accessToken: "access-secret", limiter: l, httpClient: &http.Client{
				Transport: rateTestTransport(func(*http.Request) (*http.Response, error) {
					calls++
					res := rateTestResponse(status, h)
					res.Body = body
					return res, nil
				}),
			}}
			_, missing, err := c.get(context.Background(), "/activities/42/streams", true)
			if (status == 404 && (err != nil || !missing)) || (status != 404 && status != 200 && err == nil) {
				t.Fatalf("status %d missing=%v error=%v", status, missing, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatalf("leaking error: %v", err)
			}
			if status == 429 {
				rateTestQuotaError(t, err, now.Add(30*time.Second))
			}
			s := rateTestState(t, l)
			if s.Short.OverallLimit != 123 || s.Daily.OverallLimit != 1234 ||
				s.Short.OverallUsage != 12 || s.Daily.OverallUsage != 120 ||
				s.Short.ReadLimit != 56 || s.Daily.ReadLimit != 567 ||
				s.Short.ReadUsage != 5 || s.Daily.ReadUsage != 50 || calls != 1 || !body.closed {
				t.Fatalf("calls=%d closed=%v state=%+v", calls, body.closed, s)
			}
		})
	}
}

func TestAPIRateLimiterNeverForwardsRedirectToken(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		t.Errorf("redirect target received request with auth=%q", r.Header.Get("Authorization"))
	}))
	defer target.Close()
	l := newRateLimiter(rateTestTime)
	c := &apiClient{accessToken: "access-secret", limiter: l, httpClient: &http.Client{
		Transport: rateTestTransport(func(*http.Request) (*http.Response, error) {
			h := make(http.Header)
			h.Set("Location", target.URL)
			h.Set("X-ReadRateLimit-Usage", "99,999")
			return rateTestResponse(302, h), nil
		}),
	}}
	if _, _, err := c.getActivity(context.Background(), 42); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("redirect error = %v", err)
	}
	if targetCalls.Load() != 0 || l.state.Short.ReadUsage != 99 || l.state.Daily.ReadUsage != 999 {
		t.Fatalf("targetCalls=%d state=%+v", targetCalls.Load(), l.state)
	}
}

func TestAPIRateLimiterCancellationAndFailureRemainSanitized(t *testing.T) {
	l := newRateLimiter(rateTestTime)
	c := &apiClient{accessToken: "access-secret", limiter: l, httpClient: &http.Client{
		Transport: rateTestTransport(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport echoed access-secret")
		}),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := c.getActivity(ctx, 42)
	if !errors.Is(err, context.Canceled) || l.state.Short.ReadUsage != 0 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("canceled request error=%v state=%+v", err, l.state)
	}
	l.state.Short.ReadLimit = 1
	if _, _, err := c.getActivity(context.Background(), 42); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("transport error = %v", err)
	}
	_, _, err = c.getActivity(context.Background(), 42)
	rateTestQuotaError(t, err, shortBucket(rateTestTime()).Add(rateLimitInterval))
}

func TestAPIRateLimiterReadBodyFailureRetainsHeaderUsage(t *testing.T) {
	l := newRateLimiter(rateTestTime)
	h := make(http.Header)
	h.Set("X-ReadRateLimit-Usage", "99,999")
	c := &apiClient{accessToken: "access-secret", limiter: l, httpClient: &http.Client{
		Transport: rateTestTransport(func(*http.Request) (*http.Response, error) {
			res := rateTestResponse(200, h)
			res.Body = &clientTestBody{Reader: clientTestFailingReader{}}
			return res, nil
		}),
	}}
	_, _, err := c.getActivity(context.Background(), 42)
	if err == nil || strings.Contains(err.Error(), "secret") || l.state.Short.ReadUsage != 99 || l.state.Daily.ReadUsage != 999 {
		t.Fatalf("body failure error=%v state=%+v", err, l.state)
	}
}

func TestAPIActivityPageSize(t *testing.T) {
	c := &apiClient{accessToken: "x", httpClient: &http.Client{
		Transport: rateTestTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Query().Get("per_page") != "10" || r.URL.Query().Get("page") != "2" {
				t.Errorf("unexpected query: %s", r.URL.RawQuery)
			}
			res := rateTestResponse(200, nil)
			res.Body = &clientTestBody{Reader: strings.NewReader(`[]`)}
			return res, nil
		}),
	}}
	if _, err := c.listActivities(context.Background(), 2, 10); err != nil {
		t.Fatal(err)
	}
	c.httpClient.Transport = rateTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("invalid page size made request")
		return nil, errors.New("unexpected request")
	})
	for _, sizes := range [][]int{{0}, {-1}, {101}, {10, 20}} {
		if _, err := c.listActivities(context.Background(), 1, sizes...); err == nil {
			t.Errorf("accepted page sizes %v", sizes)
		}
	}
}

func TestAPIRateLimiterCapsTimeoutWithoutChangingSharedClient(t *testing.T) {
	l := newRateLimiter(rateTestTime)
	client := &http.Client{Timeout: 2 * time.Minute}
	client.Transport = rateTestTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > time.Minute || time.Until(deadline) < 55*time.Second {
			t.Errorf("limited request timeout = %s, present=%v", time.Until(deadline), ok)
		}
		return rateTestResponse(200, nil), nil
	})
	c := &apiClient{accessToken: "x", limiter: l, httpClient: client}
	if _, _, err := c.getActivity(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 2*time.Minute || client.CheckRedirect != nil {
		t.Fatal("limiter changed shared HTTP client")
	}
}

func TestMain(m *testing.M) {
	// Most tests exercise failing at a quota, not waiting it out.
	maxRateLimitWait = 0
	os.Exit(m.Run())
}

func TestAPIClientWaitsOutShortWindow(t *testing.T) {
	maxRateLimitWait = rateLimitInterval
	t.Cleanup(func() { maxRateLimitWait = 0 })
	synctest.Test(t, func(t *testing.T) {
		l := newRateLimiter(time.Now)
		var requests atomic.Int32
		var logs []string
		c := &apiClient{accessToken: "x", limiter: l,
			logf: func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
			httpClient: &http.Client{Transport: rateTestTransport(func(*http.Request) (*http.Response, error) {
				requests.Add(1)
				return rateTestResponse(200, make(http.Header)), nil
			})},
		}
		start := time.Now()
		reset := shortBucket(start).Add(rateLimitInterval)
		l.state.Short.ReadUsage = l.state.Short.ReadLimit
		if _, _, err := c.getActivity(context.Background(), 42); err != nil {
			t.Fatal(err)
		}
		if got := time.Now(); got.Before(reset) || requests.Load() != 1 || len(logs) != 1 || !strings.Contains(logs[0], "waiting") {
			t.Fatalf("at %v (reset %v): %d requests, logs %q", got.Sub(start), reset.Sub(start), requests.Load(), logs)
		}

		// It doesn't wait for the daily quota.
		l.state.Daily.ReadUsage = l.state.Daily.ReadLimit
		_, _, err := c.getActivity(context.Background(), 42)
		rateTestQuotaError(t, err, dailyBucket(time.Now()).Add(24*time.Hour))
		if requests.Load() != 1 {
			t.Errorf("%d requests; want no more", requests.Load())
		}

		// Nor past cancellation.
		l.state.Daily.ReadUsage = 0
		l.state.Short.ReadUsage = l.state.Short.ReadLimit
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(time.Minute, cancel)
		if _, _, err := c.getActivity(ctx, 42); !errors.Is(err, context.Canceled) {
			t.Errorf("canceled wait: %v", err)
		}
	})
}
