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
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type clientTestTransport func(*http.Request) (*http.Response, error)

func (f clientTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func clientTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestAPIActivitiesPagination(t *testing.T) {
	var pages []int
	c := &apiClient{accessToken: "bearer-secret", httpClient: &http.Client{Transport: clientTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "www.strava.com" || r.URL.Path != "/api/v3/athlete/activities" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer bearer-secret" || r.Header.Get("Accept") != "application/json" {
			t.Fatalf("unexpected headers: %v", r.Header)
		}
		if r.URL.Query().Get("per_page") != "100" || len(r.URL.Query()) != 2 || strings.Contains(r.URL.RawQuery, "secret") {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		page := len(pages) + 1
		if r.URL.Query().Get("page") != fmt.Sprint(page) {
			t.Fatalf("page = %q, want %d", r.URL.Query().Get("page"), page)
		}
		pages = append(pages, page)
		if page == 1 {
			return clientTestResponse(200, `[{"id":9007199254740993,"name":"Morning ride","start_date":"2026-01-02T03:04:05Z","sport_type":"Ride"}]`), nil
		}
		return clientTestResponse(200, `[]`), nil
	})}}
	first, err := c.listActivities(context.Background(), 1)
	if err != nil || len(first) != 1 || first[0].ID != 9007199254740993 || first[0].SportType != "Ride" {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	second, err := c.listActivities(context.Background(), 2)
	if err != nil || len(second) != 0 || second == nil {
		t.Fatalf("second page = %+v, %v", second, err)
	}
	for _, page := range []int{0, -1} {
		if _, err := c.listActivities(context.Background(), page); err == nil {
			t.Errorf("page %d accepted", page)
		}
	}
	if !reflect.DeepEqual(pages, []int{1, 2}) {
		t.Errorf("requests = %v", pages)
	}
}

func TestAPIActivitiesSinceBoundary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		since time.Time
		after string
	}{
		{name: "unbounded"},
		{name: "epoch", since: time.Unix(0, 0), after: "-1"},
		{name: "fractional", since: time.Unix(1000, 900000000), after: "999"},
		{name: "offset", since: time.Unix(1000, 0).In(time.FixedZone("offset", 3600)), after: "999"},
		{name: "before-epoch", since: time.Unix(-1000, 0), after: "-1001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &apiClient{accessToken: "bearer-secret", httpClient: &http.Client{
				Transport: clientTestTransport(func(r *http.Request) (*http.Response, error) {
					want := url.Values{"page": {"2"}, "per_page": {"3"}}
					if tc.after != "" {
						want.Set("after", tc.after)
					}
					if got := r.URL.Query(); !reflect.DeepEqual(got, want) {
						t.Fatalf("query = %v; want %v", got, want)
					}
					if r.Header.Get("Authorization") != "Bearer bearer-secret" {
						t.Fatal("missing authorization")
					}
					return clientTestResponse(200, `[]`), nil
				}),
			}}
			if got, err := c.listActivitiesSince(context.Background(), 2, 3, tc.since); err != nil || got == nil || len(got) != 0 {
				t.Fatalf("listActivitiesSince = %v, %v; want empty list", got, err)
			}
		})
	}
}

func TestAPIActivitiesSinceRejectsInvalidPagination(t *testing.T) {
	requests := 0
	c := &apiClient{httpClient: &http.Client{Transport: clientTestTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return clientTestResponse(200, `[]`), nil
	})}}
	for _, args := range [][2]int{{0, 100}, {-1, 100}, {1, 0}, {1, 101}} {
		if _, err := c.listActivitiesSince(context.Background(), args[0], args[1], time.Unix(1000, 0)); err == nil {
			t.Errorf("page/size %v accepted", args)
		}
	}
	if requests != 0 {
		t.Errorf("invalid pagination made %d HTTP requests", requests)
	}
}

