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

package diskpacked

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"go4.org/jsonconfig"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/sqlite"
	"perkeep.org/pkg/test"
)

// Keep track of iterator lifetime so a regression fails before attempting a
// SQLite Get that would deadlock behind the iterator's single-slot Gate.
// Access to these fields is synchronized by synctest.Wait or enumeration ending.
type enumerationTestKV struct {
	sorted.KeyValue
	open     map[*enumerationTestIterator]bool
	closeErr error
	finds    int
}

type enumerationTestIterator struct {
	sorted.Iterator
	kv *enumerationTestKV
}

func (kv *enumerationTestKV) Find(start, end string) sorted.Iterator {
	it := &enumerationTestIterator{Iterator: kv.KeyValue.Find(start, end), kv: kv}
	kv.open[it] = true
	kv.finds++
	return it
}

func (it *enumerationTestIterator) Close() error {
	err := it.Iterator.Close()
	delete(it.kv.open, it)
	if err != nil {
		return err
	}
	return it.kv.closeErr
}

func (kv *enumerationTestKV) Close() error {
	for it := range kv.open {
		it.Close()
	}
	return kv.KeyValue.Close()
}

func newEnumerationSQLite(t *testing.T, path string) *enumerationTestKV {
	t.Helper()
	kv, err := sqlite.NewStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	return &enumerationTestKV{KeyValue: kv, open: make(map[*enumerationTestIterator]bool)}
}

func seedEnumeration(t *testing.T, kv sorted.KeyValue, n int) []blob.SizedRef {
	t.Helper()
	want := make([]blob.SizedRef, n)
	batch := kv.BeginBatch()
	for i := range n {
		br := blob.MustParse(fmt.Sprintf("sha1-%040x", i+1))
		want[i] = blob.SizedRef{Ref: br, Size: uint32(i + 1)}
		batch.Set(br.String(), fmt.Sprintf("0 0 %d", i+1))
	}
	if err := kv.CommitBatch(batch); err != nil {
		t.Fatal(err)
	}
	return want
}

func enumerationStorage(kv sorted.KeyValue) blobserver.Storage {
	return &storage{index: kv}
}

func TestEnumerateBlobsSQLiteBlockedSend(t *testing.T) {
	for _, cancelWhileBlocked := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancelWhileBlocked), func(t *testing.T) {
			// Filesystem setup is outside the bubble; the SQLite KV (and its
			// Gate) must be created inside it for deterministic blocking checks.
			path := filepath.Join(t.TempDir(), "index.sqlite")
			synctest.Test(t, func(t *testing.T) {
				kv := newEnumerationSQLite(t, path)
				defer kv.Close()
				want := seedEnumeration(t, kv, 2005)
				sto := enumerationStorage(kv)
				ctx, cancel := context.WithCancel(context.Background())
				defer func() {
					cancel()
					synctest.Wait()
				}()
				dest := make(chan blob.SizedRef)
				done := make(chan error, 1)
				go func() {
					done <- sto.EnumerateBlobs(ctx, dest, "", len(want))
				}()
				synctest.Wait()
				select {
				case err := <-done:
					t.Fatalf("enumeration returned before sending: %v", err)
				default:
				}
				if len(kv.open) != 0 {
					t.Fatal("enumeration holds a SQLite iterator while blocked sending")
				}
				if kv.finds != 1 {
					t.Fatalf("opened %d iterators before first send; want one bounded page", kv.finds)
				}
				if _, err := kv.Get(want[0].Ref.String()); err != nil {
					t.Fatalf("SQLite Get while enumeration is blocked: %v", err)
				}
				if cancelWhileBlocked {
					cancel()
					synctest.Wait()
					if err := <-done; !errors.Is(err, context.Canceled) {
						t.Fatalf("enumeration error = %v; want context.Canceled", err)
					}
					if _, ok := <-dest; ok {
						t.Fatal("canceled enumeration did not close its channel")
					}
				} else {
					var got []blob.SizedRef
					for sb := range dest {
						// This is the consumer pattern that used to deadlock:
						// read index metadata before receiving the next blob.
						if _, err := kv.Get(sb.Ref.String()); err != nil {
							t.Fatalf("SQLite Get during enumeration: %v", err)
						}
						got = append(got, sb)
					}
					if err := <-done; err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(got, want) {
						t.Fatalf("enumerated %d rows; want %d ordered rows", len(got), len(want))
					}
					if kv.finds < 3 {
						t.Fatalf("enumeration used %d pages; want at least three", kv.finds)
					}
				}
				synctest.Wait()
				if len(kv.open) != 0 {
					t.Fatal("enumeration leaked an iterator")
				}
				if _, err := kv.Get(want[0].Ref.String()); err != nil {
					t.Fatalf("SQLite Get after enumeration: %v", err)
				}
			})
		})
	}
}

func TestEnumerateBlobsSQLiteAfterLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.sqlite")
	synctest.Test(t, func(t *testing.T) {
		kv := newEnumerationSQLite(t, path)
		defer kv.Close()
		want := seedEnumeration(t, kv, 2005)
		sto := enumerationStorage(kv)
		tests := []struct {
			name  string
			after string
			limit int
			want  []blob.SizedRef
		}{
			{"all", "", len(want) + 10, want},
			{"zero", "", 0, nil},
			{"negative", "", -1, nil},
			{"one", "", 1, want[:1]},
			{"after", want[499].Ref.String(), 1101, want[500:1601]},
			{"page-boundary", want[999].Ref.String(), 1001, want[1000:2001]},
			{"nonexistent-after", want[999].Ref.String() + "x", 1001, want[1000:2001]},
			{"last", want[len(want)-1].Ref.String(), 10, nil},
		}

		for _, tt := range tests {
			dest := make(chan blob.SizedRef, len(want)+1)
			if err := sto.EnumerateBlobs(context.Background(), dest, tt.after, tt.limit); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			var got []blob.SizedRef
			for sb := range dest {
				got = append(got, sb)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("%s: got %d rows; want %d ordered rows", tt.name, len(got), len(tt.want))
			}
			if len(kv.open) != 0 {
				t.Fatalf("%s: enumeration leaked an iterator", tt.name)
			}
		}
	})
}

func TestEnumerateBlobsSQLiteCloseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.sqlite")
	synctest.Test(t, func(t *testing.T) {
		kv := newEnumerationSQLite(t, path)
		defer kv.Close()
		want := seedEnumeration(t, kv, 2)
		kv.closeErr = errors.New("iterator close failure")
		dest := make(chan blob.SizedRef, len(want)+1)
		err := enumerationStorage(kv).EnumerateBlobs(context.Background(), dest, "", len(want)+1)
		if !errors.Is(err, kv.closeErr) {
			t.Fatalf("enumeration error = %v; want %v", err, kv.closeErr)
		}
		var got []blob.SizedRef
		for sb := range dest {
			got = append(got, sb)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v before iterator Close error; want %v", got, want)
		}
		if len(kv.open) != 0 {
			t.Fatal("enumeration leaked an iterator on Close error")
		}
		if _, err := kv.Get(want[0].Ref.String()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestEnumerateBlobsSQLiteInvalidValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.sqlite")
	synctest.Test(t, func(t *testing.T) {
		kv := newEnumerationSQLite(t, path)
		defer kv.Close()
		want := seedEnumeration(t, kv, 1)
		if err := kv.Set(want[0].Ref.String(), "invalid value"); err != nil {
			t.Fatal(err)
		}
		dest := make(chan blob.SizedRef, 2)
		err := enumerationStorage(kv).EnumerateBlobs(context.Background(), dest, "", 1)
		if err == nil || !strings.Contains(err.Error(), "couldn't parse index value") {
			t.Fatalf("enumeration error = %v; want malformed index value error", err)
		}
		if _, ok := <-dest; ok {
			t.Fatal("enumeration sent a row for an invalid value")
		}
		if len(kv.open) != 0 {
			t.Fatal("enumeration leaked an iterator on parse error")
		}
		if _, err := kv.Get(want[0].Ref.String()); err != nil {
			t.Fatal(err)
		}
	})
}

// Exercise the real consumer path: fetch one enumerated blob while the
// enumerator is blocked trying to send the next result over an unbuffered channel.
func TestEnumerateBlobsSQLiteFetch(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		sto, err := newStorage(dir, 1<<20, jsonconfig.Obj{
			"type": "sqlite", "file": filepath.Join(dir, "index.sqlite"),
		})
		if err != nil {
			t.Fatal(err)
		}
		kv := &enumerationTestKV{KeyValue: sto.index, open: make(map[*enumerationTestIterator]bool)}
		sto.index = kv
		defer sto.Close()
		blobs := []*test.Blob{
			{Contents: "first enumeration fetch regression blob"},
			{Contents: "second enumeration fetch regression blob"},
		}
		for _, tb := range blobs {
			tb.MustUpload(t, sto)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer func() {
			cancel()
			synctest.Wait()
		}()
		dest := make(chan blob.SizedRef)
		done := make(chan error, 1)
		go func() { done <- sto.EnumerateBlobs(ctx, dest, "", 10) }()
		sb := <-dest
		synctest.Wait()
		if len(kv.open) != 0 {
			t.Fatal("enumeration holds a SQLite iterator while its consumer needs Fetch")
		}
		rc, size, err := sto.Fetch(context.Background(), sb.Ref)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("reading fetched blob: %v; Close: %v", err, closeErr)
		}
		if size != sb.Size || uint32(len(data)) != sb.Size || blob.RefFromBytes(data) != sb.Ref {
			t.Fatalf("Fetch returned wrong content or size for %v", sb)
		}
		cancel()
		synctest.Wait()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("enumeration error = %v; want context.Canceled", err)
		}
		if _, ok := <-dest; ok {
			t.Fatal("canceled enumeration did not close its channel")
		}
		if len(kv.open) != 0 {
			t.Fatal("canceled enumeration leaked its iterator")
		}
		rc, _, err = sto.Fetch(context.Background(), sb.Ref)
		if err != nil {
			t.Fatalf("Fetch after cancellation: %v", err)
		}
		rc.Close()
	})
}
