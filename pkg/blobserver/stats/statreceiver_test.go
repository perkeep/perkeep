/*
Copyright 2016 The Perkeep AUTHORS

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

package stats

import (
	"context"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/test"
)

func TestStats(t *testing.T) {
	ctx := context.Background()
	st := &Receiver{}
	foo := test.Blob{"foo"}
	bar := test.Blob{"bar"}
	foobar := test.Blob{"foobar"}

	foo.MustUpload(t, st)
	bar.MustUpload(t, st)
	foobar.MustUpload(t, st)

	if st.NumBlobs() != 3 {
		t.Fatal("expected 3 stats")
	}

	sizes := st.Sizes()
	if sizes[0] != 3 || sizes[1] != 3 || sizes[2] != 6 {
		t.Fatal("stats reported the incorrect sizes:", sizes)
	}

	if st.SumBlobSize() != 12 {
		t.Fatal("stats reported the incorrect sum sizes:", st.SumBlobSize())
	}

	t.Logf("foo = %v", foo.BlobRef())
	t.Logf("bar = %v", bar.BlobRef())
	t.Logf("foobar = %v", foobar.BlobRef())

	gotStat, err := blobserver.StatBlobs(context.Background(), st, []blob.Ref{
		foo.BlobRef(),
		bar.BlobRef(),
		foobar.BlobRef(),
	})
	if err != nil {
		t.Fatalf("StatBlobs: %v", err)
	}
	if _, ok := gotStat[foo.BlobRef()]; !ok {
		t.Errorf("missing ref foo")
	}
	if _, ok := gotStat[bar.BlobRef()]; !ok {
		t.Errorf("missing ref bar")
	}
	if _, ok := gotStat[foobar.BlobRef()]; !ok {
		t.Errorf("missing ref foobar")
	}
	if len(gotStat) != 3 {
		t.Errorf("got %d stat results; want 3", len(gotStat))
	}

	dest := make(chan blob.SizedRef, 2)
	err = st.EnumerateBlobs(context.Background(), dest, "sha224-1", 2)
	if err != nil {
		t.Fatal(err)
	}

	expectFoobar := <-dest
	if expectFoobar.Ref != foobar.BlobRef() {
		t.Fatal("expected foobar")
	}

	val, expectFalse := <-dest
	if expectFalse != false {
		t.Fatal("expected dest to be closed, saw", val)
	}

	err = st.RemoveBlobs(ctx, []blob.Ref{
		foo.BlobRef(),
		bar.BlobRef(),
		foobar.BlobRef(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if st.NumBlobs() != 0 {
		t.Fatal("all blobs should be gone, instead we have", st.NumBlobs())
	}
}
