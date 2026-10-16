/*
Copyright 2026 The Perkeep Authors

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
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"

	"go4.org/jsonconfig"
)

// listingS3 is an http.RoundTripper that serves an S3 ListObjectsV2 API
// for one bucket, with path-style URLs, from a list of object keys.
type listingS3 struct {
	keys []string // sorted

	served atomic.Int64 // objects returned in listings
}

func (f *listingS3) RoundTrip(req *http.Request) (*http.Response, error) {
	q := req.URL.Query()
	if req.Method != "GET" || q.Get("list-type") != "2" {
		return &http.Response{StatusCode: http.StatusNotImplemented, Body: http.NoBody, Request: req}, nil
	}
	prefix, delim, startAfter := q.Get("prefix"), q.Get("delimiter"), q.Get("start-after")
	maxKeys := 1000
	if v := q.Get("max-keys"); v != "" {
		maxKeys, _ = strconv.Atoi(v)
	}
	type object struct {
		Key  string
		Size int
	}
	type commonPrefix struct{ Prefix string }
	res := struct {
		XMLName        xml.Name       `xml:"ListBucketResult"`
		Prefix         string         `xml:"Prefix"`
		KeyCount       int            `xml:"KeyCount"`
		IsTruncated    bool           `xml:"IsTruncated"`
		Contents       []object       `xml:"Contents"`
		CommonPrefixes []commonPrefix `xml:"CommonPrefixes"`
	}{Prefix: prefix}
	for _, k := range f.keys {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok || k <= startAfter {
			continue
		}
		if res.KeyCount == maxKeys {
			res.IsTruncated = true
			break
		}
		if i := strings.Index(rest, delim); delim != "" && i >= 0 {
			cp := prefix + rest[:i+len(delim)]
			if n := len(res.CommonPrefixes); n == 0 || res.CommonPrefixes[n-1].Prefix != cp {
				res.CommonPrefixes = append(res.CommonPrefixes, commonPrefix{cp})
				res.KeyCount++
			}
			continue
		}
		res.Contents = append(res.Contents, object{Key: k, Size: 1})
		f.served.Add(1)
		res.KeyCount++
	}
	body, err := xml.Marshal(res)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/xml"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
		Request:    req,
	}, nil
}

// TestEnumerateSkipsSubdirectories checks that enumerating a storage
// doesn't list the objects in its subdirectories, such as those of a
// blobpacked storage below one at the root of the bucket.
func TestEnumerateSkipsSubdirectories(t *testing.T) {
	const (
		blob1 = "sha1-0000000a84aa1c10c44f0ca9365303d1b329d066"
		blob2 = "sha224-00000000000000000000000000000000000000000000000000000000"
	)
	var keys []string
	for i := range 50 {
		keys = append(keys, fmt.Sprintf("loose/sha1-%040d", i))
	}
	keys = append(keys, blob1, blob2, "packed/"+blob1)
	slices.Sort(keys)
	fake := &listingS3{keys: keys}
	sto, err := newFromConfigWithTransport(nil, jsonconfig.Obj{
		"aws_access_key":        "key",
		"aws_secret_access_key": "secret",
		"bucket":                "b",
		"hostname":              "s3.example",
		"cacheSize":             float64(0),
	}, fake)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := blobserver.EnumerateAll(t.Context(), sto, func(sb blob.SizedRef) error {
		got = append(got, sb.Ref.String())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{blob1, blob2}; !slices.Equal(got, want) {
		t.Errorf("enumerated %q; want %q", got, want)
	}
	if n := fake.served.Load(); n != 2 {
		t.Errorf("S3 returned %d objects; want only the 2 at the root", n)
	}
}
