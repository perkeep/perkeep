/*
Copyright 2014 The Perkeep Authors

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

package storage

import (
	"bufio"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type reqAndExpected struct {
	req, expected string
}

func req(s string) *http.Request {
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(s)))
	if err != nil {
		panic(fmt.Sprintf("bad request in test: %v (error: %v)", req, err))
	}
	return req
}

func TestStringToSign(t *testing.T) {
	a := Auth{
		Account: "johnsmith",
	}
	tests := []reqAndExpected{
		{`GET /photos/puppy.jpg HTTP/1.1
Host: johnsmith.blob.core.windows.net
Date: Tue, 27 Mar 2007 19:36:42 +0000

`,
			"GET\n\n\n\n\n\nTue, 27 Mar 2007 19:36:42 +0000\n\n\n\n\n\n/johnsmith/photos/puppy.jpg"},
		{`PUT /photos/puppy.jpg HTTP/1.1
Content-Type: image/jpeg
Content-Length: 94328
Host: johnsmith.blob.core.windows.net
Date: Tue, 27 Mar 2007 21:15:45 +0000

`,
			"PUT\n\n\n94328\n\nimage/jpeg\nTue, 27 Mar 2007 21:15:45 +0000\n\n\n\n\n\n/johnsmith/photos/puppy.jpg"},
		{`GET /?prefix=photos&maxResults=50&marker=puppy HTTP/1.1
User-Agent: Mozilla/5.0
Host: johnsmith.blob.core.windows.net
Date: Tue, 27 Mar 2007 19:42:41 +0000

`,
			"GET\n\n\n\n\n\nTue, 27 Mar 2007 19:42:41 +0000\n\n\n\n\n\n/johnsmith/\nmarker:puppy\nmaxresults:50\nprefix:photos"},
		{`DELETE /photos/puppy.jpg HTTP/1.1
User-Agent: dotnet
Host: blob.core.windows.net
Date: Tue, 27 Mar 2007 21:20:27 +0000
x-ms-date: Tue, 27 Mar 2007 21:20:26 +0000

`,
			"DELETE\n\n\n\n\n\n\n\n\n\n\n\nx-ms-date:Tue, 27 Mar 2007 21:20:26 +0000\n/johnsmith/photos/puppy.jpg"},
	}
	for idx, test := range tests {
		got := a.stringToSign(req(test.req))
		if got != test.expected {
			t.Errorf("test %d: expected %q", idx, test.expected)
			t.Errorf("test %d:      got %q", idx, got)
		}
	}
}

func TestSignRequest(t *testing.T) {
	r := req("GET /foo HTTP/1.1\n\n")
	auth := &Auth{Account: "account", AccessKey: []byte("secretkey")}
	auth.SignRequest(r)
	if r.Header.Get("Date") == "" {
		t.Error("expected a Date set")
	}
	r.Header.Set("Date", "Sat, 02 Apr 2011 04:23:52 GMT")
	auth.SignRequest(r)
	if g, e := r.Header.Get("Authorization"), "SharedKey account:3wUs3/Wpwi/ReZCrTDbMxPEcY8Nnn1E4shuw8IvAKFw="; e != g {
		t.Errorf("got header %q; expected %q", g, e)
	}
}
