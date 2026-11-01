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

package pinboard

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
)

type redactionTransport func(*http.Request) (*http.Response, error)

func (f redactionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type errorBody struct{ err error }

func (b errorBody) Read([]byte) (int, error) { return 0, b.err }
func (b errorBody) Close() error             { return nil }

func TestRunRedactsRequestErrors(t *testing.T) {
	// A numeric secret also catches JSON type errors, which can include numbers
	// from the response in their error messages.
	const secret = "941986421183"
	const token = "owner:" + secret
	for _, tt := range []struct {
		name  string
		cause error
		want  string
	}{
		{name: "transport", want: "fetching posts"},
		{name: "status", want: "HTTP status 503"},
		{name: "read", want: "reading posts response"},
		{name: "decode", want: "decoding posts response"},
		{name: "canceled", cause: context.Canceled, want: "context canceled"},
		{name: "deadline", cause: context.DeadlineExceeded, want: "context deadline exceeded"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			transport := redactionTransport(func(req *http.Request) (*http.Response, error) {
				if got := req.URL.Query().Get("auth_token"); got != token {
					t.Fatal("request did not include the expected token")
				}
				reflected := req.URL.String()
				if tt.name == "transport" || tt.cause != nil {
					cause := fmt.Errorf("provider echoed %s", reflected)
					if tt.cause != nil {
						cause = fmt.Errorf("%s: %w", reflected, tt.cause)
					}
					return nil, &url.Error{Op: "Get", URL: reflected, Err: cause}
				}
				res := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`[{"Href":` + secret + `}]`))}
				switch tt.name {
				case "status":
					res.StatusCode = http.StatusServiceUnavailable
					res.Status = "503 " + reflected
					res.Body = io.NopCloser(strings.NewReader(reflected))
				case "read":
					res.Body = errorBody{errors.New(reflected)}
				}
				return res, nil
			})
			h := imptest.NewMemoryHost(t, importer.HostConfig{HTTPClient: &http.Client{Transport: transport}})
			t.Cleanup(func() { h.Close() })
			rc, err := importer.CreateAccount(h, "pinboard")
			if err != nil {
				t.Fatal(err)
			}
			if err := rc.AccountNode().SetAttr(attrAuthToken, token); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			old := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(old)

			err = (imp{}).Run(rc)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Run error = %v; want %q", err, tt.want)
			}
			if tt.cause != nil && !errors.Is(err, tt.cause) {
				t.Errorf("Run error does not preserve %v", tt.cause)
			}

			// Check both the server log and the account log exposed to the UI.
			path := "pinboard/" + rc.AccountNode().PermanodeRef().String()
			req := httptest.NewRequest(http.MethodGet, "/importer/"+path+"?log=0", nil)
			req.Header.Set(httputil.PathSuffixHeader, path)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("account log status = %d", w.Code)
			}
			for name, text := range map[string]string{
				"error": err.Error(), "server log": logs.String(), "account log": w.Body.String(),
			} {
				if strings.Contains(text, secret) {
					t.Errorf("%s contains the credential: %s", name, text)
				}
			}
		})
	}
}

func TestCallbackDoesNotEchoInvalidToken(t *testing.T) {
	const token = "private-invalid-token"
	req := httptest.NewRequest(http.MethodGet, "/callback?apiToken="+token, nil)
	w := httptest.NewRecorder()
	(imp{}).ServeCallback(w, req, nil)
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), token) {
		t.Errorf("invalid-token response = %d %q", w.Code, w.Body.String())
	}
}