func TestAPIActivityRawAndFields(t *testing.T) {
	raw := " \n" + `{
  "id": 42, "name": "Trail & run", "description": "notes", "type": "Run", "sport_type": "TrailRun",
  "timezone": "(GMT+01:00) Europe/Paris", "external_id": "device.fit", "gear_id": "g1",
  "start_date": "2026-01-02T03:04:05Z", "distance": 12.5, "total_elevation_gain": 3.25,
  "moving_time": 15, "elapsed_time": 17, "start_latlng": [12.3,-45.6],
  "trainer": true, "commute": true, "manual": true, "private": true,
  "average_heartrate": 123.5, "max_heartrate": 170, "calories": 0,
  "future_field": {"integer": 9007199254740993, "nested": ["unrecognized"]}
}` + "\n "
	c := &apiClient{accessToken: "x", httpClient: &http.Client{Transport: clientTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != apiBaseURL+"/activities/42" {
			t.Fatalf("URL = %s", r.URL)
		}
		return clientTestResponse(200, raw), nil
	})}}
	a, gotRaw, err := c.getActivity(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotRaw) != raw {
		t.Errorf("raw JSON altered: %q", gotRaw)
	}
	want := &activity{
		ID: 42, Name: "Trail & run", Description: "notes", Type: "Run", SportType: "TrailRun",
		Timezone: "(GMT+01:00) Europe/Paris", ExternalID: "device.fit", GearID: "g1",
		StartDate: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Distance: 12.5, TotalElevationGain: 3.25,
		MovingTime: 15, ElapsedTime: 17, StartLatlng: []float64{12.3, -45.6}, Trainer: true, Commute: true, Manual: true, Private: true,
	}
	average, maximum, calories := 123.5, 170.0, 0.0
	want.AverageHeartrate, want.MaxHeartrate, want.Calories = &average, &maximum, &calories
	if !reflect.DeepEqual(a, want) {
		t.Errorf("activity = %+v, want %+v", a, want)
	}
	for _, body := range []string{`{"id":42}`, `{"id":42,"average_heartrate":null,"max_heartrate":null,"calories":null}`} {
		c.httpClient.Transport = clientTestTransport(func(*http.Request) (*http.Response, error) { return clientTestResponse(200, body), nil })
		a, _, err := c.getActivity(context.Background(), 42)
		if err != nil || a.AverageHeartrate != nil || a.MaxHeartrate != nil || a.Calories != nil {
			t.Fatalf("optional fields = %+v, %v", a, err)
		}
	}
}

func TestAPIStreamsRaw(t *testing.T) {
	raw := " \n" + `{"latlng":{"data":[[1.2,3.4]],"series_type":"time","original_size":1,"resolution":"high","unknown":true},"future_stream":{"data":[9007199254740993],"unknown":{"a":1}}}` + "\n"
	c := &apiClient{accessToken: "secret", httpClient: &http.Client{Transport: clientTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v3/activities/42/streams" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request = %s, %v", r.URL, r.Header)
		}
		want := url.Values{"keys": {"time,latlng,altitude,distance,heartrate,cadence,watts,temp,moving,velocity_smooth"}, "key_by_type": {"true"}}
		if !reflect.DeepEqual(r.URL.Query(), want) {
			t.Fatalf("query = %v, want %v", r.URL.Query(), want)
		}
		return clientTestResponse(200, raw), nil
	})}}
	s, gotRaw, err := c.getStreams(context.Background(), 42)
	if err != nil || string(gotRaw) != raw {
		t.Fatalf("streams raw = %q, %v", gotRaw, err)
	}
	if s["latlng"].SeriesType != "time" || s["latlng"].OriginalSize != 1 || s["latlng"].Resolution != "high" || string(s["latlng"].Data) != "[[1.2,3.4]]" || string(s["future_stream"].Data) != "[9007199254740993]" {
		t.Errorf("streams = %+v", s)
	}
}

