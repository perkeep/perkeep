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

package swarm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"

	"go4.org/ctxutil"
)

type redactionTransport func(*http.Request) (*http.Response, error)

func (f redactionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type errorBody struct {
	err    error
	closed bool
}

func (b *errorBody) Read([]byte) (int, error) { return 0, b.err }
func (b *errorBody) Close() error             { b.closed = true; return nil }

func TestAPIRedactsRequestErrors(t *testing.T) {
	const accessToken = "private-oauth-token"
	const clientID = "private-client-id"
	// JSON decoding errors may include numeric response values.
	const clientSecret = "941986421183"
	for _, tt := range []struct {
		name  string
		cause error
		want  string
	}{
		{name: "transport", want: "fetching API response"},
		{name: "status", want: "HTTP status 503"},
		{name: "payment", want: "HTTP status 402", cause: ErrPaymentRequired},
		{name: "read", want: "decoding API response"},
		{name: "decode", want: "decoding API response"},
		{name: "canceled", want: "context canceled", cause: context.Canceled},
		{name: "deadline", want: "context deadline exceeded", cause: context.DeadlineExceeded},
	} {
		for _, userAPI := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/user=%v", tt.name, userAPI), func(t *testing.T) {
				var body *errorBody
				transport := redactionTransport(func(req *http.Request) (*http.Response, error) {
					form := req.URL.Query()
					if userAPI && form.Get("oauth_token") != accessToken ||
						!userAPI && (form.Get("client_id") != clientID || form.Get("client_secret") != clientSecret) {
						t.Fatal("request did not include the expected credentials")
					}
					reflected := req.URL.String()
					switch tt.name {
					case "transport", "canceled", "deadline":
						cause := fmt.Errorf("provider echoed %s", reflected)
						if tt.cause != nil {
							cause = fmt.Errorf("%s: %w", reflected, tt.cause)
						}
						return nil, &url.Error{Op: "Get", URL: reflected, Err: cause}
					case "status", "payment", "read":
						body = &errorBody{err: errors.New(reflected)}
						status := http.StatusOK
						if tt.name == "status" {
							status = http.StatusServiceUnavailable
						} else if tt.name == "payment" {
							status = http.StatusPaymentRequired
						}
						return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, reflected), Body: body}, nil
					default:
						// A numeric value in a string field produces a JSON error
						// that includes the reflected secret.
						return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"value":` + clientSecret + `}`))}, nil
					}
				})
				ctx := context.WithValue(context.Background(), ctxutil.HTTPClient, &http.Client{Transport: transport})
				var logs bytes.Buffer
				old := log.Writer()
				log.SetOutput(&logs)
				defer log.SetOutput(old)
				var result struct{ Value string }
				im := &imp{}
				var err error
				if userAPI {
					err = im.doUserAPI(ctx, accessToken, &result, "users/self")
				} else {
					err = im.doCredAPI(ctx, clientID, clientSecret, &result, "venues/test/photos")
				}
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("API error = %v; want %q", err, tt.want)
				}
				if tt.cause != nil && !errors.Is(err, tt.cause) {
					t.Errorf("API error does not preserve %v", tt.cause)
				}
				if body != nil && !body.closed {
					t.Error("response body was not closed")
				}
				for _, secret := range []string{accessToken, clientID, clientSecret} {
					if strings.Contains(err.Error()+logs.String(), secret) {
						t.Errorf("error or server log contains credential %q: %v / %s", secret, err, logs.String())
					}
				}
			})
		}
	}
}

func TestOAuthExchangeDoesNotLogResponse(t *testing.T) {
	const clientID = "private-client-id"
	const clientSecret = "private-client-secret"
	const code = "private-oauth-code"
	// The OAuth library's exchange error includes the provider's response body.
	transport := redactionTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Status:     "401 Unauthorized",
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error":"%s %s %s"}`, clientID, clientSecret, code))),
		}, nil
	})
	h := imptest.NewMemoryHost(t, importer.HostConfig{
		ClientId: map[string]string{"swarm": clientID}, ClientSecret: map[string]string{"swarm": clientSecret},
	})
	t.Cleanup(func() { h.Close() })
	rc, err := importer.CreateAccount(h, "swarm")
	if err != nil {
		t.Fatal(err)
	}
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	defer func() { http.DefaultClient = oldClient }()
	var logs bytes.Buffer
	oldLog := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(oldLog)

	state := "acct:" + rc.AccountNode().PermanodeRef().String()
	req := httptest.NewRequest(http.MethodGet, "/importer/swarm/callback?"+url.Values{"state": {state}, "code": {code}}.Encode(), nil)
	req.Header.Set(httputil.PathSuffixHeader, "swarm/callback")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), "exchanging OAuth token") {
		t.Fatalf("callback = %d %q, log = %s", w.Code, w.Body.String(), logs.String())
	}
	for _, secret := range []string{clientID, clientSecret, code} {
		if strings.Contains(w.Body.String()+logs.String(), secret) {
			t.Errorf("callback response or log contains credential %q", secret)
		}
	}
}

func TestImageFailureRedactsURLAndError(t *testing.T) {
	const secret = "private-image-token"
	for _, readError := range []bool{false, true} {
		t.Run(fmt.Sprintf("read=%v", readError), func(t *testing.T) {
			transport := redactionTransport(func(req *http.Request) (*http.Response, error) {
				reflected := req.URL.String()
				if readError {
					return &http.Response{StatusCode: http.StatusOK, Body: &errorBody{err: errors.New(reflected)}}, nil
				}
				return nil, &url.Error{Op: "Get", URL: reflected, Err: errors.New(reflected)}
			})
			h := imptest.NewMemoryHost(t, importer.HostConfig{HTTPClient: &http.Client{Transport: transport}})
			t.Cleanup(func() { h.Close() })
			rc, err := importer.CreateAccount(h, "swarm")
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			old := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(old)
			r := &run{RunContext: rc, im: &imp{}}
			if ref := r.urlFileRef("https://images.example/photo?oauth_token="+secret, "photo.jpg"); ref != "" {
				t.Errorf("urlFileRef returned %q for failed download", ref)
			}
			path := "swarm/" + rc.AccountNode().PermanodeRef().String()
			req := httptest.NewRequest(http.MethodGet, "/importer/"+path+"?log=0", nil)
			req.Header.Set(httputil.PathSuffixHeader, path)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("account log status = %d", w.Code)
			}
			if !strings.Contains(logs.String(), "swarm:") || strings.Contains(logs.String()+w.Body.String(), secret) {
				t.Errorf("unsafe server or account log: %s / %s", logs.String(), w.Body.String())
			}
		})
	}
}
