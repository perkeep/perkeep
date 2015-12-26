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

package netutil

import (
	"net"
	"strconv"
	"testing"
)

func TestHostPort(t *testing.T) {
	tests := []struct {
		baseURL     string
		wantNetAddr string
	}{
		// IPv4, no prefix
		{
			baseURL:     "http://foo.com/",
			wantNetAddr: "foo.com:80",
		},

		{
			baseURL:     "https://foo.com/",
			wantNetAddr: "foo.com:443",
		},

		{
			baseURL:     "http://foo.com:8080/",
			wantNetAddr: "foo.com:8080",
		},

		{
			baseURL:     "https://foo.com:8080/",
			wantNetAddr: "foo.com:8080",
		},

		// IPv4, with prefix
		{
			baseURL:     "http://foo.com/pics/",
			wantNetAddr: "foo.com:80",
		},

		{
			baseURL:     "https://foo.com/pics/",
			wantNetAddr: "foo.com:443",
		},

		{
			baseURL:     "http://foo.com:8080/pics/",
			wantNetAddr: "foo.com:8080",
		},

		{
			baseURL:     "https://foo.com:8080/pics/",
			wantNetAddr: "foo.com:8080",
		},

		// IPv6, no prefix
		{
			baseURL:     "http://[::1]/",
			wantNetAddr: "[::1]:80",
		},

		{
			baseURL:     "https://[::1]/",
			wantNetAddr: "[::1]:443",
		},

		{
			baseURL:     "http://[::1]:8080/",
			wantNetAddr: "[::1]:8080",
		},

		{
			baseURL:     "https://[::1]:8080/",
			wantNetAddr: "[::1]:8080",
		},

		// IPv6, with prefix
		{
			baseURL:     "http://[::1]/pics/",
			wantNetAddr: "[::1]:80",
		},

		{
			baseURL:     "https://[::1]/pics/",
			wantNetAddr: "[::1]:443",
		},

		{
			baseURL:     "http://[::1]:8080/pics/",
			wantNetAddr: "[::1]:8080",
		},

		{
			baseURL:     "https://[::1]:8080/pics/",
			wantNetAddr: "[::1]:8080",
		},
	}
	for _, v := range tests {
		got, err := HostPort(v.baseURL)
		if err != nil {
			t.Error(err)
			continue
		}
		if got != v.wantNetAddr {
			t.Errorf("got: %v for %v, want: %v", got, v.baseURL, v.wantNetAddr)
		}
	}
}

func TestListenHostPort(t *testing.T) {
	tests := []struct {
		in   string
		want string // or "ERR:"
	}{
		{":80", "localhost:80"},
		{"0.0.0.0:80", "localhost:80"},
		{"foo:80", "foo:80"},
		{"foo:0", "foo:0"},
		{"", "ERR"},
	}
	for _, tt := range tests {
		got, err := ListenHostPort(tt.in)
		if err != nil {
			got = "ERR"
		}
		if got != tt.want {
			t.Errorf("ListenHostPort(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}

}

func testLocalhostResolver(t *testing.T, resolve func() net.IP) {
	ip := resolve()
	if ip == nil {
		t.Fatal("no ip found.")
	}
	if !ip.IsLoopback() {
		t.Errorf("expected a loopback address: %s", ip)
	}
}

func testLocalhost(t *testing.T) {
	testLocalhostResolver(t, localhostLookup)
}

func testLoopbackIp(t *testing.T) {
	testLocalhostResolver(t, loopbackIP)
}

func TestLocalhost(t *testing.T) {
	_, err := Localhost()
	if err != nil {
		t.Fatal(err)
	}
}

func TestListenOnLocalRandomPort(t *testing.T) {
	l, err := ListenOnLocalRandomPort()
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	defer l.Close()

	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := strconv.Atoi(port); p < 1 {
		t.Fatalf("expected port(%d) to be > 0", p)
	}
}

func BenchmarkLocalhostLookup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if ip := localhostLookup(); ip == nil {
			b.Fatal("no ip found.")
		}
	}
}

func BenchmarkLoopbackIP(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if ip := loopbackIP(); ip == nil {
			b.Fatal("no ip found.")
		}
	}
}
