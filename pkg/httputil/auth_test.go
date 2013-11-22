/*
Copyright 2013 The Camlistore Authors

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
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testServer(t *testing.T, l net.Listener) *httptest.Server {
	ts := &httptest.Server{
		Listener: l,
		Config: &http.Server{
			Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				if IsLocalhost(r) {
					fmt.Fprintf(rw, "authorized")
					return
				}
				fmt.Fprintf(rw, "unauthorized")
			}),
		},
	}
	ts.Start()

	return ts
}

func TestLocalhostAuthIPv6(t *testing.T) {
	l, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("skipping IPv6 test; can't listen on [::1]:0")
	}
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	// See if IPv6 works on this machine first. It seems the above
	// Listen can pass on Linux but fail here in the dial.
	c, err := net.Dial("tcp6", l.Addr().String())
	if err != nil {
		t.Skipf("skipping IPv6 test; dial back to %s failed with %v", l.Addr(), err)
	}
	c.Close()

	ts := testServer(t, l)
	defer ts.Close()

	// Use an explicit transport to force IPv6 (http.Get resolves localhost in IPv4 otherwise)
	trans := &http.Transport{
		Dial: func(network, addr string) (net.Conn, error) {
			c, err := net.Dial("tcp6", addr)
			return c, err
		},
	}

	testLoginRequest(t, &http.Client{Transport: trans}, "http://[::1]:"+port)

	// See if we can get an IPv6 from resolving localhost
	localips, err := net.LookupIP("localhost")
	if err != nil {
		t.Skipf("skipping IPv6 test; resolving localhost failed with %v", err)
	}
	if hasIPv6(localips) {
		testLoginRequest(t, &http.Client{Transport: trans}, "http://localhost:"+port)
	} else {
		t.Logf("incomplete IPv6 test; resolving localhost didn't return any IPv6 addresses")
	}
}

func hasIPv6(ips []net.IP) bool {
	for _, ip := range ips {
		if ip.To4() == nil {
			return true
		}
	}
	return false
}

func TestLocalhostAuthIPv4(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("skipping IPv4 test; can't listen on 127.0.0.1:0")
	}
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	ts := testServer(t, l)
	defer ts.Close()

	testLoginRequest(t, &http.Client{}, "http://127.0.0.1:"+port)
	testLoginRequest(t, &http.Client{}, "http://localhost:"+port)
}

func testLoginRequest(t *testing.T, client *http.Client, URL string) {
	res, err := client.Get(URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	const exp = "authorized"
	if string(body) != exp {
		t.Errorf("got %q (instead of %v)", string(body), exp)
	}
}

func TestBasicAuth(t *testing.T) {
	for _, d := range []struct {
		header string
		u, pw  string
		valid  bool
	}{
		// Empty is invalid.
		{},
		{
			// Missing password.
			header: "Basic QWxhZGRpbg==",
		},
		{
			// Malformed base64 encoding.
			header: "Basic foo",
		},
		{
			// Malformed header, no 'Basic ' prefix.
			header: "QWxhZGRpbg==",
		},
		{
			header: "Basic QWxhZGRpbjpvcGVuIHNlc2FtZQ==",
			u:      "Aladdin",
			pw:     "open sesame",
			valid:  true,
		},
	} {
		req, err := http.NewRequest("GET", "/", nil)
		if err != nil {
			t.Fatal(err)
		}
		if d.header != "" {
			req.Header.Set("Authorization", d.header)
		}

		u, pw, err := BasicAuth(req)
		t.Log(d.header, err)
		if d.valid && err != nil {
			t.Error("Want success parse of auth header, got", err)
		}
		if !d.valid && err == nil {
			t.Error("Want error parsing", d.header)
		}

		if d.u != u {
			t.Errorf("Want user %q, got %q", d.u, u)
		}

		if d.pw != pw {
			t.Errorf("Want password %q, got %q", d.pw, pw)
		}
	}
}
