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

package index

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/sqlite"
)

func enumstatRef(n int) blob.Ref {
	return blob.MustParse(fmt.Sprintf("sha1-%040x", n))
}

func TestEnumerateBlobsAfterLimit(t *testing.T) {
	kv := sorted.NewMemoryKeyValue()
	defer kv.Close()
	for k, v := range map[string]string{
		"have:" + enumstatRef(1).String(): "1|indexed",
		"have:" + enumstatRef(2).String(): "bad-size",
		"have:" + enumstatRef(3).String(): "3",
		"have:" + enumstatRef(4).String(): "4294967296",
		"have:zz-invalid":                 "5",
		"have;outside":                    "6",
	} {
		if err := kv.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	ix := &Index{s: kv}
	first := blob.SizedRef{Ref: enumstatRef(1), Size: 1}
	third := blob.SizedRef{Ref: enumstatRef(3), Size: 3}
	for _, tt := range []struct {
		name  string
		after string
		limit int
		want  []blob.SizedRef
	}{
		{"all", "", 100, []blob.SizedRef{first, third}},
		{"zero", "", 0, nil},
		{"negative", "", -1, nil},
		{"one", "", 1, []blob.SizedRef{first}},
		{"invalid-consumes-limit", "", 2, []blob.SizedRef{first}},
		{"three", "", 3, []blob.SizedRef{first, third}},
		{"exclusive-after", enumstatRef(1).String(), 1, nil},
		{"after-invalid", enumstatRef(2).String(), 1, []blob.SizedRef{third}},
		{"missing-after", enumstatRef(2).String() + "x", 1, []blob.SizedRef{third}},
		{"past-last", enumstatRef(3).String(), 100, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dst := make(chan blob.SizedRef, 10)
			if err := ix.EnumerateBlobs(context.Background(), dst, tt.after, tt.limit); err != nil {
				t.Fatal(err)
			}
			var got []blob.SizedRef
			for sb := range dst {
				got = append(got, sb)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %v; want %v", got, tt.want)
			}
		})
	}
}

// A receiver may need the same SQLite KV while enumeration is blocked sending.
// In particular, EnumerateAll invokes callbacks before draining its input.
func TestEnumerateBlobsSQLiteReentrant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kv, err := sqlite.NewStorage(filepath.Join(t.TempDir(), "index.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer kv.Close()
		const count = 2005 // Cross two page boundaries.
		batch := kv.BeginBatch()
		for i := 1; i <= count; i++ {
			batch.Set("have:"+enumstatRef(i).String(), fmt.Sprintf("%d|indexed", i))
		}
		if err := kv.CommitBatch(batch); err != nil {
			t.Fatal(err)
		}
		ix := &Index{s: kv}
		dst := make(chan blob.SizedRef)
		errc := make(chan error, 1)
		go func() {
			errc <- ix.EnumerateBlobs(context.Background(), dst, "", count)
		}()
		synctest.Wait() // Enumerator is blocked on its first send.
		for i := 1; i <= count; i++ {
			// Read before accepting the next blob, while the sender is blocked.
			want := blob.SizedRef{Ref: enumstatRef(i), Size: uint32(i)}
			calls := 0
			if err := ix.StatBlobs(context.Background(), []blob.Ref{want.Ref}, func(sb blob.SizedRef) error {
				calls++
				if sb != want {
					t.Errorf("StatBlobs = %v; want %v", sb, want)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("StatBlobs called back %d times; want 1", calls)
			}
			if got, ok := <-dst; !ok || got != want {
				t.Fatalf("blob %d = %v, %v; want %v", i, got, ok, want)
			}
		}
		if _, ok := <-dst; ok {
			t.Fatal("enumerator did not close its channel at the limit")
		}
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	})
}

func TestEnumerateBlobsSQLiteCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kv, err := sqlite.NewStorage(filepath.Join(t.TempDir(), "index.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer kv.Close()
		br := enumstatRef(1)
		if err := kv.Set("have:"+br.String(), "1"); err != nil {
			t.Fatal(err)
		}
		ix := &Index{s: kv}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dst := make(chan blob.SizedRef)
		errc := make(chan error, 1)
		go func() { errc <- ix.EnumerateBlobs(ctx, dst, "", 1) }()
		synctest.Wait()
		cancel()
		if err := <-errc; !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v; want context.Canceled", err)
		}
		if _, ok := <-dst; ok {
			t.Fatal("canceled enumerator sent a blob")
		}
		if _, err := kv.Get("have:" + br.String()); err != nil {
			t.Fatal(err)
		}
	})
}

