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

package webserver

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/auth"

	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/memnet"
	"tailscale.com/tsnet"
	"tailscale.com/types/key"
)

func TestTailscaleAuthContext(t *testing.T) {
	for _, tailnet := range []bool{false, true} {
		s := New()
		if tailnet {
			// Constructing the request context must not start a node.
			s.tsnetServer = new(tsnet.Server)
		}
		ctx := s.httpServer().BaseContext(nil)
		if got := TSNetCtxKey.Value(ctx); got != s.tsnetServer {
			t.Errorf("TSNetCtxKey = %p; want %p", got, s.tsnetServer)
		}
		if got := TailscaleCtxKey.Value(ctx); got != tailnet {
			t.Errorf("TailscaleCtxKey = %v; want %v", got, tailnet)
		}
		if got := auth.TailscaleWhoIsCtxKey.Value(ctx) != nil; got != tailnet {
			t.Errorf("identity lookup present = %v; want %v", got, tailnet)
		}
		am, err := auth.FromConfig("tailscale:full-access-to-tailnet")
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
		// A public request cannot gain access by claiming a tailnet address.
		req.RemoteAddr = "100.64.0.1:1234"
		want := auth.Operation(0)
		if tailnet {
			want = auth.OpAll
		}
		if got := am.AllowedAccess(req); got != want {
			t.Errorf("tailnet=%v: AllowedAccess = %v; want %v", tailnet, got, want)
		}
	}
}

// TestServeHTTP2 checks that a TLS server negotiates HTTP/2 with clients
// that support it, and still serves HTTP/1.1 to those that don't.
func TestServeHTTP2(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if _, err := httputil.GenSelfTLSFiles("localhost", certFile, keyFile); err != nil {
		t.Fatal(err)
	}
	s := New()
	s.SetTLS(TLSSetup{CertFile: certFile, KeyFile: keyFile})
	s.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Proto)
	})
	mln := memnet.Listen("localhost:443")
	ln, err := s.tlsListener(mln)
	if err != nil {
		t.Fatal(err)
	}
	srv := s.httpServer()
	go srv.Serve(ln)
	defer srv.Close()

	get := func(tr *http.Transport) string {
		t.Helper()
		tr.DialContext = mln.Dial
		defer tr.CloseIdleConnections()
		res, err := (&http.Client{Transport: tr}).Get("https://localhost/")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	// Each transport gets its own tls.Config, as one with HTTP/2 enabled
	// adds to its NextProtos.
	insecure := func() *tls.Config { return &tls.Config{InsecureSkipVerify: true} }
	var h1, h2 http.Protocols
	h1.SetHTTP1(true)
	h2.SetHTTP1(true)
	h2.SetHTTP2(true)
	if got := get(&http.Transport{TLSClientConfig: insecure(), Protocols: &h2}); got != "HTTP/2.0" {
		t.Errorf("HTTP/2 client got served %q; want HTTP/2.0", got)
	}
	if got := get(&http.Transport{TLSClientConfig: insecure(), Protocols: &h1}); got != "HTTP/1.1" {
		t.Errorf("HTTP/1.1 client got served %q; want HTTP/1.1", got)
	}
}

// TestServeHTTP1 checks that the plain HTTP/1.1 listener serves only
// under its prefix, and that even in Tailscale mode, its requests can't
// be authenticated by Tailscale.
func TestServeHTTP1(t *testing.T) {
	s := New()
	// Constructing the request context must not start a node.
	s.tsnetServer = new(tsnet.Server)
	s.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s tailscale=%v", r.Proto, auth.TailscaleWhoIsCtxKey.Value(r.Context()) != nil)
	})
	mln := memnet.Listen("localhost:2283")
	srv := s.http1Server("/immich/api/")
	go srv.Serve(mln)
	defer srv.Close()

	tr := &http.Transport{DialContext: mln.Dial}
	defer tr.CloseIdleConnections()
	get := func(path string) (int, string) {
		t.Helper()
		res, err := (&http.Client{Transport: tr}).Get("http://localhost:2283" + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(body)
	}
	if code, body := get("/immich/api/server/ping"); code != 200 || body != "HTTP/1.1 tailscale=false" {
		t.Errorf("under prefix: got %d %q; want 200 \"HTTP/1.1 tailscale=false\"", code, body)
	}
	if code, _ := get("/ui/"); code != http.StatusNotFound {
		t.Errorf("outside prefix: got %d; want 404", code)
	}
}

func TestWriteTailscaleStatus(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	k1, k2, k3 := key.NewNode().Public(), key.NewNode().Public(), key.NewNode().Public()
	st := &ipnstate.Status{
		BackendState: "Running",
		Self:         &ipnstate.PeerStatus{DNSName: "perkeep.example.ts.net.", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
		Peer: map[key.NodePublic]*ipnstate.PeerStatus{
			k1: {HostName: "phone", OS: "iOS", Active: true, CurAddr: "10.0.0.5:41641", RxBytes: 100, TxBytes: 200, LastHandshake: now.Add(-30 * time.Second), Online: true},
			k2: {HostName: "laptop", OS: "macOS", Active: true, Relay: "sfo", Online: true},
			k3: {HostName: "old", OS: "linux"},
		},
	}
	var buf strings.Builder
	writeTailscaleStatus(&buf, st, false, now)
	got := buf.String()
	for _, want := range []string{"Backend state: Running", "perkeep.example.ts.net", "direct 10.0.0.5:41641", "derp sfo", "30s ago", "(1 inactive peers not shown"} {
		if !strings.Contains(got, want) {
			t.Errorf("status lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old") {
		t.Errorf("status shows the inactive peer:\n%s", got)
	}
	if strings.Index(got, "laptop") > strings.Index(got, "phone") {
		t.Errorf("peers not sorted by name:\n%s", got)
	}
	buf.Reset()
	writeTailscaleStatus(&buf, st, true, now)
	if !strings.Contains(buf.String(), "old") {
		t.Errorf("all status lacks the inactive peer:\n%s", buf.String())
	}
}

func TestTailscaleDebugHandlerOffTailnet(t *testing.T) {
	rec := httptest.NewRecorder()
	TailscaleDebugHandler(rec, httptest.NewRequest("GET", "/debug/tailscale", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("off the tailnet, got %d; want 404", rec.Code)
	}
}

func TestStartupHandler(t *testing.T) {
	s := New()
	s.SetStartupHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "starting")
	}))
	s.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ready")
	})
	get := func() string {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		return rec.Body.String()
	}
	if got := get(); got != "starting" {
		t.Errorf("before EndStartup, got %q; want the startup handler's response", got)
	}
	s.EndStartup()
	if got := get(); got != "ready" {
		t.Errorf("after EndStartup, got %q; want the registered handler's response", got)
	}
}