func TestAPIStatusErrorsAndStreamsNotFound(t *testing.T) {
	for _, status := range []int{301, 401, 403, 404, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			body := &clientTestBody{Reader: strings.NewReader("access-secret refresh-secret echoed server body")}
			c := &apiClient{accessToken: "access-secret", httpClient: &http.Client{Transport: clientTestTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Status: "access-secret", Body: body, Header: http.Header{"Location": {apiBaseURL + "/redirect?access-secret"}}}, nil
			})}}
			_, _, err := c.getActivity(context.Background(), 42)
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "server body") {
				t.Errorf("error leaks response or token: %v", err)
			}
			if status == 429 && !strings.Contains(err.Error(), "rate limit") {
				t.Errorf("429 error not explicit: %v", err)
			}
			if calls != 1 || !body.closed || body.readBytes != 0 {
				t.Errorf("requests=%d closed=%v read=%d", calls, body.closed, body.readBytes)
			}
			body.closed = false
			s, raw, err := c.getStreams(context.Background(), 42)
			if status == 404 {
				if err != nil || len(s) != 0 || s == nil || raw != nil {
					t.Errorf("missing streams = %v, %q, %v", s, raw, err)
				}
			} else if err == nil {
				t.Error("streams request unexpectedly succeeded")
			}
			if !body.closed {
				t.Error("streams body not closed")
			}
		})
	}
}

func TestAPIRejectsMalformedResponses(t *testing.T) {
	for _, body := range []string{"", "{", "null", "[]", `{"id":41}`, `{"id":42} {"extra":1}`, `{"id":42,"start_date":"access-secret"}`} {
		c := &apiClient{accessToken: "secret", httpClient: &http.Client{Transport: clientTestTransport(func(*http.Request) (*http.Response, error) { return clientTestResponse(200, body), nil })}}
		if a, raw, err := c.getActivity(context.Background(), 42); err == nil || a != nil || raw != nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("getActivity(%q) = %v, %q, %v", body, a, raw, err)
		}
	}
	for _, body := range []string{"", "null", "{}", `[{"id":0}]`, `[{"id":42}] null`} {
		c := &apiClient{accessToken: "x", httpClient: &http.Client{Transport: clientTestTransport(func(*http.Request) (*http.Response, error) { return clientTestResponse(200, body), nil })}}
		if _, err := c.listActivities(context.Background(), 1); err == nil {
			t.Errorf("list accepted %q", body)
		}
	}
	for _, body := range []string{"", "null", "[]", `{"time":{"original_size":"wrong"}}`, `{} {}`} {
		c := &apiClient{accessToken: "x", httpClient: &http.Client{Transport: clientTestTransport(func(*http.Request) (*http.Response, error) { return clientTestResponse(200, body), nil })}}
		if s, raw, err := c.getStreams(context.Background(), 42); err == nil || s != nil || raw != nil {
			t.Errorf("getStreams(%q) = %v, %q, %v", body, s, raw, err)
		}
	}
}

func TestAPIInvalidInput(t *testing.T) {
	c := &apiClient{accessToken: "x", httpClient: &http.Client{Transport: clientTestTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid input made request")
		return nil, nil
	})}}
	for _, id := range []int64{0, -1} {
		if _, _, err := c.getActivity(context.Background(), id); err == nil {
			t.Errorf("activity ID %d accepted", id)
		}
		if _, _, err := c.getStreams(context.Background(), id); err == nil {
			t.Errorf("streams ID %d accepted", id)
		}
	}
	c.accessToken = " "
	if _, err := c.listActivities(context.Background(), 1); err == nil {
		t.Error("empty token accepted")
	}
}

func TestExchangeTokenFormAndRotation(t *testing.T) {
	for _, grant := range []string{"authorization_code", "refresh_token"} {
		t.Run(grant, func(t *testing.T) {
			form := url.Values{"client_id": {"123"}, "client_secret": {"a&b=secret"}, "grant_type": {grant}}
			if grant == "authorization_code" {
				form.Set("code", "code+?&value")
			} else {
				form.Set("refresh_token", "old-refresh")
			}
			client := &http.Client{Transport: clientTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost || r.URL.String() != tokenURL || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("Authorization") != "" {
					t.Fatalf("token request = %s %s, %v", r.Method, r.URL, r.Header)
				}
				if err := r.ParseForm(); err != nil || !reflect.DeepEqual(r.PostForm, form) {
					t.Fatalf("form = %v, %v", r.PostForm, err)
				}
				return clientTestResponse(200, fmt.Sprintf(`{"access_token":"new-access","refresh_token":"new-refresh","expires_at":%d,"athlete":{"id":7,"username":"runner","firstname":"A","lastname":"B"}}`, time.Now().Add(time.Hour).Unix())), nil
			})}
			token, err := exchangeToken(context.Background(), client, form)
			if err != nil || token.AccessToken != "new-access" || token.RefreshToken != "new-refresh" || token.ExpiresAt <= time.Now().Unix() || token.Athlete != (athlete{ID: 7, Username: "runner", Firstname: "A", Lastname: "B"}) {
				t.Fatalf("token = %+v, %v", token, err)
			}
		})
	}
}

