/*
Copyright 2026 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package immich

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

// These tests exercise the KV implementation directly. HTTP, constructor and
// legacy-import coverage lives in the other immich tests.
func immichStateTestAsset(t *testing.T, h *Handler, sequence uint64) immichAsset {
	t.Helper()
	ctx := context.Background()
	original := fmt.Sprintf("native state original %d", sequence)
	fr, err := schema.WriteFileFromReader(ctx, h.storage, "original.jpg", strings.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	pn, err := h.storeSigned(ctx, schema.NewPlannedPermanode(fmt.Sprintf("immich-state-test-photo-%d", sequence)), time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Ordinary claims, not just multi children, must rebuild correctly.
	for _, attr := range [][2]string{{"camliContent", fr.String()}, {"title", "Existing title"}, {"tag", "Keep my tag"}} {
		if _, err := h.storeSigned(ctx, schema.NewSetAttributeClaim(pn, attr[0], attr[1]), time.Unix(1500000000, 0)); err != nil {
			t.Fatal(err)
		}
	}
	id, err := immichUUID()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum([]byte(original))
	return immichAsset{
		ID: id, Checksum: hex.EncodeToString(sum[:]), WholeRef: blob.RefFromString(original),
		FileRef: fr, PermanodeRef: pn, Name: "phone.jpg", Type: "IMAGE",
		CreatedAt: time.Unix(1600000000, 0).UTC(), FileCreatedAt: time.Unix(1400000000, 0).UTC(),
		Size: int64(len(original)), Sequence: sequence, Visibility: "timeline",
		Metadata: json.RawMessage(`[{"key":"description","value":{"description":"metadata is in the immutable record"}}]`),
	}
}

func immichStateTestSetAsset(t *testing.T, b sorted.BatchMutation, a immichAsset) {
	t.Helper()
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	b.Set("asset|"+a.ID, string(data))
}

func immichStateTestRows(t *testing.T, kv sorted.KeyValue) map[string]string {
	t.Helper()
	rows := make(map[string]string)
	if err := sorted.Foreach(kv, func(k, v string) error {
		rows[k] = v
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return rows
}

type immichStateDenyOriginalFetch struct {
	blobserver.Storage
	denied map[blob.Ref]bool
}

func (s immichStateDenyOriginalFetch) Fetch(ctx context.Context, ref blob.Ref) (io.ReadCloser, uint32, error) {
	if s.denied[ref] {
		return nil, 0, fmt.Errorf("startup attempted to fetch original %v", ref)
	}
	return s.Storage.Fetch(ctx, ref)
}

func TestImmichStateNativeRebuild(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, corpus)
			a := immichStateTestAsset(t, h, 2)
			video := immichStateTestAsset(t, h, 1)
			video.Type = "VIDEO"
			a.LivePhotoVideoID = video.ID
			a.SidecarRef = blob.RefFromString("original sidecar")
			b := h.metadata.BeginBatch()
			immichStateTestSetAsset(t, b, a)
			immichStateTestSetAsset(t, b, video)
			b.Set("session|test-session", `{"ID":"device-session","DeviceOS":"ios"}`)
			b.Set("user|preferences", `{"memories":{"enabled":false}}`)
			b.Set("syncack|device|AssetV1", "AssetV1|2")
			// A preference-only bump must outlive a reconstruction from photos.
			b.Set("sequence", "7")
			if err := h.metadata.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			before := immichStateTestRows(t, h.metadata)
			s := h.metadata.(*immichState)
			cl, err := s.claims(context.Background(), a.PermanodeRef)
			if err != nil {
				t.Fatal(err)
			}
			attrs, err := s.nativeAttrs(context.Background(), a.PermanodeRef, cl, make(map[blob.Ref]immichStateClaim))
			if err != nil {
				t.Fatal(err)
			}
			if attrs.Get(immichAssetIDAttr) != a.ID || attrs.Get(immichChecksumAttr) != a.Checksum ||
				attrs.Get(immichStateAttr) != s.account.String() || attrs.Get(immichRecordAttr) == "" ||
				attrs.Get("title") != "Existing title" || attrs.Get("tag") != "Keep my tag" {
				t.Fatalf("incorrect photo attributes: %v", attrs)
			}
			if _, err := h.metadata.Get("whole|" + a.WholeRef.String()); err != nil {
				t.Fatal(err)
			}
			storage := h.storage
			// Rebuild neither touches the original file schemas/chunks nor
			// relies on the HTTP handler's cached metadata.
			h.storage = immichStateDenyOriginalFetch{Storage: storage, denied: map[blob.Ref]bool{
				a.FileRef: true, a.WholeRef: true, video.FileRef: true, video.WholeRef: true,
			}}
			h.prefix = "/moved/api/"
			restored, err := newImmichState(h)
			h.storage = storage
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if after := immichStateTestRows(t, restored); !reflect.DeepEqual(before, after) {
				t.Fatalf("rebuild changed state:\nbefore=%v\nafter=%v", before, after)
			}
			if restored.(*immichState).account != s.account {
				t.Fatal("moving the mount changed the native account")
			}
		})
	}
}

func TestImmichStateNativeQueryIsUnlimited(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, corpus)
			b := h.metadata.BeginBatch()
			for n := uint64(1); n <= 205; n++ {
				immichStateTestSetAsset(t, b, immichStateTestAsset(t, h, n))
			}
			if err := h.metadata.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			reopened, err := newImmichState(h)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if got := len(reopened.(*immichState).assets); got != 205 {
				t.Fatalf("query silently truncated catalog to %d", got)
			}
		})
	}
}

// An error may occur after the index accepts a claim but before primary
// persistence. Reads must not expose the previous state as though that failure
// meant the transaction definitely did not happen.
type immichStatePartialClaim struct {
	*immichTestStorage
	ref          blob.Ref
	text         string
	falseSuccess bool
}

func (s *immichStatePartialClaim) ReceiveBlob(ctx context.Context, ref blob.Ref, src io.Reader) (blob.SizedRef, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return blob.SizedRef{}, err
	}
	var meta struct{ CamliType string }
	if json.Unmarshal(data, &meta) == nil && meta.CamliType == "claim" {
		if _, err := s.index.ReceiveBlob(ctx, ref, bytes.NewReader(data)); err != nil {
			return blob.SizedRef{}, err
		}
		s.ref, s.text = ref, string(data)
		if s.falseSuccess {
			return blob.SizedRef{Ref: ref, Size: uint32(len(data))}, nil
		}
		return blob.SizedRef{}, errors.New("index succeeded, primary failed")
	}
	return s.immichTestStorage.ReceiveBlob(ctx, ref, bytes.NewReader(data))
}

func TestImmichStateAmbiguousPublicationFailsClosed(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		for _, falseSuccess := range []bool{false, true} {
			t.Run(fmt.Sprintf("corpus=%t/falseSuccess=%t", corpus, falseSuccess), func(t *testing.T) {
				h := newImmichTestHandlerWithCorpus(t, corpus)
				a := immichStateTestAsset(t, h, 1)
				s := h.metadata.(*immichState)
				before := immichStateTestRows(t, s.view)
				primary := h.storage.(*immichTestStorage)
				fault := &immichStatePartialClaim{immichTestStorage: primary, falseSuccess: falseSuccess}
				h.storage = fault
				b := h.metadata.BeginBatch()
				immichStateTestSetAsset(t, b, a)
				b.Set("session|transaction-test", "durable-session")
				if err := h.metadata.CommitBatch(b); err == nil {
					t.Fatal("partially failed native publication succeeded")
				}
				h.storage = primary
				if !fault.ref.Valid() {
					t.Fatal("claim write failure was not exercised")
				}
				if after := immichStateTestRows(t, s.view); !reflect.DeepEqual(before, after) {
					t.Fatal("failed publication modified the local view")
				}
				for _, key := range []string{"asset|" + a.ID, "user|id", "whole|" + a.WholeRef.String()} {
					if _, err := s.Get(key); err == nil || errors.Is(err, sorted.ErrNotFound) {
						t.Fatalf("poisoned Get(%q) = %v", key, err)
					}
				}
				it := s.Find("", "")
				if it.Next() || it.Close() == nil {
					t.Fatal("poisoned iterator exposed state")
				}
				if err := s.Set("session|another", "new"); err == nil {
					t.Fatal("poisoned state accepted a new write")
				}
				if reopened, err := newImmichState(h); err == nil {
					reopened.Close()
					t.Fatal("startup accepted an index-only claim")
				}
				if _, err := blobserver.Receive(context.Background(), primary, fault.ref, strings.NewReader(fault.text)); err != nil {
					t.Fatal(err)
				}
				restored, err := newImmichState(h)
				if err != nil {
					t.Fatal(err)
				}
				defer restored.Close()
				if id, err := restored.Get("whole|" + a.WholeRef.String()); err != nil || id != a.ID {
					t.Fatalf("repair lost durable UUID: %q, %v", id, err)
				}
				if v, err := restored.Get("session|transaction-test"); err != nil || v != "durable-session" {
					t.Fatalf("repair lost transaction's session: %q, %v", v, err)
				}
			})
		}
	}
}

type immichStateMissingFetch struct {
	blobserver.Storage
	missing blob.Ref
}

func (s immichStateMissingFetch) Fetch(ctx context.Context, ref blob.Ref) (io.ReadCloser, uint32, error) {
	if ref == s.missing {
		return nil, 0, os.ErrNotExist
	}
	return s.Storage.Fetch(ctx, ref)
}

func TestImmichStateMissingRecordFailsClosed(t *testing.T) {
	h := newImmichTestHandler(t)
	a := immichStateTestAsset(t, h, 1)
	b := h.metadata.BeginBatch()
	immichStateTestSetAsset(t, b, a)
	if err := h.metadata.CommitBatch(b); err != nil {
		t.Fatal(err)
	}
	record, err := h.metadata.Get("asset|" + a.ID)
	if err != nil {
		t.Fatal(err)
	}
	storage := h.storage
	h.storage = immichStateMissingFetch{Storage: storage, missing: blob.RefFromString(record)}
	defer func() { h.storage = storage }()
	if reopened, err := newImmichState(h); err == nil {
		reopened.Close()
		t.Fatal("startup accepted a missing current asset record")
	}
}

func TestImmichStateLostPhotoIndexFailsClosed(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, false)
			a := immichStateTestAsset(t, h, 1)
			b := h.metadata.BeginBatch()
			immichStateTestSetAsset(t, b, a)
			if err := h.metadata.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			// Rare account transactions checkpoint a catalog floor. A wholly
			// missing, uncheckpointed photo-index tail cannot be detected from
			// the account without reintroducing a per-upload account anchor.
			if err := h.metadata.Set("session|catalog-checkpoint", "checkpoint"); err != nil {
				t.Fatal(err)
			}
			ix := h.search.Index().(*index.Index)
			kv := ix.Storage()
			it := kv.Find("", "")
			var keys []string
			for it.Next() {
				if strings.Contains(it.Key(), a.PermanodeRef.String()) {
					keys = append(keys, it.Key())
				}
			}
			if err := it.Close(); err != nil {
				t.Fatal(err)
			}
			for _, key := range keys {
				if err := kv.Delete(key); err != nil {
					t.Fatal(err)
				}
			}
			if corpus {
				// Build a fresh corpus from the damaged native index, rather
				// than leaving a live corpus that still has the removed rows.
				c, err := ix.KeepInMemory()
				if err != nil {
					t.Fatal(err)
				}
				h.search.SetCorpus(c)
			}
			if reopened, err := newImmichState(h); err == nil {
				reopened.Close()
				t.Fatal("startup silently shrank the catalog after losing photo index rows")
			}
		})
	}
}

func TestImmichStateLostSessionDeletionIndexFailsClosed(t *testing.T) {
	h := newImmichTestHandlerWithCorpus(t, false)
	if err := h.metadata.Set("session|revoked", "old-session"); err != nil {
		t.Fatal(err)
	}
	if err := h.metadata.Delete("session|revoked"); err != nil {
		t.Fatal(err)
	}
	kv := h.search.Index().(*index.Index).Storage()
	it := kv.Find("claim|", "claim}")
	var keys []string
	for it.Next() {
		if strings.Contains(it.Value(), "del-attribute") {
			keys = append(keys, it.Key())
		}
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	if len(keys) == 0 {
		t.Fatal("test did not find the session deletion row")
	}
	for _, key := range keys {
		if err := kv.Delete(key); err != nil {
			t.Fatal(err)
		}
	}
	if reopened, err := newImmichState(h); err == nil {
		reopened.Close()
		t.Fatal("startup accepted a partially indexed logout and resurrected a session")
	}
}

func TestImmichStateMissingAccountIndexFailsClosed(t *testing.T) {
	h := newImmichTestHandler(t)
	owner := h.search.Owner()
	ix := index.NewMemoryIndex()
	defer ix.Close()
	h.search = search.NewHandler(ix, index.NewOwner(h.signer.KeyIDLong(), owner))
	if reopened, err := newImmichState(h); err == nil {
		reopened.Close()
		t.Fatal("startup invented an owner after losing the account index")
	}
}

func TestImmichStateNoopDoesNotPublish(t *testing.T) {
	h := newImmichTestHandler(t)
	key, value := "syncack|device|AssetV1", "AssetV1|42"
	if err := h.metadata.Set(key, value); err != nil {
		t.Fatal(err)
	}
	s := h.metadata.(*immichState)
	primary := h.storage.(*immichTestStorage)
	before := len(primary.BlobrefStrings())
	tx := s.tx
	b := h.metadata.BeginBatch()
	b.Set(key, value)
	b.Set(key, value)
	b.Delete("session|already-missing")
	if err := h.metadata.CommitBatch(b); err != nil {
		t.Fatal(err)
	}
	if s.tx != tx || len(primary.BlobrefStrings()) != before {
		t.Fatal("no-op ACKs or deletion retained a new transaction")
	}
}

func TestImmichStateMonotonicDateAndCompetingWriter(t *testing.T) {
	h := newImmichTestHandler(t)
	stale, err := newImmichState(h)
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	s := h.metadata.(*immichState)
	future := time.Now().Add(24 * time.Hour).UTC()
	s.lastDate = future
	if err := s.Set("session|future", "future-session"); err != nil {
		t.Fatal(err)
	}
	if !s.lastDate.After(future) {
		t.Fatal("claim date went backwards")
	}
	restored, err := newImmichState(h)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	r := restored.(*immichState)
	if err := r.Set("sequence", "4"); err != nil {
		t.Fatal(err)
	}
	if !r.lastDate.After(s.lastDate) {
		t.Fatal("claim date went backwards across restart/clock rollback")
	}
	// Cross-process exclusion is a deployment requirement, not a native CAS.
	// If that requirement is violated, startup must detect the branch rather
	// than silently merge two owners' independent account histories.
	if err := stale.Set("session|stale-writer", "must not merge"); err == nil {
		if merged, err := newImmichState(h); err == nil {
			merged.Close()
			t.Fatal("startup silently merged divergent account writers")
		}
	}
}

func TestImmichStateSHA1HintUsesNewestSequenceWithinBatch(t *testing.T) {
	h := newImmichTestHandler(t)
	a, b := immichStateTestAsset(t, h, 1), immichStateTestAsset(t, h, 2)
	a.ID, b.ID = "ffffffff-ffff-4fff-bfff-ffffffffffff", "00000000-0000-4000-8000-000000000000"
	b.Checksum = a.Checksum // simulate SHA-1 collision without changing WholeRef
	batch := h.metadata.BeginBatch()
	immichStateTestSetAsset(t, batch, a)
	immichStateTestSetAsset(t, batch, b)
	if err := h.metadata.CommitBatch(batch); err != nil {
		t.Fatal(err)
	}
	if id, err := h.metadata.Get("checksum|" + a.Checksum); err != nil || id != b.ID {
		t.Fatalf("checksum hint = %q, %v; want sequence %s", id, err, strconv.FormatUint(b.Sequence, 10))
	}
	reopened, err := newImmichState(h)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if id, err := reopened.Get("checksum|" + a.Checksum); err != nil || id != b.ID {
		t.Fatalf("rebuilt checksum hint = %q, %v", id, err)
	}
}

func TestImmichStateMigrationActivation(t *testing.T) {
	base := newImmichTestHandler(t)
	ix := index.NewMemoryIndex()
	t.Cleanup(func() { ix.Close() })
	storage := &immichTestStorage{Fetcher: new(test.Fetcher), index: ix}
	ix.InitBlobSource(storage)
	ref := base.search.Owner()
	pub, _, err := base.storage.Fetch(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	if _, err := blobserver.Receive(context.Background(), storage, ref, pub); err != nil {
		t.Fatal(err)
	}
	h := &Handler{
		storage: storage, signer: base.signer,
		search: search.NewHandler(ix, index.NewOwner(base.signer.KeyIDLong(), ref)),
	}
	h.metadata, err = newImmichStateForMigration(h)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { h.metadata.Close() }()
	a := immichStateTestAsset(t, h, 3)
	b := h.metadata.BeginBatch()
	immichStateTestSetAsset(t, b, a)
	b.Set("session|migrated", "session")
	b.Set("sequence", "8") // includes legacy preference-only bumps
	if err := h.metadata.CommitBatch(b); err != nil {
		t.Fatal(err)
	}
	if ordinary, err := newImmichState(h); err == nil {
		ordinary.Close()
		t.Fatal("ordinary load accepted a partially imported account")
	}
	resumed, err := newImmichStateForMigration(h)
	if err != nil {
		t.Fatal(err)
	}
	h.metadata.Close()
	h.metadata = resumed
	if err := h.metadata.Set("user|id", base.uid); err != nil {
		t.Fatal(err)
	}
	reopened, err := newImmichState(h)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for key, want := range map[string]string{
		"user|id": base.uid, "sequence": "8", "whole|" + a.WholeRef.String(): a.ID, "session|migrated": "session",
	} {
		if got, err := reopened.Get(key); err != nil || got != want {
			t.Fatalf("Get(%q) = %q, %v; want %q", key, got, err, want)
		}
	}
}

type immichStateBadFetch struct {
	blobserver.Storage
	ref  blob.Ref
	data string
	size uint32
}

func (s immichStateBadFetch) Fetch(ctx context.Context, ref blob.Ref) (io.ReadCloser, uint32, error) {
	if ref == s.ref {
		return io.NopCloser(strings.NewReader(s.data)), s.size, nil
	}
	return s.Storage.Fetch(ctx, ref)
}

func TestImmichStateReadBoundsHashAndSize(t *testing.T) {
	h := newImmichTestHandler(t)
	s := h.metadata.(*immichState)
	const original = "0123456789"
	ref := blob.RefFromString(original)
	storage := h.storage
	defer func() { h.storage = storage }()
	for _, tc := range []struct {
		name  string
		data  string
		size  uint32
		limit int
	}{
		{"hash", "012345678X", 10, 10},
		{"size", original, 9, 10},
		{"advertised-bound", original, 10, 9},
		{"actual-bound", original + strings.Repeat("x", 1000), 10, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.storage = immichStateBadFetch{Storage: storage, ref: ref, data: tc.data, size: tc.size}
			if _, err := s.fetch(context.Background(), ref, tc.limit); err == nil {
				t.Fatal("accepted an invalid or oversized primary blob")
			}
		})
	}
}

func TestImmichStateNativeSizeLimits(t *testing.T) {
	h := newImmichTestHandler(t)
	// A maximum-sized KV value becomes a linked immutable blob, not a large
	// attribute that would be silently omitted by the sorted native index.
	value := strings.Repeat(`"`, sorted.MaxValueSize)
	if err := h.metadata.Set("session|large", value); err != nil {
		t.Fatal(err)
	}
	reopened, err := newImmichState(h)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, err := reopened.Get("session|large"); err != nil || got != value {
		t.Fatalf("maximum-sized value did not rebuild: got %d bytes, %v", len(got), err)
	}
	primary := h.storage.(*immichTestStorage)
	before := len(primary.BlobrefStrings())
	b := h.metadata.BeginBatch()
	for i := 0; i < 1200; i++ {
		b.Set(fmt.Sprintf("session|%04d|%s", i, strings.Repeat("x", 650)), "value")
	}
	if err := h.metadata.CommitBatch(b); err == nil {
		t.Fatal("accepted a transaction larger than native schema indexing limit")
	}
	if len(primary.BlobrefStrings()) != before {
		t.Fatal("oversized transaction wrote native blobs before validation")
	}
	if err := h.metadata.Set("session|still-healthy", "okay"); err != nil {
		t.Fatalf("validation failure unnecessarily poisoned the writer: %v", err)
	}
}

func TestImmichStateRejectsInvalidOrDuplicatePhotoPN(t *testing.T) {
	for _, kind := range []string{"non-permanode", "duplicate-pn"} {
		t.Run(kind, func(t *testing.T) {
			h := newImmichTestHandler(t)
			a := immichStateTestAsset(t, h, 1)
			b := h.metadata.BeginBatch()
			if kind == "non-permanode" {
				a.PermanodeRef = a.FileRef
				immichStateTestSetAsset(t, b, a)
			} else {
				other := immichStateTestAsset(t, h, 2)
				other.PermanodeRef = a.PermanodeRef
				immichStateTestSetAsset(t, b, a)
				immichStateTestSetAsset(t, b, other)
			}
			if err := h.metadata.CommitBatch(b); err == nil {
				t.Fatal("accepted invalid photo permanode assignment")
			}
			if _, err := h.metadata.Get("asset|" + a.ID); !errors.Is(err, sorted.ErrNotFound) {
				t.Fatalf("invalid batch changed local state: %v", err)
			}
		})
	}
}

type immichStateCountingIndexKV struct {
	sorted.KeyValue
	rootPrefix string
	rootScans  int
	pointReads int
}

func (ix *immichStateCountingIndexKV) Find(start, end string) sorted.Iterator {
	if strings.HasPrefix(start, ix.rootPrefix) {
		ix.rootScans++
	}
	return ix.KeyValue.Find(start, end)
}

func (ix *immichStateCountingIndexKV) Get(key string) (string, error) {
	if strings.HasPrefix(key, ix.rootPrefix) {
		ix.pointReads++
	}
	return ix.KeyValue.Get(key)
}

func TestImmichStateHotPathDoesNotScanAccountHistory(t *testing.T) {
	h := newImmichTestHandlerWithCorpus(t, false)
	kv := &immichStateCountingIndexKV{
		KeyValue: sorted.NewMemoryKeyValue(), rootPrefix: "claim|" + h.metadata.(*immichState).account.String() + "|",
	}
	if err := sorted.Foreach(h.search.Index().(*index.Index).Storage(), func(k, v string) error {
		return kv.Set(k, v)
	}); err != nil {
		t.Fatal(err)
	}
	ix, err := index.New(kv)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	primary := h.storage.(*immichTestStorage)
	primary.index = ix
	ix.InitBlobSource(primary)
	h.search = search.NewHandler(ix, index.NewOwner(h.signer.KeyIDLong(), h.search.Owner()))
	for n := 0; n < 20; n++ {
		if err := h.metadata.Set("syncack|device|AssetV1", strconv.Itoa(n)); err != nil {
			t.Fatal(err)
		}
	}
	if kv.rootScans != 0 || kv.pointReads != 20 {
		t.Fatalf("publication rescanned growing account history: %d scans, %d point confirmations", kv.rootScans, kv.pointReads)
	}
}

func TestImmichStateFutureAssetClaimRebuild(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, corpus)
			h.metadata.(*immichState).lastDate = time.Now().UTC().Add(24 * time.Hour)
			a := immichStateTestAsset(t, h, 1)
			b := h.metadata.BeginBatch()
			immichStateTestSetAsset(t, b, a)
			if err := h.metadata.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			restored, err := newImmichState(h)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if id, err := restored.Get("whole|" + a.WholeRef.String()); err != nil || id != a.ID {
				t.Fatalf("clock rollback hid a durable photo: %q, %v", id, err)
			}
		})
	}
}
