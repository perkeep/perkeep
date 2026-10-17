/*
Copyright 2015 The Perkeep Authors.

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

package client

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"perkeep.org/internal/hashutil"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/client/android"

	"tailscale.com/net/memnet"
	"tailscale.com/tstest"
)

var transportTests = []struct {
	// input
	server       string
	onAndroid    bool
	trustedCerts []string
	insecureTLS  bool
	// ouptput
	dialFunc    bool // whether the transport's Dial is not nil
	dialTLSFunc bool // whether the transport's DialTLS is not nil
	http2       bool // whether we're on http2
}{
	// All http, not android.
	{
		server:       "http://example.com",
		onAndroid:    false,
		trustedCerts: nil,
		insecureTLS:  false,
		dialFunc:     false,
		dialTLSFunc:  false,
		http2:        false,
	},
	{
		server:       "http://example.com",
		onAndroid:    false,
		trustedCerts: nil,
		insecureTLS:  true,
		dialFunc:     false,
		dialTLSFunc:  false,
		http2:        false,
	},
	{
		server:       "http://example.com",
		onAndroid:    false,
		trustedCerts: []string{"whatever"},
		insecureTLS:  false,
		dialFunc:     false,
		dialTLSFunc:  false,
		http2:        false,
	},

	// All http, on android.
	{
		server:       "http://example.com",
		onAndroid:    true,
		trustedCerts: nil,
		insecureTLS:  false,
		dialFunc:     true,
		dialTLSFunc:  false,
		http2:        false,
	},
	{
		server:       "http://example.com",
		onAndroid:    true,
		trustedCerts: nil,
		insecureTLS:  true,
		dialFunc:     true,
		dialTLSFunc:  false,
		http2:        false,
	},
	{
		server:       "http://example.com",
		onAndroid:    true,
		trustedCerts: []string{"whatever"},
		insecureTLS:  false,
		dialFunc:     true,
		dialTLSFunc:  false,
		http2:        false,
	},

	// All https, not android, hence http2.
	{
		server:       "https://example.com",
		onAndroid:    false,
		trustedCerts: nil,
		insecureTLS:  false,
		dialFunc:     false,
		dialTLSFunc:  false,
		http2:        true,
	},
	{
		server:       "https://example.com",
		onAndroid:    false,
		trustedCerts: nil,
		insecureTLS:  true,
		dialFunc:     false,
		dialTLSFunc:  true,
		http2:        true,
	},
	{
		server:       "https://example.com",
		onAndroid:    false,
		trustedCerts: []string{"whatever"},
		insecureTLS:  false,
		dialFunc:     false,
		dialTLSFunc:  true,
		http2:        true,
	},

	// All https, on android.
	{
		server:       "https://example.com",
		onAndroid:    true,
		trustedCerts: nil,
		insecureTLS:  false,
		dialFunc:     false,
		dialTLSFunc:  true,
		http2:        false,
	},
	{
		server:       "https://example.com",
		onAndroid:    true,
		trustedCerts: nil,
		insecureTLS:  true,
		dialFunc:     false,
		dialTLSFunc:  true,
		http2:        false,
	},
	{
		server:       "https://example.com",
		onAndroid:    true,
		trustedCerts: []string{"whatever"},
		insecureTLS:  false,
		dialFunc:     false,
		dialTLSFunc:  true,
		http2:        false,
	},
}

// TestTransportProtocols checks that the client's own TLS dialing (for
// trusted certificate fingerprints, or no verification) negotiates
// HTTP/2 with a server that supports it, unless HTTP/2 is off, as with a
// proxy.
func TestTransportProtocols(t *testing.T) {
	certPEM, keyPEM, err := httputil.GenSelfTLS("localhost")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	mln := memnet.Listen("localhost:443")
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Proto)
	})}
	go srv.Serve(tls.NewListener(mln, &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2", "http/1.1"},
	}))
	defer srv.Close()
	tstest.AssertNotParallel(t)
	tstest.Replace(t, &android.OnAndroidHook, func() bool { return false })
	fingerprint := hashutil.SHA256Prefix(cert.Certificate[0])

	tests := []struct {
		name string
		cl   *Client
		tc   *TransportConfig
		want string
	}{
		{"insecure", &Client{insecureAnyTLSCert: true}, nil, "HTTP/2.0"},
		{"trusted cert", &Client{trustedCerts: []string{fingerprint}}, nil, "HTTP/2.0"},
		{"proxy", &Client{insecureAnyTLSCert: true}, &TransportConfig{
			Proxy: func(*http.Request) (*url.URL, error) { return nil, nil }, // none, but HTTP/2 is off
		}, "HTTP/1.1"},
		{"untrusted cert", &Client{trustedCerts: []string{"sha256-0123456789"}}, nil, ""}, // fails
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cl.noExtConfig = true
			tt.cl.server = "https://localhost"
			tt.cl.testTLSNetDial = mln.Dial
			hc := &http.Client{Transport: tt.cl.transportForConfig(tt.tc)}
			defer hc.CloseIdleConnections()
			res, err := hc.Get("https://localhost/")
			if tt.want == "" {
				if err == nil || !strings.Contains(err.Error(), "untrusted certificate") {
					t.Fatalf("Get = %v; want an untrusted certificate error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != tt.want {
				t.Errorf("served %q; want %q", body, tt.want)
			}
		})
	}
}

func TestTransportSetup(t *testing.T) {
	sayNil := func(isNil bool) string {
		if isNil {
			return "nil"
		}
		return "not nil"
	}
	tstest.AssertNotParallel(t)
	for tti, tt := range transportTests {
		cl := &Client{
			noExtConfig:        true,
			server:             tt.server,
			trustedCerts:       tt.trustedCerts,
			insecureAnyTLSCert: tt.insecureTLS,
		}
		tstest.Replace(t, &android.OnAndroidHook, func() bool {
			return tt.onAndroid
		})
		rt := cl.transportForConfig(nil)
		var tr *http.Transport
		var ok bool
		if tt.onAndroid {
			tr, ok = rt.(*android.StatsTransport).Rt.(*httputil.StatsTransport).Transport.(*http.Transport)
			if !ok {
				t.Fatalf("test %d for %#v: got %T, wanted *http.Transport", tti, tt, rt)
			}
		} else {
			tr, ok = rt.(*httputil.StatsTransport).Transport.(*http.Transport)
			if !ok {
				t.Fatalf("test %d for %#v: got %T, wanted *http.Transport", tti, tt, rt)
			}
		}
		if tt.dialTLSFunc != (tr.DialTLS != nil) {
			t.Errorf("test %d for %#v: dialTLSFunc should be %v", tti, tt, sayNil(!tt.dialTLSFunc))
		}
		if tt.dialFunc != (tr.Dial != nil) {
			t.Errorf("test %d for %#v: dialFunc should be %v", tti, tt, sayNil(!tt.dialFunc))
		}
		if got := tr.Protocols != nil && tr.Protocols.HTTP2(); got != tt.http2 {
			t.Errorf("test %d for %#v: HTTP/2 enabled = %v; want %v", tti, tt, got, tt.http2)
		}
		want := maxParallelHTTP_h1
		if tt.http2 {
			want = maxParallelHTTP_h2
		}
		if got := httpGateSize(rt); got != want {
			t.Errorf("test %d for %#v: httpGateSize = %d; want %d", tti, tt, got, want)
		}
	}
}
