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

package blobserver

import (
	"reflect"
	"sort"
	"testing"

	"camlistore.org/pkg/blob"
	"golang.org/x/net/context"
)

var mergedTests = []struct {
	name  string
	srcs  []BlobEnumerator
	after string
	limit int
	want  []string
}{
	{
		name: "a first",
		srcs: []BlobEnumerator{
			enumBlobs("foo-a", "foo-d"),
			enumBlobs("foo-b", "foo-c", "foo-e"),
		},
		want: []string{"foo-a", "foo-b", "foo-c", "foo-d", "foo-e"},
	},
	{
		name: "b first",
		srcs: []BlobEnumerator{
			enumBlobs("foo-b", "foo-c", "foo-e"),
			enumBlobs("foo-a", "foo-d"),
		},
		want: []string{"foo-a", "foo-b", "foo-c", "foo-d", "foo-e"},
	},
	{
		name: "after",
		srcs: []BlobEnumerator{
			enumBlobs("foo-b", "foo-c", "foo-e"),
			enumBlobs("foo-a", "foo-d"),
		},
		after: "foo-a",
		want:  []string{"foo-b", "foo-c", "foo-d", "foo-e"},
	},
	{
		name: "after-sha1",
		srcs: []BlobEnumerator{
			enumBlobs("sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15"),
			enumBlobs("sha1-a1d2d2f924e986ac86fdf7b36c94bcdf32beec15"),
		},
		after: "sha1-b1d2d2f924e986ac86fdf7b36c94bcdf32beec15",
		want:  []string{"sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15"},
	},
	{
		name: "limit",
		srcs: []BlobEnumerator{
			enumBlobs("foo-b", "foo-c", "foo-e"),
			enumBlobs("foo-a", "foo-d"),
		},
		limit: 3,
		want:  []string{"foo-a", "foo-b", "foo-c"},
	},
	{
		name: "no sources",
		srcs: []BlobEnumerator{},
	},
	{
		name: "three sources",
		srcs: []BlobEnumerator{
			enumBlobs("foo-a", "foo-d"),
			enumBlobs("foo-b", "foo-e", "foo-e1"),
			enumBlobs("foo-c", "foo-e", "foo-e2"),
		},
		want: []string{"foo-a", "foo-b", "foo-c", "foo-d", "foo-e", "foo-e1", "foo-e2"},
	},
}

func TestMergedEnumerate(t *testing.T) {
	for _, tt := range mergedTests {
		ctx := context.TODO()
		var got []string
		ch := make(chan blob.SizedRef)
		errc := make(chan error)
		limit := tt.limit
		if limit == 0 {
			limit = 1e9
		}
		go func() {
			errc <- MergedEnumerate(ctx, ch, tt.srcs, tt.after, limit)
		}()
		for sb := range ch {
			got = append(got, sb.Ref.String())
		}
		if err := <-errc; err != nil {
			t.Errorf("%s. MergedEnumerate = %v", tt.name, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s. different:\n got = %v\nwant = %v", tt.name, got, tt.want)
			continue
		}
	}
}

func enumBlobs(v ...string) BlobEnumerator {
	sort.Strings(v)
	return testEnum{v}
}

type testEnum struct {
	blobs []string
}

func (te testEnum) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	done := 0
	for _, bs := range te.blobs {
		if bs <= after {
			continue
		}
		br := blob.MustParse(bs)
		dest <- blob.SizedRef{br, 1}
		done++
		if done == limit {
			break
		}
	}
	return nil
}
