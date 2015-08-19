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

import "testing"

var rewritetests = []struct {
	in  string
	out string
}{
	// Valid URLs change the scheme, and add :433 iff there's no port.
	{"https://foo.bar:443", "http://foo.bar:443"},
	{"https://foo.bar", "http://foo.bar:443"},
	{"https://foo.bar/", "http://foo.bar:443/"},
	{"https://foo.bar:443/", "http://foo.bar:443/"},
	{"https://foo.bar:baz/", "http://foo.bar:baz/"},
	{"https://[::0]/", "http://[::0]:443/"},
	{"https://[::0]:82/", "http://[::0]:82/"},
	{"https://[2001:DB8::1]:80/", "http://[2001:DB8::1]:80/"},
	{"https://[2001:DB8:0:1]/", "http://[2001:DB8:0:1]:443/"},
	{"https://192.0.2.3/", "http://192.0.2.3:443/"},
	{"https://192.0.2.3:60/", "http://192.0.2.3:60/"},
	// Invalid URLs stay exactly the same.
	{"https://[2001:DB8::1:/", "https://[2001:DB8::1:/"},
	{"https://foo.bar:443:baz/", "https://foo.bar:443:baz/"},
	{"https://foo.bar:/", "https://foo.bar:/"},
	{"https://[2001:DB8::1]:/", "https://[2001:DB8::1]:/"},
}

func TestCondRewriteURL(t *testing.T) {
	c := &Client{InsecureTLS: true}
	c.initTrustedCertsOnce.Do(c.initTrustedCerts) // Initialise an empty list of trusted certs.
	c.server = "https://example.com/"
	for _, tt := range rewritetests {
		s := c.condRewriteURL(tt.in)
		if s != tt.out {
			t.Errorf("c.condRewriteURL(%q) => %q, want %q", tt.in, s, tt.out)
		}
	}
}
