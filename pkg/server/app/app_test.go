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
	"regexp"
	"testing"
)

func TestRandPortBackendURL(t *testing.T) {
	tests := []struct {
		apiHost          string
		appHandlerPrefix string
		wantBackendURL   string
		wantErr          bool
	}{
		{
			apiHost:          "http://foo.com/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   "http://foo.com:[0-9]+/pics/",
		},

		{
			apiHost:          "https://foo.com/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   "https://foo.com:[0-9]+/pics/",
		},

		{
			apiHost:          "http://foo.com:8080/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   "http://foo.com:[0-9]+/pics/",
		},

		{
			apiHost:          "https://foo.com:8080/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   "https://foo.com:[0-9]+/pics/",
		},

		{
			apiHost:          "http://foo.com:/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   "http://foo.com:[0-9]+/pics/",
		},

		{
			apiHost:          "https://foo.com:/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   "https://foo.com:[0-9]+/pics/",
		},

		{
			apiHost:          "http://foo.com/bar/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   "http://foo.com:[0-9]+/pics/",
		},

		{
			apiHost:          "https://foo.com/bar/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   "https://foo.com:[0-9]+/pics/",
		},

		{
			apiHost:          "http://foo.com:8080/bar/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   "http://foo.com:[0-9]+/pics/",
		},

		{
			apiHost:          "https://foo.com:8080/bar/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   "https://foo.com:[0-9]+/pics/",
		},

		{
			apiHost:          "http://foo.com:/bar/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   "http://foo.com:[0-9]+/pics/",
		},

		{
			apiHost:          "https://foo.com:/bar/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   "https://foo.com:[0-9]+/pics/",
		},

		{
			apiHost:          "http://[::1]:80/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   `http://\[::1\]:[0-9]+/pics/`,
		},

		{
			apiHost:          "https://[::1]:80/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   `https://\[::1\]:[0-9]+/pics/`,
		},

		{
			apiHost:          "http://[::1]/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   `http://\[::1\]:[0-9]+/pics/`,
		},

		{
			apiHost:          "https://[::1]/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   `https://\[::1\]:[0-9]+/pics/`,
		},

		{
			apiHost:          "http://[::1]:/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   `http://\[::1\]:[0-9]+/pics/`,
		},

		{
			apiHost:          "https://[::1]:/",
			appHandlerPrefix: "/pics/",
			wantBackendURL:   `https://\[::1\]:[0-9]+/pics/`,
		},
	}
	for _, v := range tests {
		got, err := randPortBackendURL(v.apiHost, v.appHandlerPrefix)
		if err != nil {
			t.Error(err)
			continue
		}
		reg := regexp.MustCompile(v.wantBackendURL)
		if !reg.MatchString(got) {
			t.Errorf("got: %v for %v, want: %v", got, v.apiHost, v.wantBackendURL)
		}
	}
}