func TestExchangeTokenRejectsInvalidResponses(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `{"access_token":"access-secret","refresh_token":"refresh-secret"}`,
		`{"access_token":"access-secret","refresh_token":"refresh-secret","expires_at":-1}`,
		fmt.Sprintf(`{"access_token":"access-secret","refresh_token":"refresh-secret","expires_at":%d}`, time.Now().Add(-time.Hour).Unix()),
		`{"access_token":"access-secret","refresh_token":"refresh-secret","expires_at":"access-secret"}`,
		`{"access_token":"access-secret","refresh_token":"refresh-secret","expires_at":4000000000} null`,
		`{"access_token":" ","refresh_token":"refresh-secret","expires_at":4000000000}`,
		`{"access_token":"access-secret","expires_at":4000000000}`,
	} {
		client := &http.Client{Transport: clientTestTransport(func(*http.Request) (*http.Response, error) { return clientTestResponse(200, body), nil })}
		token, err := exchangeToken(context.Background(), client, nil)
		if err == nil || token != nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("exchange(%q) = %+v, %v", body, token, err)
		}
	}
	for _, status := range []int{400, 401, 429, 500} {
		client := &http.Client{Transport: clientTestTransport(func(*http.Request) (*http.Response, error) {
			return clientTestResponse(status, "access-secret refresh-secret client-secret"), nil
		})}
		if _, err := exchangeToken(context.Background(), client, nil); err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("exchange status %d = %v", status, err)
		}
	}
}

func TestHTTPCancellationAndRedactedTransportError(t *testing.T) {
	for _, tokenRequest := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		started := make(chan struct{})
		client := &http.Client{Transport: clientTestTransport(func(r *http.Request) (*http.Response, error) {
			close(started)
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
		done := make(chan error, 1)
		go func() {
			if tokenRequest {
				_, err := exchangeToken(ctx, client, nil)
				done <- err
			} else {
				_, _, err := (&apiClient{httpClient: client, accessToken: "secret"}).getActivity(ctx, 42)
				done <- err
			}
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("request did not start")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("request did not cancel")
		}
	}
	cause := errors.New("echoed access-secret refresh-secret")
	client := &http.Client{Transport: clientTestTransport(func(*http.Request) (*http.Response, error) { return nil, cause })}
	_, _, err := (&apiClient{httpClient: client, accessToken: "access-secret"}).getActivity(context.Background(), 42)
	if err == nil || strings.Contains(err.Error(), "secret") || !errors.Is(err, cause) {
		t.Fatalf("transport error = %v", err)
	}
}

type clientTestBody struct {
	io.Reader
	closed    bool
	readBytes int
}

func (b *clientTestBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.readBytes += n
	return n, err
}
func (b *clientTestBody) Close() error { b.closed = true; return nil }

type clientTestFailingReader struct{}

func (clientTestFailingReader) Read([]byte) (int, error) { return 0, errors.New("access-secret") }

type clientTestContextReader struct {
	ctx     context.Context
	started chan struct{}
}

func (r *clientTestContextReader) Read([]byte) (int, error) {
	close(r.started)
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func TestHTTPBodyReadCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	body := &clientTestBody{Reader: &clientTestContextReader{ctx: ctx, started: started}}
	client := &http.Client{Transport: clientTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: body}, nil
	})}
	done := make(chan error, 1)
	go func() {
		_, _, err := (&apiClient{httpClient: client, accessToken: "secret"}).getActivity(ctx, 42)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("body read did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !body.closed {
			t.Errorf("body cancellation: err=%v closed=%v", err, body.closed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("body read did not cancel")
	}
}

func TestHTTPBoundedReadAndClose(t *testing.T) {
	for _, tc := range []struct {
		name          string
		reader        io.Reader
		contentLength int64
		wantRead      int
		wantErr       bool
	}{
		{"exact limit", strings.NewReader(`{"id":1}`), 8, 8, false},
		{"unknown length oversize", strings.NewReader(strings.Repeat("x", 100)), -1, 9, true},
		{"declared oversize", strings.NewReader(strings.Repeat("x", 100)), 100, 0, true},
		{"read error", clientTestFailingReader{}, -1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &clientTestBody{Reader: tc.reader}
			client := &http.Client{Transport: clientTestTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: body, ContentLength: tc.contentLength}, nil
			})}
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, apiBaseURL, nil)
			_, _, err := readResponse(client, req, 8, false)
			if (err != nil) != tc.wantErr || !body.closed || body.readBytes != tc.wantRead {
				t.Errorf("err=%v closed=%v read=%d, wantErr=%v wantRead=%d", err, body.closed, body.readBytes, tc.wantErr, tc.wantRead)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Errorf("read error leaked token: %v", err)
			}
		})
	}
	for _, tokenRequest := range []bool{false, true} {
		body := &clientTestBody{Reader: strings.NewReader("irrelevant")}
		limit := int64(maxAPIResponseBytes)
		if tokenRequest {
			limit = maxTokenResponseBytes
		}
		client := &http.Client{Transport: clientTestTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: body, ContentLength: limit + 1}, nil
		})}
		var err error
		if tokenRequest {
			_, err = exchangeToken(context.Background(), client, nil)
		} else {
			_, _, err = (&apiClient{httpClient: client, accessToken: "x"}).getActivity(context.Background(), 1)
		}
		if err == nil || !body.closed || body.readBytes != 0 {
			t.Errorf("public limit: err=%v closed=%v read=%d", err, body.closed, body.readBytes)
		}
	}
}

