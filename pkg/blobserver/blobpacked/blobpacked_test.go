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

package blobpacked

import (
	"bytes"
	"math/rand"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/test"
)

func TestStorage(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
		return &storage{
			small: new(test.Fetcher),
			large: new(test.Fetcher),
			meta:  sorted.NewMemoryKeyValue(),
		}, func() {}
	})
}

func TestParseMetaRow(t *testing.T) {
	cases := []struct {
		in   string
		want meta
		err  bool
	}{
		{in: "123 s", want: meta{exists: true, size: 123}},
		{in: "123 sx", err: true},
		{in: "-123 s", err: true},
		{in: "", err: true},
		{in: "1 ", err: true},
		{in: " ", err: true},
		{in: "123 x", err: true},
		{in: "123 l", err: true},
		{in: "123 l sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15", err: true},
		{in: "123 l notaref 12", err: true},
		{in: "123 l sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15 42 extra", err: true},
		{in: "123 l sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15 42 ", err: true},
		{in: "123 l sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15 42", want: meta{
			exists:   true,
			size:     123,
			largeRef: blob.MustParse("sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15"),
			largeOff: 42,
		}},
	}
	for _, tt := range cases {
		got, err := parseMetaRow([]byte(tt.in))
		if (err != nil) != tt.err {
			t.Errorf("For %q error = %v; want-err? = %v", tt.in, err, tt.err)
			continue
		}
		if tt.err {
			continue
		}
		if got != tt.want {
			t.Errorf("For %q, parseMetaRow = %+v; want %+v", tt.in, got, tt.want)
		}
	}
}

func TestPack(t *testing.T) {
	small, large := new(test.Fetcher), new(test.Fetcher)
	sto := &storage{
		small: small,
		large: large,
		meta:  sorted.NewMemoryKeyValue(),
	}

	const fileSize = 5 << 20
	fileContents := make([]byte, fileSize)
	for i := range fileContents {
		fileContents[i] = byte(rand.Int63())
	}
	const fileName = "foo.dat"
	ref, err := schema.WriteFileFromReader(sto, fileName, bytes.NewReader(fileContents))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote file %v", ref)
	t.Logf("items in small: %v", small.NumBlobs())
	t.Logf("items in large: %v", large.NumBlobs())
}
