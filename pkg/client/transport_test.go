// +build fake_android

/*
Copyright 2015 The Camlistore Authors.

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
	"net/http"
	"testing"

	"camlistore.org/pkg/client/android"
	"camlistore.org/pkg/httputil"

	"golang.org/x/net/http2"
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

func TestTransportSetup(t *testing.T) {
	sayNil := func(isNil bool) string {
		if isNil {
			return "nil"
		}
		return "not nil"
	}
	for tti, tt := range transportTests {
		cl := &Client{
			paramsOnly:         true,
			server:             tt.server,
			trustedCerts:       tt.trustedCerts,
			insecureAnyTLSCert: tt.insecureTLS,
		}
		android.OnAndroidHook = func() bool {
			return tt.onAndroid
		}
		rt := cl.transportForConfig(nil)
		if tt.http2 {
			tr, ok := rt.(*httputil.StatsTransport).Transport.(*http2.Transport)
			if !ok {
				t.Fatalf("test %d for %#v: got %T, wanted *http2.Transport", tti, tt, rt)
			}
			if tt.dialTLSFunc != (tr.DialTLS != nil) {
				t.Errorf("test %d for %#v: dialTLSFunc should be %v", tti, tt, sayNil(!tt.dialTLSFunc))
			}
			continue
		}

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
	}
}
