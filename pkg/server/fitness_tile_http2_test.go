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

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFitnessTileHTTP2(t *testing.T) {
	data := fitnessTileTestPNG(t, 256, 256)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("tile protocol = %s, want HTTP/2", r.Proto)
		}
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write(data)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	// Keep the production default transport's protocol settings, changing only
	// proxying and trust roots to reach this local synthetic tile server.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	defer transport.CloseIdleConnections()
	source, err := newFitnessTileSource(server.URL+"/{z}/{x}/{y}.png", t.TempDir(), "", &http.Client{Transport: transport}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	source.limiter = &fitnessTileLimiter{slots: make(chan struct{}, 16)}
	if _, err := source.tile(context.Background(), 0, 0, 0, "", true); err != nil {
		t.Fatal(err)
	}
}
