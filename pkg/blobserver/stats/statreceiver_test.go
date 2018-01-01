/*
Copyright 2016 The Camlistore AUTHORS

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
	"perkeep.org/pkg/test"
)

func TestStats(t *testing.T) {
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

	dest := make(chan blob.SizedRef, 5) // buffer past what we expect so we see if there is something extra
	err := st.StatBlobs(dest, []blob.Ref{
		foo.BlobRef(),
		bar.BlobRef(),
		foobar.BlobRef(),
	})

	if err != nil {
		t.Fatal(err)
	}

	var foundFoo, foundBar, foundFoobar bool

	func() {
		for {
			select {
			case sb := <-dest:
				switch {
				case sb.Ref == foo.BlobRef():
					foundFoo = true
				case sb.Ref == bar.BlobRef():
					foundBar = true
				case sb.Ref == foobar.BlobRef():
					foundFoobar = true
				default:
					t.Fatal("found unexpected ref:", sb)
				}
			default:
				return
			}
		}
	}()

	if !foundFoo || !foundBar || !foundFoobar {
		t.Fatalf("missing a ref: foo: %t bar: %t foobar: %t", foundFoo, foundBar, foundFoobar)
	}

	dest = make(chan blob.SizedRef, 2)
	err = st.EnumerateBlobs(context.Background(), dest, "sha1-7", 2)
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

	err = st.RemoveBlobs([]blob.Ref{
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
