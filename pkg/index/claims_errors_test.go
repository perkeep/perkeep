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
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/camtypes"
)

// sorted iterators report storage failures through Close, including failures
// that stop iteration before any rows or after only a partial result.
type claimsErrorKV struct {
	sorted.KeyValue
	failPrefix string
	afterRows  int
	err        error
	closed     int
}

func (s *claimsErrorKV) Find(start, end string) sorted.Iterator {
	it := s.KeyValue.Find(start, end)
	if s.err == nil || !strings.HasPrefix(start, s.failPrefix) {
		return it
	}
	return &claimsErrorIterator{Iterator: it, store: s, remaining: s.afterRows}
}

type claimsErrorIterator struct {
	sorted.Iterator
	store     *claimsErrorKV
	remaining int
}

func (it *claimsErrorIterator) Next() bool {
	if it.remaining == 0 {
		return false
	}
	it.remaining--
	return it.Iterator.Next()
}

func (it *claimsErrorIterator) Close() error {
	it.store.closed++
	if err := it.Iterator.Close(); err != nil {
		return err
	}
	return it.store.err
}

func newClaimsErrorIndex(t *testing.T) (*Index, *claimsErrorKV, blob.Ref, string) {
	t.Helper()
	store := &claimsErrorKV{KeyValue: sorted.NewMemoryKeyValue()}
	ix, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ix.Close(); err != nil {
			t.Error(err)
		}
	})
	pn := blob.RefFromString("claims error permanode")
	const keyID = "1234567890ABCDEF"
	for n := range 2 {
		signer := blob.RefFromString(fmt.Sprintf("claims error signer %d", n))
		claim := blob.RefFromString(fmt.Sprintf("claims error claim %d", n))
		if err := store.Set(keySignerKeyID.name+":"+signer.String(), keyID); err != nil {
			t.Fatal(err)
		}
		key := keyPermanodeClaim.Key(pn, keyID, "2020-01-02T03:04:05Z", claim)
		value := keyPermanodeClaim.Val("set-attribute", "camliContent", "original", signer)
		if err := store.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	return ix, store, pn, keyID
}

func TestAppendClaimsIteratorErrors(t *testing.T) {
	for _, signerFiltered := range []bool{false, true} {
		for _, attrFilter := range []string{"", "camliContent"} {
			for _, afterRows := range []int{0, 1, 2} {
				t.Run(fmt.Sprintf("signer_filtered=%v/attr=%s/after_rows=%d", signerFiltered, attrFilter, afterRows), func(t *testing.T) {
					ix, store, pn, keyID := newClaimsErrorIndex(t)
					signerFilter := ""
					if signerFiltered {
						signerFilter = keyID
					}
					got, err := ix.AppendClaims(context.Background(), nil, pn, signerFilter, attrFilter)
					if err != nil || len(got) != 2 {
						t.Fatalf("healthy AppendClaims = (%v, %v); want two claims", got, err)
					}
					sentinel := errors.New("claims iterator storage failure")
					store.failPrefix = keyPermanodeClaim.name
					store.afterRows = afterRows
					store.err = sentinel
					dst := []camtypes.Claim{{BlobRef: blob.RefFromString("existing destination")}}
					got, err = ix.AppendClaims(context.Background(), dst, pn, signerFilter, attrFilter)
					if !errors.Is(err, sentinel) {
						t.Fatalf("AppendClaims = (%v, %v); want error %v", got, err, sentinel)
					}
					if len(got) != len(dst)+afterRows || got[0] != dst[0] {
						t.Errorf("AppendClaims lost destination or partial rows: %v", got)
					}
					if store.closed != 1 {
						t.Errorf("failed iterator closed %d times; want once", store.closed)
					}
				})
			}
		}
	}
}

func TestSignerRefsIteratorErrors(t *testing.T) {
	for _, afterRows := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("after_rows=%d", afterRows), func(t *testing.T) {
			ix, store, pn, keyID := newClaimsErrorIndex(t)
			refs, err := ix.signerRefs(context.Background(), keyID)
			if err != nil || len(refs) != 2 {
				t.Fatalf("healthy signerRefs = (%v, %v); want two refs", refs, err)
			}
			sentinel := errors.New("signer refs iterator storage failure")
			store.failPrefix = keySignerKeyID.name
			store.afterRows = afterRows
			store.err = sentinel
			refs, err = ix.signerRefs(context.Background(), keyID)
			if !errors.Is(err, sentinel) || len(refs) != afterRows {
				t.Fatalf("signerRefs = (%v, %v); want %d refs and error %v", refs, err, afterRows, sentinel)
			}
			dst := []camtypes.Claim{{BlobRef: blob.RefFromString("existing destination")}}
			got, err := ix.AppendClaims(context.Background(), dst, pn, keyID, "")
			if !errors.Is(err, sentinel) {
				t.Fatalf("AppendClaims = (%v, %v); want signer refs error %v", got, err, sentinel)
			}
			if len(got) != len(dst) || got[0] != dst[0] {
				t.Errorf("AppendClaims changed destination on signer lookup failure: %v", got)
			}
			if store.closed != 2 {
				t.Errorf("failed iterators closed %d times; want twice", store.closed)
			}
		})
	}
}
