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

package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
)

func TestTailscaleAuth(t *testing.T) {
	for _, tt := range []struct {
		name, mode, login string
		tailnet           bool
		err               error
		want              Operation
		wantLookup        bool
	}{
		{name: "public full access", mode: "full-access-to-tailnet"},
		{name: "public named user", mode: "alice@example.com"},
		{name: "tailnet full access", mode: "full-access-to-tailnet", tailnet: true, want: OpAll},
		{name: "matching user", mode: "alice@example.com", tailnet: true, login: "alice@example.com", want: OpAll, wantLookup: true},
		{name: "different user", mode: "alice@example.com", tailnet: true, login: "bob@example.com", wantLookup: true},
		{name: "case-sensitive login", mode: "alice@example.com", tailnet: true, login: "Alice@example.com", wantLookup: true},
		{name: "empty login", mode: "alice@example.com", tailnet: true, wantLookup: true},
		{name: "lookup failure", mode: "alice@example.com", tailnet: true, err: errors.New("whois failed"), wantLookup: true},
		{name: "matching login with lookup failure", mode: "alice@example.com", tailnet: true, login: "alice@example.com", err: errors.New("whois failed"), wantLookup: true},
		{name: "canceled", mode: "alice@example.com", tailnet: true, err: context.Canceled, wantLookup: true},
		{name: "wrapped cancellation", mode: "alice@example.com", tailnet: true, err: errors.Join(errors.New("whois failed"), context.Canceled), wantLookup: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			am, err := newTailscaleAuth(tt.mode)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "http://example.com/", nil)
			called := false
			if tt.tailnet {
				ctx := TailscaleWhoIsCtxKey.WithValue(req.Context(), func(ctx context.Context, addr string) (string, error) {
					called = true
					if ctx != req.Context() || addr != req.RemoteAddr {
						t.Error("WhoIs did not receive the request context and remote address")
					}
					return tt.login, tt.err
				})
				req = req.WithContext(ctx)
			}
			if got := am.AllowedAccess(req); got != tt.want {
				t.Errorf("AllowedAccess = %v; want %v", got, tt.want)
			}
			if called != tt.wantLookup {
				t.Errorf("WhoIs called = %v; want %v", called, tt.wantLookup)
			}
			am.AddAuthHeader(req)
			if len(req.Header) != 0 {
				t.Errorf("AddAuthHeader changed headers: %v", req.Header)
			}
		})
	}
	if _, err := newTailscaleAuth("invalid"); err == nil {
		t.Error("invalid auth mode accepted")
	}
}

func TestTailscaleAuthUntrustedRequest(t *testing.T) {
	for _, mode := range []string{"full-access-to-tailnet", "alice@example.com"} {
		t.Run(mode, func(t *testing.T) {
			am, err := newTailscaleAuth(mode)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "http://example.com/", nil)
			req.RemoteAddr = "100.64.0.1:1234"
			req.Header.Set("Tailscale-User-Login", "alice@example.com")
			req.Header.Set("X-Forwarded-For", "100.64.0.1")
			if got := am.AllowedAccess(req); got != 0 {
				t.Fatalf("untrusted request got access %v", got)
			}
			// A typed nil value must not establish listener trust either.
			ctx := TailscaleWhoIsCtxKey.WithValue(req.Context(), nil)
			if got := am.AllowedAccess(req.WithContext(ctx)); got != 0 {
				t.Fatalf("nil identity lookup got access %v", got)
			}
		})
	}
}
