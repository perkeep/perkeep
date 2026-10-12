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
	"sync"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/camtypes"
)

type claimLookupKV struct {
	sorted.KeyValue
	mu      sync.Mutex
	failKey string
	err     error
	cancel  context.CancelFunc
	gets    []string
	finds   int
}

func (s *claimLookupKV) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets = append(s.gets, key)
	if s.err != nil && key == s.failKey {
		return "", s.err
	}
	value, err := s.KeyValue.Get(key)
	if s.cancel != nil && strings.HasPrefix(key, "claim|") {
		s.cancel()
	}
	return value, err
}

func (s *claimLookupKV) Find(start, end string) sorted.Iterator {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finds++
	return s.KeyValue.Find(start, end)
}

func newClaimLookupIndex(t *testing.T, corpus bool, ordinal int) (*Index, *claimLookupKV, camtypes.Claim, string) {
	t.Helper()
	store := &claimLookupKV{KeyValue: sorted.NewMemoryKeyValue()}
	ix, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ix.Close(); err != nil {
			t.Error(err)
		}
	})
	cl := camtypes.Claim{
		BlobRef:   blob.RefFromString("point lookup envelope"),
		Signer:    blob.RefFromString("point lookup signer"),
		Permanode: blob.RefFromString("point lookup permanode"),
		Date:      time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC),
		Type:      "set-attribute", Attr: "attribute with | & spaces", Value: "value with | & spaces",
		MultiIndex: max(ordinal, 0),
	}
	const keyID = "1234567890ABCDEF"
	base := keyPermanodeClaim.Key(cl.Permanode, keyID, schema.RFC3339FromTime(cl.Date), cl.BlobRef)
	key := base
	if ordinal >= 0 {
		key = fmt.Sprintf("%s|%020d", base, ordinal)
	}
	rows := map[string]string{
		"signerkeyid:" + cl.Signer.String(): keyID,
		"meta:" + cl.Signer.String():        "100|application/octet-stream",
		"meta:" + cl.Permanode.String():     "100|application/json; camliType=permanode",
		"meta:" + cl.BlobRef.String():       "100|application/json; camliType=claim",
		key:                                 keyPermanodeClaim.Val(cl.Type, cl.Attr, cl.Value, cl.Signer),
	}
	for key, value := range rows {
		if err := store.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if corpus {
		if _, err := ix.KeepInMemory(); err != nil {
			t.Fatal(err)
		}
	}
	store.gets, store.finds = nil, 0
	return ix, store, cl, key
}

func lookupClaim(t *testing.T, ix *Index, ctx context.Context, cl camtypes.Claim) (bool, error) {
	t.Helper()
	ix.RLock()
	defer ix.RUnlock()
	return ix.HasClaim(ctx, cl)
}

func TestHasClaim(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		for _, ordinal := range []int{-1, 0, 1, 17} {
			t.Run(fmt.Sprintf("corpus=%v/ordinal=%d", corpus, ordinal), func(t *testing.T) {
				ix, store, cl, key := newClaimLookupIndex(t, corpus, ordinal)
				if got, err := lookupClaim(t, ix, context.Background(), cl); err != nil || !got {
					t.Fatalf("HasClaim = %v, %v; want true", got, err)
				}
				if ordinal == -1 {
					cl.MultiIndex = -1
					if got, err := lookupClaim(t, ix, context.Background(), cl); err != nil || !got {
						t.Fatalf("explicit ordinary claim = %v, %v; want true", got, err)
					}
				}
				if store.finds != 0 {
					t.Fatalf("point lookup scanned %d ranges", store.finds)
				}
				// Mutate only raw storage: a cached corpus must not mask a missing
				// or incorrect authoritative row.
				if err := store.Set(key, keyPermanodeClaim.Val(cl.Type, cl.Attr, "wrong value", cl.Signer)); err != nil {
					t.Fatal(err)
				}
				if got, err := lookupClaim(t, ix, context.Background(), cl); err != nil || got {
					t.Fatalf("wrong raw row = %v, %v; want false", got, err)
				}
				if err := store.Delete(key); err != nil {
					t.Fatal(err)
				}
				if got, err := lookupClaim(t, ix, context.Background(), cl); err != nil || got {
					t.Fatalf("missing raw row = %v, %v; want false", got, err)
				}
			})
		}
	}
}

