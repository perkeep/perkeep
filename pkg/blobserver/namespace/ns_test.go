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

package namespace

import (
	"reflect"
	"sort"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/test"
	"golang.org/x/net/context"
)

func newNamespace(t *testing.T, ld *test.Loader) *nsto {
	sto, err := newFromConfig(ld, map[string]interface{}{
		"storage": "/good-storage/",
		"inventory": map[string]interface{}{
			"type": "memory",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sto.(*nsto)
}

func TestStorageTest(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
		ld := test.NewLoader()
		return newNamespace(t, ld), func() {}
	})
}

func TestIsolation(t *testing.T) {
	ld := test.NewLoader()
	master, _ := ld.GetStorage("/good-storage/")

	ns1 := newNamespace(t, ld)
	ns2 := newNamespace(t, ld)
	stoMap := map[string]blobserver.Storage{
		"ns1":    ns1,
		"ns2":    ns2,
		"master": master,
	}

	want := func(src string, want ...blob.Ref) {
		if _, ok := stoMap[src]; !ok {
			t.Fatalf("undefined storage %q", src)
		}
		sort.Sort(blob.ByRef(want))
		var got []blob.Ref
		if err := blobserver.EnumerateAll(context.TODO(), stoMap[src], func(sb blob.SizedRef) error {
			got = append(got, sb.Ref)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("server %q = %q; want %q", src, got, want)
		}
	}

	b1 := &test.Blob{Contents: "Blob 1"}
	b1r := b1.BlobRef()
	b2 := &test.Blob{Contents: "Blob 2"}
	b2r := b2.BlobRef()
	b3 := &test.Blob{Contents: "Shared Blob"}
	b3r := b3.BlobRef()

	b1.MustUpload(t, ns1)
	want("ns1", b1r)
	want("ns2")
	want("master", b1r)

	b2.MustUpload(t, ns2)
	want("ns1", b1r)
	want("ns2", b2r)
	want("master", b1r, b2r)

	b3.MustUpload(t, ns2)
	want("ns1", b1r)
	want("ns2", b2r, b3r)
	want("master", b1r, b2r, b3r)

	b3.MustUpload(t, ns1)
	want("ns1", b1r, b3r)
	want("ns2", b2r, b3r)
	want("master", b1r, b2r, b3r)

	if _, _, err := ns2.Fetch(b1r); err == nil {
		t.Errorf("b1 shouldn't be accessible via ns2")
	}
}
