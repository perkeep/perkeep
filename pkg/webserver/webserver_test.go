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
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"perkeep.org/internal/httputil"

	"tailscale.com/net/memnet"
)

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
