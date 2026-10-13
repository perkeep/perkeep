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

package blobpacked

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/sqlite"
	"perkeep.org/pkg/test"
)

func newSQLitePackedStorage(t *testing.T) *storage {
	t.Helper()
	meta, err := sqlite.NewStorage(filepath.Join(t.TempDir(), "meta.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := meta.Close(); err != nil {
			t.Error(err)
		}
	})
	s := &storage{
		small: new(test.Fetcher),
		large: new(test.Fetcher),
		meta:  meta,
		log:   test.NewLogger(t, "blobpacked: "),
	}
	s.init()
	return s
}

// Exercise the same merged enumeration and Fetch combination as reindex,
// using a real packed file and SQLite's single-slot metadata gate.
func TestEnumeratePackedSQLiteFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newSQLitePackedStorage(t)
		if _, err := schema.WriteFileFromReader(context.Background(), s, "packed.dat", bytes.NewReader(randBytes(5<<20))); err != nil {
			t.Fatal(err)
		}
		if !s.anyMeta() || !s.anyZipPacks() {
			t.Fatal("file was not packed")
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		n := 0
		go func() {
			done <- blobserver.EnumerateAll(ctx, s, func(sb blob.SizedRef) error {
				rc, size, err := s.Fetch(ctx, sb.Ref)
				if err != nil {
					return err
				}
				defer rc.Close()
				data, err := io.ReadAll(rc)
				if err != nil {
					return err
				}
				if size != sb.Size || len(data) != int(sb.Size) || blob.RefFromBytes(data) != sb.Ref {
					return fmt.Errorf("packed blob %v did not round-trip", sb.Ref)
				}
				n++
				return nil
			})
		}()
		synctest.Wait()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		default:
			// Let a broken enumerator release its gate before failing, so this
			// regression reports the failure without stranding any goroutines.
			cancel()
			synctest.Wait()
			t.Fatal("enumeration deadlocked with Fetch on SQLite metadata")
		}
		// More than the merged enumerator and EnumerateAll buffers combined.
		if n <= 24 {
			t.Fatalf("only %d packed blobs; need more than the channel buffers", n)
		}
	})
}

func TestEnumeratePackedSQLitePages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newSQLitePackedStorage(t)
		const count = 2005
		var contents bytes.Buffer
		refs := make([]blob.Ref, 0, count)
		offsets := make(map[blob.Ref]int)
		for i := range count {
			data := fmt.Sprintf("packed blob %04d\n", i)
			br := blob.RefFromString(data)
			refs = append(refs, br)
			offsets[br] = contents.Len()
			contents.WriteString(data)
		}
		// Fetch only needs the uncompressed data and b: rows, not a ZIP TOC.
		pack := &test.Blob{Contents: contents.String()}
		s.large.(*test.Fetcher).AddBlob(pack)
		batch := s.meta.BeginBatch()
		for _, br := range refs {
			batch.Set(blobMetaPrefix+br.String(), fmt.Sprintf("%d %v %d", len("packed blob 0000\n"), pack.BlobRef(), offsets[br]))
		}
		if err := s.meta.CommitBatch(batch); err != nil {
			t.Fatal(err)
		}
		slices.SortFunc(refs, func(a, b blob.Ref) int {
			if a.Less(b) {
				return -1
			}
			if b.Less(a) {
				return 1
			}
			return 0
		})
		for _, tc := range []struct {
			name  string
			after string
			limit int
			want  []blob.Ref
		}{
			{"all", "", count + 1, refs},
			{"limit", "", 7, refs[:7]},
			{"after", refs[999].String(), count, refs[1000:]},
			{"absent-after", refs[999].String() + "0", 3, refs[1000:1003]},
			{"last", refs[count-1].String(), count, nil},
			{"zero", "", 0, nil},
			{"negative", "", -1, nil},
		} {
			func() {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				dest := make(chan blob.SizedRef)
				done := make(chan error, 1)
				fetched := make(chan error, 1)
				var got []blob.Ref
				go func() {
					done <- (enumerator{s}).EnumerateBlobs(ctx, dest, tc.after, tc.limit)
				}()
				go func() {
					for sb := range dest {
						rc, size, err := s.Fetch(ctx, sb.Ref)
						if err != nil {
							fetched <- err
							return
						}
						data, err := io.ReadAll(rc)
						rc.Close()
						if err != nil || size != sb.Size || blob.RefFromBytes(data) != sb.Ref {
							fetched <- fmt.Errorf("Fetch(%v) mismatch: %v", sb.Ref, err)
							return
						}
						got = append(got, sb.Ref)
					}
					fetched <- nil
				}()
				synctest.Wait()
				select {
				case err := <-fetched:
					if err != nil {
						t.Fatal(err)
					}
				default:
					cancel()
					synctest.Wait()
					t.Fatalf("%s: enumerator held SQLite gate while sending", tc.name)
				}
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(got, tc.want) {
					t.Fatalf("%s: got %d refs, want %d (order or range mismatch)", tc.name, len(got), len(tc.want))
				}
			}()
		}

		// Cancellation of a blocked send must leave the metadata gate usable.
		ctx, cancel := context.WithCancel(context.Background())
		dest := make(chan blob.SizedRef)
		done := make(chan error, 1)
		go func() {
			done <- (enumerator{s}).EnumerateBlobs(ctx, dest, "", count)
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled enumeration: %v", err)
		}
		if _, ok := <-dest; ok {
			t.Fatal("destination not closed")
		}
		if _, err := s.meta.Get(blobMetaPrefix + refs[0].String()); err != nil {
			t.Fatal(err)
		}
	})
}

type packedEnumerationErrorKV struct {
	sorted.KeyValue
	err error
}

func (kv *packedEnumerationErrorKV) Find(start, end string) sorted.Iterator {
	return &packedEnumerationErrorIterator{Iterator: kv.KeyValue.Find(start, end), err: kv.err}
}

type packedEnumerationErrorIterator struct {
	sorted.Iterator
	err error
}

func (it *packedEnumerationErrorIterator) Close() error {
	if err := it.Iterator.Close(); err != nil {
		return err
	}
	return it.err
}

func TestEnumeratePackedErrorAfterPrefix(t *testing.T) {
	wantErr := errors.New("metadata iterator failed")
	kv := &packedEnumerationErrorKV{KeyValue: sorted.NewMemoryKeyValue(), err: wantErr}
	defer kv.Close()
	var want []blob.SizedRef
	for i := 1; i <= 2; i++ {
		br := blob.MustParse(fmt.Sprintf("sha1-%040x", i))
		if err := kv.Set(blobMetaPrefix+br.String(), fmt.Sprintf("1 %v 0", br)); err != nil {
			t.Fatal(err)
		}
		want = append(want, blob.SizedRef{Ref: br, Size: 1})
	}
	for _, tc := range []struct {
		name  string
		limit int
		want  []blob.SizedRef
		err   error
	}{
		{"terminal-error", 3, want, wantErr},
		// Stopping successfully does not consume the pending error result.
		{"early-stop", 1, want[:1], nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := make(chan blob.SizedRef, len(want))
			if err := (enumerator{&storage{meta: kv}}).EnumerateBlobs(context.Background(), dest, "", tc.limit); err != tc.err {
				t.Fatalf("error = %v, want %v", err, tc.err)
			}
			var got []blob.SizedRef
			for sb := range dest {
				got = append(got, sb)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
