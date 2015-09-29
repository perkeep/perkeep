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

package s3

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
	var a Auth
	tests := []reqAndExpected{
		{`GET /photos/puppy.jpg HTTP/1.1
Host: johnsmith.s3.amazonaws.com
Date: Tue, 27 Mar 2007 19:36:42 +0000

`,
			"GET\n\n\nTue, 27 Mar 2007 19:36:42 +0000\n/johnsmith/photos/puppy.jpg"},
		{`PUT /photos/puppy.jpg HTTP/1.1
Content-Type: image/jpeg
Content-Length: 94328
Host: johnsmith.s3.amazonaws.com
Date: Tue, 27 Mar 2007 21:15:45 +0000

`,
			"PUT\n\nimage/jpeg\nTue, 27 Mar 2007 21:15:45 +0000\n/johnsmith/photos/puppy.jpg"},
		{`GET /?prefix=photos&max-keys=50&marker=puppy HTTP/1.1
User-Agent: Mozilla/5.0
Host: johnsmith.s3.amazonaws.com
Date: Tue, 27 Mar 2007 19:42:41 +0000

`,
			"GET\n\n\nTue, 27 Mar 2007 19:42:41 +0000\n/johnsmith/"},
		{`DELETE /johnsmith/photos/puppy.jpg HTTP/1.1
User-Agent: dotnet
Host: s3.amazonaws.com
Date: Tue, 27 Mar 2007 21:20:27 +0000
x-amz-date: Tue, 27 Mar 2007 21:20:26 +0000

`,
			"DELETE\n\n\n\nx-amz-date:Tue, 27 Mar 2007 21:20:26 +0000\n/johnsmith/photos/puppy.jpg"},
		{`PUT /db-backup.dat.gz HTTP/1.1
User-Agent: curl/7.15.5
Host: static.johnsmith.net:8080
Date: Tue, 27 Mar 2007 21:06:08 +0000
x-amz-acl: public-read
content-type: application/x-download
Content-MD5: 4gJE4saaMU4BqNR0kLY+lw==
X-Amz-Meta-ReviewedBy: joe@johnsmith.net
X-Amz-Meta-ReviewedBy: jane@johnsmith.net
X-Amz-Meta-FileChecksum: 0x02661779
X-Amz-Meta-ChecksumAlgorithm: crc32
Content-Disposition: attachment; filename=database.dat
Content-Encoding: gzip
Content-Length: 5913339

`,
			"PUT\n4gJE4saaMU4BqNR0kLY+lw==\napplication/x-download\nTue, 27 Mar 2007 21:06:08 +0000\nx-amz-acl:public-read\nx-amz-meta-checksumalgorithm:crc32\nx-amz-meta-filechecksum:0x02661779\nx-amz-meta-reviewedby:joe@johnsmith.net,jane@johnsmith.net\n/static.johnsmith.net/db-backup.dat.gz"},
	}
	for idx, test := range tests {
		got := a.stringToSign(req(test.req))
		if got != test.expected {
			t.Errorf("test %d: expected %q", idx, test.expected)
			t.Errorf("test %d:      got %q", idx, got)
		}
	}
}

func TestBucketFromHostname(t *testing.T) {
	var a Auth
	tests := []reqAndExpected{
		{"GET / HTTP/1.0\n\n", ""},
		{"GET / HTTP/1.0\nHost: s3.amazonaws.com\n\n", ""},
		{"GET / HTTP/1.0\nHost: foo.s3.amazonaws.com\n\n", "foo"},
		{"GET / HTTP/1.0\nHost: foo.com:123\n\n", "foo.com"},
		{"GET / HTTP/1.0\nHost: bar.com\n\n", "bar.com"},
	}
	for idx, test := range tests {
		got := a.bucketFromHostname(req(test.req))
		if got != test.expected {
			t.Errorf("test %d: expected %q; got %q", idx, test.expected, got)
		}
	}
}

func TestSignRequest(t *testing.T) {
	r := req("GET /foo HTTP/1.1\n\n")
	auth := &Auth{AccessKey: "key", SecretAccessKey: "secretkey"}
	auth.SignRequest(r)
	if r.Header.Get("Date") == "" {
		t.Error("expected a Date set")
	}
	r.Header.Set("Date", "Sat, 02 Apr 2011 04:23:52 GMT")
	auth.SignRequest(r)
	if e, g := r.Header.Get("Authorization"), "AWS key:kHpCR/N7Rw3PwRlDd8+5X40CFVc="; e != g {
		t.Errorf("got header %q; expected %q", g, e)
	}
}

func TestHasDotSuffix(t *testing.T) {
	if !hasDotSuffix("foo.com", "com") {
		t.Fail()
	}
	if hasDotSuffix("foocom", "com") {
		t.Fail()
	}
	if hasDotSuffix("com", "com") {
		t.Fail()
	}
}
