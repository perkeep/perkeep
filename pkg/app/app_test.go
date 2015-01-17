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

package app

import (
	"os"
	"testing"
)

func TestListenAddress(t *testing.T) {
	tests := []struct {
		baseURL  string
		wantAddr string
		wantErr  bool
	}{
		{
			baseURL:  "http://foo.com/",
			wantAddr: "foo.com:80",
		},

		{
			baseURL:  "https://foo.com/",
			wantAddr: "foo.com:443",
		},

		{
			baseURL:  "http://foo.com:8080/",
			wantAddr: "foo.com:8080",
		},

		{
			baseURL:  "https://foo.com:8080/",
			wantAddr: "foo.com:8080",
		},

		{
			baseURL:  "http://foo.com:/",
			wantAddr: "foo.com:",
		},

		{
			baseURL:  "https://foo.com:/",
			wantAddr: "foo.com:",
		},

		{
			baseURL:  "http://foo.com/bar/",
			wantAddr: "foo.com:80",
		},

		{
			baseURL:  "https://foo.com/bar/",
			wantAddr: "foo.com:443",
		},

		{
			baseURL:  "http://foo.com:8080/bar/",
			wantAddr: "foo.com:8080",
		},

		{
			baseURL:  "https://foo.com:8080/bar/",
			wantAddr: "foo.com:8080",
		},

		{
			baseURL:  "http://foo.com:/bar/",
			wantAddr: "foo.com:",
		},

		{
			baseURL:  "https://foo.com:/bar/",
			wantAddr: "foo.com:",
		},

		{
			baseURL: "",
			wantErr: true,
		},

		{
			baseURL:  "http://foo.com",
			wantAddr: "foo.com:80",
		},

		{
			baseURL:  "https://foo.com",
			wantAddr: "foo.com:443",
		},

		{
			baseURL:  "http://[::1]",
			wantAddr: "[::1]:80",
		},

		{
			baseURL:  "https://[fe80::a288:b4ff:fe49:627c]:8443",
			wantAddr: "[fe80::a288:b4ff:fe49:627c]:8443",
		},
	}
	for _, v := range tests {
		os.Setenv("CAMLI_APP_BACKEND_URL", v.baseURL)
		got, err := ListenAddress()
		if v.wantErr {
			if err == nil {
				t.Errorf("Wanted error for %v", v.baseURL)
			}
			continue
		}
		if err != nil {
			t.Error(err)
			continue
		}
		if got != v.wantAddr {
			t.Errorf("got: %v, want: %v", got, v.wantAddr)
		}
	}
}
