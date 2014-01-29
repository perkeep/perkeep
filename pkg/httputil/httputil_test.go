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

package httputil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestCloseBody(t *testing.T) {
	const msg = "{\"foo\":\"bar\"}\r\n"
	addrSeen := make(map[string]int)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addrSeen[r.RemoteAddr]++
		w.Header().Set("Content-Length", strconv.Itoa(len(msg)))
		w.WriteHeader(200)
		w.Write([]byte(msg))
	}))
	defer ts.Close()

	buf := make([]byte, len(msg))

	for _, trim := range []int{0, 2} {
		for i := 0; i < 3; i++ {
			res, err := http.Get(ts.URL)
			if err != nil {
				t.Errorf("Get: %v", err)
				continue
			}
			want := len(buf) - trim
			n, err := res.Body.Read(buf[:want])
			CloseBody(res.Body)
			if n != want {
				t.Errorf("Read = %v; want %v", n, want)
			}
			if err != nil && err != io.EOF {
				t.Errorf("Read = %v", err)
			}
		}
	}
	if len(addrSeen) != 1 {
		t.Errorf("server saw %d distinct client addresses; want 1", len(addrSeen))
	}
}