func TestHTTPRejectsRedirectWithoutChangingClient(t *testing.T) {
	var calls atomic.Int32
	policyCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/" {
			t.Error("credentials followed redirect")
		}
		w.Header().Set("Location", "/redirect")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { policyCalls++; return nil }
	req, _ := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("client_secret=secret"))
	_, _, err := readResponse(client, req, 100, false)
	if err == nil || !strings.Contains(err.Error(), "307") || calls.Load() != 1 || policyCalls != 0 {
		t.Fatalf("redirect: err=%v calls=%d policyCalls=%d", err, calls.Load(), policyCalls)
	}
	if err := client.CheckRedirect(nil, nil); err != nil || policyCalls != 1 {
		t.Fatal("shared client redirect policy was mutated")
	}
}

func TestHTTPTimeoutDefaultsWithoutChangingClient(t *testing.T) {
	for _, configured := range []time.Duration{0, 2 * time.Second, 2 * time.Minute} {
		want := configured
		if want == 0 {
			want = time.Minute
		}
		client := &http.Client{Timeout: configured, Transport: clientTestTransport(func(r *http.Request) (*http.Response, error) {
			deadline, ok := r.Context().Deadline()
			if !ok {
				t.Error("request has no deadline")
			}
			remaining := time.Until(deadline)
			if remaining > want || remaining < want-time.Second {
				t.Errorf("timeout = %v, want approximately %v", remaining, want)
			}
			return clientTestResponse(200, `{"id":42}`), nil
		})}
		if _, _, err := (&apiClient{httpClient: client, accessToken: "x"}).getActivity(context.Background(), 42); err != nil {
			t.Fatal(err)
		}
		if client.Timeout != configured {
			t.Errorf("client timeout changed to %v, want %v", client.Timeout, configured)
		}
	}
}

func TestActivityJSONOptionalValues(t *testing.T) {
	var a activity
	if err := json.Unmarshal([]byte(`{"id":1,"calories":0,"average_heartrate":null}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.Calories == nil || *a.Calories != 0 || a.AverageHeartrate != nil || a.MaxHeartrate != nil {
		t.Errorf("optional values = %+v", a)
	}
}