func TestHasClaimWrongOperation(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		for _, tt := range []struct {
			name   string
			change func(*camtypes.Claim)
		}{
			{"envelope", func(c *camtypes.Claim) { c.BlobRef = blob.RefFromString("other envelope") }},
			{"permanode", func(c *camtypes.Claim) { c.Permanode = blob.RefFromString("other permanode") }},
			{"signer", func(c *camtypes.Claim) { c.Signer = blob.RefFromString("other signer") }},
			{"date", func(c *camtypes.Claim) { c.Date = c.Date.Add(time.Nanosecond) }},
			{"type", func(c *camtypes.Claim) { c.Type = "add-attribute" }},
			{"attribute", func(c *camtypes.Claim) { c.Attr = "other attribute" }},
			{"value", func(c *camtypes.Claim) { c.Value = "other value" }},
			{"ordinal", func(c *camtypes.Claim) { c.MultiIndex++ }},
			{"ordinary only", func(c *camtypes.Claim) { c.MultiIndex = -1 }},
			{"invalid ordinal", func(c *camtypes.Claim) { c.MultiIndex = -2 }},
			{"target", func(c *camtypes.Claim) { c.Target = blob.RefFromString("not an attribute claim") }},
		} {
			t.Run(fmt.Sprintf("corpus=%v/%s", corpus, tt.name), func(t *testing.T) {
				ix, _, cl, _ := newClaimLookupIndex(t, corpus, 0)
				tt.change(&cl)
				if got, err := lookupClaim(t, ix, context.Background(), cl); err != nil || got {
					t.Fatalf("HasClaim = %v, %v; want false", got, err)
				}
			})
		}
	}
}

func TestHasClaimZeroFallback(t *testing.T) {
	ix, store, cl, bare := newClaimLookupIndex(t, false, -1)
	suffixed := fmt.Sprintf("%s|%020d", bare, 0)
	if err := store.Set(suffixed, keyPermanodeClaim.Val(cl.Type, cl.Attr, "different child zero", cl.Signer)); err != nil {
		t.Fatal(err)
	}
	if got, err := lookupClaim(t, ix, context.Background(), cl); err != nil || !got {
		t.Fatalf("ordinary fallback after nonmatching child zero = %v, %v", got, err)
	}
	if len(store.gets) != 3 || store.gets[1] != suffixed || store.gets[2] != bare {
		t.Fatalf("lookup keys = %q; want signer, suffixed zero, bare", store.gets)
	}
}

func TestHasClaimErrors(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%v", corpus), func(t *testing.T) {
			ix, store, cl, key := newClaimLookupIndex(t, corpus, 0)
			failure := errors.New("point lookup storage failure")
			store.failKey, store.err = key, failure
			if got, err := lookupClaim(t, ix, context.Background(), cl); got || !errors.Is(err, failure) {
				t.Fatalf("row failure = %v, %v", got, err)
			}
			if !corpus {
				store.failKey = "signerkeyid:" + cl.Signer.String()
				if got, err := lookupClaim(t, ix, context.Background(), cl); got || !errors.Is(err, failure) {
					t.Fatalf("signer failure = %v, %v", got, err)
				}
			}
			store.err = nil
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			store.gets = nil
			if got, err := lookupClaim(t, ix, ctx, cl); got || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled lookup = %v, %v", got, err)
			}
			if len(store.gets) != 0 {
				t.Fatal("canceled lookup accessed storage")
			}
			ctx, cancel = context.WithCancel(context.Background())
			defer cancel()
			store.cancel = cancel
			if got, err := lookupClaim(t, ix, ctx, cl); got || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation during lookup = %v, %v", got, err)
			}
		})
	}
}

func TestHasClaimDeleted(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%v", corpus), func(t *testing.T) {
			ix, _, cl, _ := newClaimLookupIndex(t, corpus, 0)
			deleter := blob.RefFromString("point lookup deleter")
			ix.deletes.Lock()
			ix.deletes.m[cl.BlobRef] = []deletion{{deleter: deleter, when: cl.Date.Add(time.Second)}}
			ix.deletes.Unlock()
			if got, err := lookupClaim(t, ix, context.Background(), cl); err != nil || got {
				t.Fatalf("deleted envelope = %v, %v; want false", got, err)
			}
		})
	}
}

func TestHasClaimCanonicalDate(t *testing.T) {
	ix, store, cl, key := newClaimLookupIndex(t, false, -1)
	// The instant is the same but its original textual spelling is unavailable.
	alternate := strings.Replace(key, schema.RFC3339FromTime(cl.Date),
		cl.Date.In(time.FixedZone("offset", 3600)).Format(time.RFC3339Nano), 1)
	value, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(key); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(alternate, value); err != nil {
		t.Fatal(err)
	}
	if got, err := lookupClaim(t, ix, context.Background(), cl); err != nil || got {
		t.Fatalf("noncanonical stored date = %v, %v; want false", got, err)
	}
}