type enumstatErrorKV struct {
	sorted.KeyValue
	closeErr error
	finds    int
}

func (kv *enumstatErrorKV) Find(start, end string) sorted.Iterator {
	kv.finds++
	return &enumstatErrorIter{Iterator: kv.KeyValue.Find(start, end), closeErr: kv.closeErr}
}

type enumstatErrorIter struct {
	sorted.Iterator
	closeErr error
}

func (it *enumstatErrorIter) Close() error {
	if err := it.Iterator.Close(); err != nil {
		return err
	}
	return it.closeErr
}

func TestEnumerateBlobsPageLimitAndError(t *testing.T) {
	kv := &enumstatErrorKV{KeyValue: sorted.NewMemoryKeyValue()}
	defer kv.Close()
	for i := 1; i <= 1001; i++ {
		if err := kv.Set("have:"+enumstatRef(i).String(), "1"); err != nil {
			t.Fatal(err)
		}
	}
	ix := &Index{s: kv}
	dst := make(chan blob.SizedRef, 1001)
	if err := ix.EnumerateBlobs(context.Background(), dst, "", 1000); err != nil {
		t.Fatal(err)
	}
	if len(dst) != 1000 || kv.finds != 1 {
		t.Fatalf("sent %d blobs using %d iterators; want 1000 blobs and 1 iterator", len(dst), kv.finds)
	}
	kv.finds = 0
	dst = make(chan blob.SizedRef, 1001)
	if err := ix.EnumerateBlobs(context.Background(), dst, enumstatRef(1).String(), 1000); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 1001; i++ {
		if got := <-dst; got.Ref != enumstatRef(i) {
			t.Fatalf("blob %d after exclusive start = %v", i, got)
		}
	}
	if _, ok := <-dst; ok || kv.finds != 2 {
		t.Fatalf("channel open = %v, iterators = %d; want closed and 2 iterators", ok, kv.finds)
	}
	kv.closeErr = errors.New("iterator failed")
	kv.finds = 0
	dst = make(chan blob.SizedRef, 1001)
	if err := ix.EnumerateBlobs(context.Background(), dst, "", 1); err != nil {
		t.Fatalf("limited enumeration got %v; want nil", err)
	}
	if got, ok := <-dst; !ok || got != (blob.SizedRef{Ref: enumstatRef(1), Size: 1}) {
		t.Fatalf("limited enumeration got %v, %v; want first buffered blob", got, ok)
	}
	if _, ok := <-dst; ok || kv.finds != 1 {
		t.Fatalf("channel open = %v, iterators = %d; want closed and 1 iterator", ok, kv.finds)
	}
	kv.finds = 0
	dst = make(chan blob.SizedRef, 1001)
	// Do not reach the limit before ScanPaged's terminal iterator error.
	if err := ix.EnumerateBlobs(context.Background(), dst, "", 1001); !errors.Is(err, kv.closeErr) {
		t.Fatalf("got %v; want %v", err, kv.closeErr)
	}
	for i := 1; i <= 1000; i++ {
		want := blob.SizedRef{Ref: enumstatRef(i), Size: 1}
		if got, ok := <-dst; !ok || got != want {
			t.Fatalf("buffered blob %d = %v, %v; want %v", i, got, ok, want)
		}
	}
	if _, ok := <-dst; ok || kv.finds != 1 {
		t.Fatalf("channel open = %v, iterators = %d; want closed and 1 iterator", ok, kv.finds)
	}
}
