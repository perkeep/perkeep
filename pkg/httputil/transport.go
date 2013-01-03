/*
Copyright 2011 Google Inc.

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

package httputil

import (
	"log"
	"net/http"
	"sync"
)

// StatsTransport wraps another RoundTripper (or uses the default one) and
// counts the number of HTTP requests performed.
type StatsTransport struct {
	mu   sync.Mutex
	reqs int

	// Transport optionally specifies the transport to use.
	// If nil, http.DefaultTransport is used.
	Transport http.RoundTripper

	// If VerboseLog is true, HTTP request summaries are logged.
	VerboseLog bool
}

func (t *StatsTransport) Requests() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reqs
}

func (t *StatsTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	t.mu.Lock()
	t.reqs++
	t.mu.Unlock()

	rt := t.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	if t.VerboseLog {
		log.Printf("%s %s", req.Method, req.URL)
	}
	return rt.RoundTrip(req)
}
