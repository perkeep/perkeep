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
	"bytes"
	"crypto/md5"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"camlistore.org/pkg/test/dockertest"

	"go4.org/syncutil"
)

var (
	tc          *Client
	containerID dockertest.ContainerID // for running fake-s3
)

func getTestClient(t *testing.T) {
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secret := os.Getenv("AWS_ACCESS_KEY_SECRET")
	if accessKey != "" && secret != "" {
		tc = &Client{
			Auth:      &Auth{AccessKey: accessKey, SecretAccessKey: secret},
			Transport: http.DefaultTransport,
			PutGate:   syncutil.NewGate(5),
		}
		return
	}
	t.Logf("no AWS_ACCESS_KEY_ID or AWS_ACCESS_KEY_SECRET set in environment; trying against local fakes3 instead.")
	var ip string
	containerID, ip = dockertest.SetupFakeS3Container(t)
	hostname := ip + ":4567"
	tc = &Client{
		Auth:      &Auth{AccessKey: "foo", SecretAccessKey: "bar", Hostname: hostname},
		Transport: http.DefaultTransport,
		PutGate:   syncutil.NewGate(5),
		NoSSL:     true,
	}
}

func TestBuckets(t *testing.T) {
	getTestClient(t)
	defer containerID.KillRemove(t)
	_, err := tc.Buckets()
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseBuckets(t *testing.T) {
	res := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<ListAllMyBucketsResult xmlns=\"http://s3.amazonaws.com/doc/2006-03-01/\"><Owner><ID>ownerIDField</ID><DisplayName>bobDisplayName</DisplayName></Owner><Buckets><Bucket><Name>bucketOne</Name><CreationDate>2006-06-21T07:04:31.000Z</CreationDate></Bucket><Bucket><Name>bucketTwo</Name><CreationDate>2006-06-21T07:04:32.000Z</CreationDate></Bucket></Buckets></ListAllMyBucketsResult>"
	buckets, err := parseListAllMyBuckets(strings.NewReader(res))
	if err != nil {
		t.Fatal(err)
	}
	if g, w := len(buckets), 2; g != w {
		t.Errorf("num parsed buckets = %d; want %d", g, w)
	}
	want := []*Bucket{
		{Name: "bucketOne", CreationDate: "2006-06-21T07:04:31.000Z"},
		{Name: "bucketTwo", CreationDate: "2006-06-21T07:04:32.000Z"},
	}
	dump := func(v []*Bucket) {
		for i, b := range v {
			t.Logf("Bucket #%d: %#v", i, b)
		}
	}
	if !reflect.DeepEqual(buckets, want) {
		t.Error("mismatch; GOT:")
		dump(buckets)
		t.Error("WANT:")
		dump(want)
	}
}

func TestValidBucketNames(t *testing.T) {
	m := []struct {
		in   string
		want bool
	}{
		{"myawsbucket", true},
		{"my.aws.bucket", true},
		{"my-aws-bucket.1", true},
		{"my---bucket.1", true},
		{".myawsbucket", false},
		{"-myawsbucket", false},
		{"myawsbucket.", false},
		{"myawsbucket-", false},
		{"my..awsbucket", false},
	}

	for _, bt := range m {
		got := IsValidBucket(bt.in)
		if got != bt.want {
			t.Errorf("func(%q) = %v; want %v", bt.in, got, bt.want)
		}
	}
}

func TestPutObject(t *testing.T) {
	getTestClient(t)
	defer containerID.KillRemove(t)
	var buf bytes.Buffer
	md5h := md5.New()

	size, err := io.Copy(io.MultiWriter(&buf, md5h), strings.NewReader("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	// TODO(mpl): figure how to make fake-s3 work with buckets.
	if err = tc.PutObject("hello.txt", "", md5h, size, &buf); err != nil {
		t.Fatal(err)
	}
	// TODO(mpl): figure out why Stat of newly uploaded object does not match size from above.
}
