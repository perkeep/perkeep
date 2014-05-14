/*
Copyright 2014 The Camlistore Authors

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

package test

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"strings"

	"camlistore.org/pkg/types"
)

// NewFakeTransport takes a map of URL to function generating a response
// and returns an http.RoundTripper that does HTTP requests out of that.
func NewFakeTransport(urls map[string]func() *http.Response) http.RoundTripper {
	return fakeTransport(urls)
}

type fakeTransport map[string]func() *http.Response

func (m fakeTransport) RoundTrip(req *http.Request) (res *http.Response, err error) {
	urls := req.URL.String()
	fn, ok := m[urls]
	if !ok {
		return nil, fmt.Errorf("Unexpected FakeTransport URL requested: %s", urls)
	}
	return fn(), nil
}

// FileResponder returns an HTTP response generator that returns the
// contents of the named file.
func FileResponder(filename string) func() *http.Response {
	return func() *http.Response {
		f, err := os.Open(filename)
		if err != nil {
			return &http.Response{StatusCode: 404, Status: "404 Not Found", Body: types.EmptyBody}
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Body: f}
	}
}

// StaticResponder returns an HTTP response generator that parses res
// for an entire HTTP response, including headers and body.
func StaticResponder(res string) func() *http.Response {
	_, err := http.ReadResponse(bufio.NewReader(strings.NewReader(res)), nil)
	if err != nil {
		panic("Invalid response given to StaticResponder: " + err.Error())
	}
	return func() *http.Response {
		res, _ := http.ReadResponse(bufio.NewReader(strings.NewReader(res)), nil)
		return res
	}
}
