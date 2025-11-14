/*
Copyright 2018 The Perkeep Authors

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

package overlay

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/test"
)

var ctxbg = context.Background()

func newOverlay(t *testing.T, withDel bool) blobserver.Storage {
	overlay, _ := newOverlayWithLower(t, withDel)
	return overlay
}

func newOverlayWithLower(t *testing.T, withDel bool) (sto, lower blobserver.Storage) {
	ld := test.NewLoader()
	lower, _ = ld.GetStorage("/good-lower/")
	ld.GetStorage("/good-upper/")
	conf := map[string]any{
		"lower": "/good-lower/",
		"upper": "/good-upper/",
	}
	if withDel {
		conf["deleted"] = map[string]any{
			"type": "memory",
		}
	}
	sto, err := newFromConfig(ld, conf)
	if err != nil {
		t.Fatalf("Invalid config: %v", err)
	}
	return sto, lower
}

func TestStorageTest(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		return newOverlay(t, true)
	})
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		return newOverlay(t, false)
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	sto, lower := newOverlayWithLower(t, true)

	var (
		// blobs that go into lower
		S0 = &test.Blob{Contents: "lower blob 0"}
		S1 = &test.Blob{Contents: "lower blob 1"}

		// blobs that go into sto
		A = &test.Blob{Contents: "some small blob"}
		B = &test.Blob{Contents: strings.Repeat("some middle blob", 100)}
		C = &test.Blob{Contents: strings.Repeat("A 8192 bytes length largish blob", 8192/32)}
	)

	// add S0 and S1 to lower
	for _, tb := range []*test.Blob{S0, S1} {
		sb, err := lower.ReceiveBlob(ctxbg, tb.BlobRef(), tb.Reader())
		if err != nil {
			t.Fatalf("ReceiveBlob of %s: %v", sb, err)
		}
		if sb != tb.SizedRef() {
			t.Fatalf("Received %v; want %v", sb, tb.SizedRef())
		}
	}

	lowerRefs := []blob.SizedRef{
		S0.SizedRef(),
		S1.SizedRef(),
	}

	type step func() error

	stepAdd := func(tb *test.Blob) step { // add the blob to sto
		return func() error {
			sb, err := sto.ReceiveBlob(ctxbg, tb.BlobRef(), tb.Reader())
			if err != nil {
				return fmt.Errorf("ReceiveBlob of %s: %v", sb, err)
			}
			if sb != tb.SizedRef() {
				return fmt.Errorf("Received %v; want %v", sb, tb.SizedRef())
			}
			return nil
		}
	}

	stepCheck := func(want ...*test.Blob) step { // check the blob
		wantRefs := make([]blob.SizedRef, len(want))
		for i, tb := range want {
			wantRefs[i] = tb.SizedRef()
		}
		return func() error {
			// ensure lower was not modified
			if err := storagetest.CheckEnumerate(lower, lowerRefs); err != nil {
				return err
			}
			return storagetest.CheckEnumerate(sto, wantRefs)
		}
	}

	stepDelete := func(tb *test.Blob) step { // delete the blob in sto
		return func() error {
			if err := sto.RemoveBlobs(ctx, []blob.Ref{tb.BlobRef()}); err != nil {
				return fmt.Errorf("RemoveBlob(%s): %v", tb.BlobRef(), err)
			}
			return nil
		}
	}

	var deleteTests = [][]step{
		{
			stepAdd(A),
			stepDelete(A),
			stepCheck(S0, S1),
			stepAdd(B),
			stepCheck(S0, S1, B),
			stepDelete(B),
			stepCheck(S0, S1),
			stepAdd(C),
			stepCheck(S0, S1, C),
			stepAdd(A),
			stepCheck(S0, S1, A, C),
			stepDelete(A),
			stepDelete(C),
			stepCheck(S0, S1),
		},
		{
			stepAdd(A),
			stepAdd(B),
			stepAdd(C),
			stepCheck(S0, S1, A, B, C),
			stepDelete(C),
			stepCheck(S0, S1, A, B),
			stepDelete(S0),
			stepCheck(S1, A, B),
			stepDelete(A),
			stepDelete(B),
			stepCheck(S1),
		},
		{
			stepAdd(S0),
			stepCheck(S0, S1),
		},
		{
			stepDelete(S0),
			stepDelete(S1),
			stepCheck(),
			stepAdd(A),
			stepAdd(B),
			stepCheck(A, B),
		},
	}
	for i, steps := range deleteTests {
		for j, s := range steps {
			if err := s(); err != nil {
				t.Errorf("error at test %d, step %d: %v", i+1, j+1, err)
			}
		}
	}
}
