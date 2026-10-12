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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
	"perkeep.org/pkg/types/camtypes"
)

// Count both signed envelopes and their indexed child rows. Counting only the
// head, or only unique envelopes, misses accidental per-photo account mutations.
type immichPhotoAccountSnapshot struct {
	tx        uint64
	head      blob.Ref
	claims    []camtypes.Claim
	envelopes map[blob.Ref]bool
}

func immichPhotoAccount(t *testing.T, h *Handler) immichPhotoAccountSnapshot {
	t.Helper()
	s := h.metadata.(*immichState)
	claims, err := s.claims(context.Background(), s.account)
	if err != nil {
		t.Fatal(err)
	}
	envelopes := make(map[blob.Ref]bool)
	for _, cl := range claims {
		envelopes[cl.BlobRef] = true
	}
	tx, head, err := s.accountHead(claims)
	if err != nil || tx != s.tx || head != s.head {
		t.Fatalf("invalid account chain: tx=%d head=%v, runtime=%d/%v: %v", tx, head, s.tx, s.head, err)
	}
	return immichPhotoAccountSnapshot{s.tx, s.head, claims, envelopes}
}

func immichPhotoAccountUnchanged(t *testing.T, h *Handler, before immichPhotoAccountSnapshot) {
	t.Helper()
	after := immichPhotoAccount(t, h)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("photo publication mutated account: tx %d->%d, head %v->%v, envelopes %d->%d, child rows %d->%d",
			before.tx, after.tx, before.head, after.head, len(before.envelopes), len(after.envelopes), len(before.claims), len(after.claims))
	}
}

func immichPhotoAccountAttrs(t *testing.T, h *Handler) map[string][]string {
	t.Helper()
	s := h.metadata.(*immichState)
	cl, err := s.claims(context.Background(), s.account)
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := s.nativeAttrs(context.Background(), s.account, cl, make(map[blob.Ref]immichStateClaim))
	if err != nil {
		t.Fatal(err)
	}
	return attrs
}

func immichPhotoDurableSequence(t *testing.T, h *Handler) uint64 {
	t.Helper()
	attr := immichStateKeyPrefix + base64.RawURLEncoding.EncodeToString([]byte("sequence"))
	attrs := immichPhotoAccountAttrs(t, h)
	values := attrs[attr]
	if len(values) == 0 {
		return 0
	}
	if len(values) != 1 {
		t.Fatalf("multiple durable account sequences: %v", values)
	}
	data, err := h.metadata.(*immichState).fetch(context.Background(), blob.MustParse(values[0]), immichStateMaxValueBlob)
	if err != nil {
		t.Fatal(err)
	}
	var value immichStateValue
	if err := json.Unmarshal(data, &value); err != nil || value.Key != "sequence" {
		t.Fatalf("invalid durable sequence value: %s, %v", data, err)
	}
	n, err := strconv.ParseUint(value.Value, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func immichPhotoAssertAccountSnapshot(t *testing.T, h *Handler, count int, usage int64, sequence uint64) {
	t.Helper()
	s := h.metadata.(*immichState)
	account := immichPhotoAccount(t, h)
	sequenceAttr := immichStateKeyPrefix + base64.RawURLEncoding.EncodeToString([]byte("sequence"))
	want := map[string]string{
		immichStateAssetCount: strconv.Itoa(count),
		immichStateUsage:      strconv.FormatInt(usage, 10),
	}
	seen := make(map[string]bool)
	for _, cl := range account.claims {
		if cl.BlobRef != s.head {
			continue
		}
		if value, ok := want[cl.Attr]; ok {
			if cl.Value != value || cl.Type != string(schema.SetAttributeClaim) {
				t.Fatalf("latest rare transaction has wrong snapshot child: %+v", cl)
			}
			seen[cl.Attr] = true
		}
		if cl.Attr == sequenceAttr {
			seen[sequenceAttr] = true
		}
	}
	if !seen[immichStateAssetCount] || !seen[immichStateUsage] || !seen[sequenceAttr] ||
		immichPhotoDurableSequence(t, h) != sequence {
		t.Fatalf("rare transaction did not refresh all snapshot fields: seen=%v sequence=%d", seen, sequence)
	}
}

func immichPhotoAssertPublication(t *testing.T, h *Handler, a immichAsset) {
	t.Helper()
	s := h.metadata.(*immichState)
	claims, err := s.claims(context.Background(), a.PermanodeRef)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		immichStateAttr: s.account.String(), immichAssetIDAttr: a.ID,
		immichChecksumAttr: a.Checksum,
	}
	record, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	want[immichRecordAttr] = blob.RefFromBytes(record).String()
	var envelope blob.Ref
	for _, cl := range claims {
		if cl.Attr == immichStateAttr {
			if envelope.Valid() && envelope != cl.BlobRef {
				t.Fatal("photo has multiple adapter publications")
			}
			envelope = cl.BlobRef
		}
	}
	if !envelope.Valid() {
		t.Fatal("photo has no adapter publication")
	}
	data, err := s.fetch(context.Background(), envelope, schema.MaxSchemaBlobSize)
	if err != nil {
		t.Fatal(err)
	}
	b, err := schema.BlobFromReader(envelope, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	c, ok := b.AsClaim()
	if !ok || c.ClaimType() != "multi" {
		t.Fatalf("adapter publication is not a multi-claim: %s", data)
	}
	ops, err := c.AttributeClaims()
	if err != nil || len(ops) != 4 {
		t.Fatalf("adapter publication has %d children, want exactly four: %v", len(ops), err)
	}
	seen := make(map[string]bool)
	for _, op := range ops {
		if op.Permanode != a.PermanodeRef || op.Type != schema.SetAttributeClaim ||
			seen[op.Attribute] || want[op.Attribute] == "" || want[op.Attribute] != op.Value {
			t.Fatalf("adapter publication contains non-photo bookkeeping or duplicate/wrong attribute: %+v", op)
		}
		seen[op.Attribute] = true
	}
	for _, cl := range claims {
		if cl.BlobRef == envelope && (cl.Permanode != a.PermanodeRef || cl.MultiIndex < 0) {
			t.Fatalf("adapter publication not indexed on the single photo PN: %+v", cl)
		}
		if cl.Attr == "camliContent" && cl.BlobRef == envelope {
			t.Fatal("initial native content and adapter state share an envelope")
		}
	}
}

// Reindex PRIMARY blobs into a fresh index, with no old corpus, KV rows,
// handler identity, sequence, counters, or ACK cache surviving the restart.
func immichPhotoBlobOnlyRestart(t *testing.T, old *Handler, corpus bool) *Handler {
	t.Helper()
	primary := old.storage.(*immichTestStorage)
	ix := index.NewMemoryIndex()
	ix.Logger = test.NewLogger(t, "")
	storage := &immichTestStorage{Fetcher: new(test.Fetcher), index: ix}
	ix.InitBlobSource(storage)
	refs := primary.BlobrefStrings()
	for _, textRef := range refs {
		ref := blob.MustParse(textRef)
		text, ok := primary.BlobContents(ref)
		if !ok {
			t.Fatalf("missing primary blob %v", ref)
		}
		if _, err := storage.Fetcher.ReceiveBlob(context.Background(), ref, strings.NewReader(text)); err != nil {
			t.Fatal(err)
		}
	}
	// Dependencies are already readable from primary, independent of blob order.
	for _, textRef := range refs {
		ref := blob.MustParse(textRef)
		text, _ := primary.BlobContents(ref)
		if _, err := ix.ReceiveBlob(context.Background(), ref, strings.NewReader(text)); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{
		storage: storage, signer: old.signer,
		search: search.NewHandler(ix, index.NewOwner(old.signer.KeyIDLong(), old.search.Owner())),
		email:  old.email, password: old.password, prefix: "/reopened/immich/api/", maxUploadBytes: old.maxUploadBytes,
	}
	if corpus {
		c, err := ix.KeepInMemory()
		if err != nil {
			t.Fatal(err)
		}
		h.search.SetCorpus(c)
	}
	var err error
	h.metadata, err = newImmichState(h)
	if err != nil {
		ix.Close()
		t.Fatal(err)
	}
	if err := h.initIdentity(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
		if err := ix.Close(); err != nil {
			t.Error(err)
		}
	})
	return h
}

func TestImmichPhotoStateUploadsNeverMutateAccount(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, corpus)
			token := immichTestLogin(t, h)
			// The first profile read lazily persists compatibility metadata;
			// include that rare write in bootstrap, not the upload/ACK window.
			if w := immichTestRequest(t, h, token, "GET", "users/me", nil, ""); w.Code != 200 {
				t.Fatalf("profile bootstrap: %d %s", w.Code, w.Body)
			}
			account := immichPhotoAccount(t, h)
			const count = 205 // More than search's default limit, in separate commits.
			var usage int64
			assets := make(map[string]immichAsset)
			for n := range count {
				original := fmt.Sprintf("photo-only upload %03d", n)
				// Capture time is deliberately unrelated to publication order.
				date := time.Date(2099-n%100, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
				id := immichUploadedID(t, immichTestUpload(t, h, token, original, map[string]string{"fileCreatedAt": date}), 201)
				a := *immichDedupAsset(t, h, id)
				if a.Sequence != uint64(n+1) {
					t.Fatalf("upload %d sequence = %d", n, a.Sequence)
				}
				assets[id] = a
				usage += int64(len(original))
				immichPhotoAssertPublication(t, h, a)
				immichPhotoAccountUnchanged(t, h, account)
			}
			s := h.metadata.(*immichState)
			if len(s.assets) != count || s.usage != usage || s.sequence != count {
				t.Fatalf("RAM catalog: count=%d usage=%d sequence=%d", len(s.assets), s.usage, s.sequence)
			}
			if got := immichPhotoDurableSequence(t, h); got != 0 {
				t.Fatalf("uploads persisted account sequence %d", got)
			}
			// A rare preference-only transaction checkpoints the photo-derived
			// catalog and must retain a gap beyond the latest photo sequence.
			prefs := map[string]any{"memories": map[string]any{"enabled": true}}
			if w := immichMobileTestJSON(t, h, token, "PUT", "users/me/preferences", prefs); w.Code != 200 {
				t.Fatalf("preference PUT: %d %s", w.Code, w.Body)
			}
			preferenceWrite := immichPhotoAccount(t, h)
			for _, request := range []struct {
				method string
				value  any
			}{{"PUT", prefs}, {"PATCH", prefs}, {"PUT", map[string]any{}}, {"PATCH", map[string]any{}}} {
				if w := immichMobileTestJSON(t, h, token, request.method, "users/me/preferences", request.value); w.Code != 200 {
					t.Fatalf("identical/empty preference %s: %d %s", request.method, w.Code, w.Body)
				}
				immichPhotoAccountUnchanged(t, h, preferenceWrite)
			}
			prefSeq, err := h.mobileSequence()
			if err != nil || prefSeq <= count || immichPhotoDurableSequence(t, h) != prefSeq {
				t.Fatalf("preference-only sequence gap not durable: %d, %v", prefSeq, err)
			}
			attrs := immichPhotoAccountAttrs(t, h)
			if !reflect.DeepEqual(attrs[immichStateAssetCount], []string{strconv.Itoa(count)}) ||
				!reflect.DeepEqual(attrs[immichStateUsage], []string{strconv.FormatInt(usage, 10)}) {
				t.Fatalf("rare account transaction did not checkpoint catalog: %v", attrs)
			}
			checkpoint := immichPhotoAccount(t, h)
			if checkpoint.tx <= account.tx || checkpoint.head == account.head {
				t.Fatal("preference transaction did not retain an account chain")
			}
			immichPhotoAssertAccountSnapshot(t, h, count, usage, prefSeq)
			events := immichMobileTestStream(t, h, token, "AssetsV2")
			if len(events) != count+2 {
				t.Fatalf("catalog stream truncated: %d events", len(events))
			}
			for n, event := range events[:count] {
				if event.Type != "AssetV2" || event.Ack != fmt.Sprintf("AssetV2|%d", n+1) {
					t.Fatalf("search/camera ordering leaked into sequence stream at %d: %+v", n, event)
				}
				immichMobileTestAck(t, h, token, event.Ack)
			}
			immichPhotoAccountUnchanged(t, h, checkpoint)
			h.mu.Lock()
			acks := h.acks
			now := acks.lastFlush.Add(immichAckInterval - time.Nanosecond)
			acks.now = func() time.Time { return now }
			err = acks.flushLocked(false)
			h.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			immichPhotoAccountUnchanged(t, h, checkpoint)
			h.mu.Lock()
			now = now.Add(time.Nanosecond)
			err = acks.flushLocked(false)
			h.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			daily := immichPhotoAccount(t, h)
			if daily.tx != checkpoint.tx+1 || len(daily.envelopes) != len(checkpoint.envelopes)+1 {
				t.Fatal("24-hour ACK checkpoint did not publish exactly one rare account transaction")
			}
			// RAM counters did not change since the preference transaction;
			// this transaction must still include its own complete snapshot.
			immichPhotoAssertAccountSnapshot(t, h, count, usage, prefSeq)
			wantResumed := immichMobileTestStream(t, h, token, "AssetsV2")
			// The preference-only gap still needs a snapshot checkpoint, but
			// none of the 205 acknowledged photos may be replayed.
			if len(wantResumed) != 2 || wantResumed[0].Type != "SyncAckV1" {
				t.Fatalf("daily ACK did not consume the full photo catalog: %+v", wantResumed)
			}
			before := immichStateTestRows(t, h.metadata)
			restarted := immichPhotoBlobOnlyRestart(t, h, !corpus)
			if restarted.userID() != h.userID() || restarted.search.Owner() != h.search.Owner() {
				t.Fatal("blob-only restart changed owner identity")
			}
			if after := immichStateTestRows(t, restarted.metadata); !reflect.DeepEqual(before, after) {
				t.Fatalf("blob-only restart changed durable rows: before=%v after=%v", before, after)
			}
			restored := restarted.metadata.(*immichState)
			if len(restored.assets) != count || restored.usage != usage || restored.sequence != prefSeq ||
				!reflect.DeepEqual(restored.assets, assets) {
				t.Fatal("blob-only restart lost photos, RAM-derived counters, or preference sequence")
			}
			if w := immichTestRequest(t, restarted, token, "GET", "users/me", nil, ""); w.Code != 200 {
				t.Fatalf("blob-only restart lost session: %d %s", w.Code, w.Body)
			}
			if events := immichMobileTestStream(t, restarted, token, "AssetsV2"); !reflect.DeepEqual(events, wantResumed) {
				t.Fatalf("blob-only restart lost daily ACK: %+v", events)
			}
			if !restarted.acks.lastFlush.Equal(now) {
				t.Fatal("blob-only restart changed persisted ACK checkpoint clock")
			}
			root := immichPhotoAccount(t, restarted)
			id := immichUploadedID(t, immichTestUpload(t, restarted, token, "uncheckpointed photo tail", nil), 201)
			if a := immichDedupAsset(t, restarted, id); a.Sequence != prefSeq+1 {
				t.Fatalf("next upload sequence %d, want preference gap + 1 (%d)", a.Sequence, prefSeq+1)
			}
			immichPhotoAccountUnchanged(t, restarted, root)
			// A complete index can recover a photo tail ahead of the last rare
			// account snapshot. The root is a floor, not an exact catalog size.
			tail := immichPhotoBlobOnlyRestart(t, restarted, corpus)
			if seq, err := tail.mobileSequence(); err != nil || seq != prefSeq+1 {
				t.Fatalf("photo tail did not advance recovered global sequence: %d, %v", seq, err)
			}
			if a := immichDedupAsset(t, tail, id); a.Sequence != prefSeq+1 {
				t.Fatal("photo tail changed receipt after native rebuild")
			}
			immichPhotoAccountUnchanged(t, tail, root)
			next := immichUploadedID(t, immichTestUpload(t, tail, token, "photo after rebuilt tail", nil), 201)
			if a := immichDedupAsset(t, tail, next); a.Sequence != prefSeq+2 {
				t.Fatalf("next upload reused the rebuilt photo tail sequence: %d", a.Sequence)
			}
			immichPhotoAccountUnchanged(t, tail, root)
		})
	}
}

func TestImmichPhotoStateConcurrentDistinctAndDuplicateUploads(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, corpus)
			token := immichTestLogin(t, h)
			account := immichPhotoAccount(t, h)
			const distinct, copies = 24, 3
			type result struct {
				number int
				w      *httptest.ResponseRecorder
			}
			results := make(chan result, distinct*copies)
			var wg sync.WaitGroup
			for n := range distinct * copies {
				number := n % distinct
				body, contentType := immichUploadBody(t, fmt.Sprintf("concurrent original %d", number), nil)
				wg.Add(1)
				go func() {
					defer wg.Done()
					results <- result{number, immichTestRequest(t, h, token, "POST", "assets", body, contentType)}
				}()
			}
			wg.Wait()
			close(results)
			created := make(map[int]int)
			ids := make(map[int]string)
			for result := range results {
				if result.w.Code != 201 && result.w.Code != 200 {
					t.Fatalf("concurrent upload: %d %s", result.w.Code, result.w.Body)
				}
				id := immichUploadedID(t, result.w, result.w.Code)
				want := immichAssetID(h.search.Owner(), blob.RefFromString(fmt.Sprintf("concurrent original %d", result.number)))
				if id != want || (ids[result.number] != "" && ids[result.number] != id) {
					t.Fatalf("concurrent content received unstable IDs: got %q want %q", id, want)
				}
				ids[result.number] = id
				if result.w.Code == 201 {
					created[result.number]++
				}
			}
			sequences := make(map[uint64]bool)
			var usage int64
			for n, id := range ids {
				if created[n] != 1 {
					t.Fatalf("content %d created %d times, want once", n, created[n])
				}
				a := *immichDedupAsset(t, h, id)
				if a.Sequence == 0 || a.Sequence > distinct || sequences[a.Sequence] {
					t.Fatalf("duplicate or invalid concurrent sequence: %d", a.Sequence)
				}
				sequences[a.Sequence] = true
				usage += a.Size
				immichPhotoAssertPublication(t, h, a)
			}
			s := h.metadata.(*immichState)
			if len(ids) != distinct || len(s.assets) != distinct || s.sequence != distinct || s.usage != usage {
				t.Fatalf("concurrent uploads changed catalog size/sequence/usage: %+v", s)
			}
			immichPhotoAccountUnchanged(t, h, account)
			restarted := immichPhotoBlobOnlyRestart(t, h, !corpus)
			before := immichDedupPublished(t, restarted)
			for n, id := range ids {
				got := immichUploadedID(t, immichTestUpload(t, restarted, token, fmt.Sprintf("concurrent original %d", n), immichDedupChangedFields()), 200)
				if got != id {
					t.Fatalf("concurrent receipt changed after restart: %q -> %q", id, got)
				}
			}
			immichDedupAssertPublished(t, restarted, before)
			immichPhotoAccountUnchanged(t, restarted, account)
		})
	}
}

func TestImmichPhotoStateClockRollbackAfterRestart(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, corpus)
			s := h.metadata.(*immichState)
			account := immichPhotoAccount(t, h)
			future := time.Now().UTC().Add(72 * time.Hour)
			s.lastDate = future
			first := immichStateTestAsset(t, h, 1)
			b := s.BeginBatch()
			immichStateTestSetAsset(t, b, first)
			if err := s.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			firstDate := s.lastDate
			if !firstDate.After(future) {
				t.Fatal("photo publication did not advance future claim-date floor")
			}
			restarted := immichPhotoBlobOnlyRestart(t, h, !corpus)
			r := restarted.metadata.(*immichState)
			if r.lastDate.Before(firstDate) {
				t.Fatal("restart lost maximum photo claim date")
			}
			second := immichStateTestAsset(t, restarted, 2)
			b = r.BeginBatch()
			immichStateTestSetAsset(t, b, second)
			if err := r.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			if !r.lastDate.After(firstDate) {
				t.Fatal("photo claim date went backwards after restart/clock rollback")
			}
			immichPhotoAccountUnchanged(t, restarted, account)
		})
	}
}

// A false-success backend must not fool publication verification. In
// partial-index mode the association marker survives but the record child does
// not, so confirming only the first operation is insufficient.
type immichPhotoPartialPublication struct {
	*immichTestStorage
	mode         string
	falseSuccess bool
	ref          blob.Ref
	text         string
	deleted      map[string]string
}

func (s *immichPhotoPartialPublication) ReceiveBlob(ctx context.Context, ref blob.Ref, src io.Reader) (blob.SizedRef, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return blob.SizedRef{}, err
	}
	var meta struct{ CamliType string }
	if json.Unmarshal(data, &meta) != nil || meta.CamliType != "claim" {
		return s.immichTestStorage.ReceiveBlob(ctx, ref, bytes.NewReader(data))
	}
	s.ref, s.text = ref, string(data)
	switch s.mode {
	case "index-only":
		_, err = s.index.ReceiveBlob(ctx, ref, bytes.NewReader(data))
	case "primary-only":
		_, err = s.Fetcher.ReceiveBlob(ctx, ref, bytes.NewReader(data))
	case "partial-index":
		_, err = s.immichTestStorage.ReceiveBlob(ctx, ref, bytes.NewReader(data))
		if err == nil {
			kv := s.index.Storage()
			it := kv.Find("claim|", "claim}")
			s.deleted = make(map[string]string)
			for it.Next() {
				if strings.Contains(it.Key(), ref.String()) && strings.Contains(it.Value(), "|"+immichRecordAttr+"|") {
					s.deleted[it.Key()] = it.Value()
				}
			}
			err = it.Close()
			for key := range s.deleted {
				if err == nil {
					err = kv.Delete(key)
				}
			}
		}
	default:
		return blob.SizedRef{}, fmt.Errorf("unknown fault mode %q", s.mode)
	}
	if err != nil {
		return blob.SizedRef{}, err
	}
	if s.falseSuccess {
		return blob.SizedRef{Ref: ref, Size: uint32(len(data))}, nil
	}
	return blob.SizedRef{}, errors.New("injected partial photo publication")
}

func immichPhotoRefreshCorpus(t *testing.T, h *Handler, corpus bool) {
	t.Helper()
	if corpus {
		c, err := h.search.Index().(*index.Index).KeepInMemory()
		if err != nil {
			t.Fatal(err)
		}
		h.search.SetCorpus(c)
	}
}

func TestImmichPhotoStatePartialPublicationFailsClosedAndRepairs(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		for _, mode := range []string{"index-only", "primary-only", "partial-index"} {
			for _, falseSuccess := range []bool{false, true} {
				t.Run(fmt.Sprintf("restartCorpus=%t/mode=%s/falseSuccess=%t", corpus, mode, falseSuccess), func(t *testing.T) {
					// Damage a disk index, not a stale live corpus. Startup below
					// constructs the optional corpus from the actual damaged rows.
					h := newImmichTestHandlerWithCorpus(t, false)
					s := h.metadata.(*immichState)
					a := immichStateTestAsset(t, h, 1)
					before := immichStateTestRows(t, s.view)
					account := immichPhotoAccount(t, h)
					primary := h.storage.(*immichTestStorage)
					fault := &immichPhotoPartialPublication{
						immichTestStorage: primary, mode: mode, falseSuccess: falseSuccess,
					}
					h.storage = fault
					b := s.BeginBatch()
					immichStateTestSetAsset(t, b, a)
					b.Set("sequence", "1") // Normal HTTP upload's derived mutations.
					b.Set("usage", strconv.FormatInt(a.Size, 10))
					if err := s.CommitBatch(b); err == nil {
						t.Fatal("partial photo publication was acknowledged")
					}
					h.storage = primary
					if !fault.ref.Valid() || (mode == "partial-index" && len(fault.deleted) != 1) {
						t.Fatalf("fault was not exercised: ref=%v deleted=%v", fault.ref, fault.deleted)
					}
					if after := immichStateTestRows(t, s.view); !reflect.DeepEqual(before, after) {
						t.Fatal("ambiguous photo publication changed local view")
					}
					immichPhotoAccountUnchanged(t, h, account)
					for _, key := range []string{"user|id", "sequence", "asset|" + a.ID, "whole|" + a.WholeRef.String()} {
						if _, err := s.Get(key); err == nil || errors.Is(err, sorted.ErrNotFound) {
							t.Fatalf("poisoned photo state Get(%q) = %v", key, err)
						}
					}
					it := s.Find("", "")
					if it.Next() || it.Close() == nil {
						t.Fatal("ambiguous photo publication exposed stale iterator")
					}
					if err := s.Set("session|blocked", "blocked"); err == nil {
						t.Fatal("ambiguous photo publication accepted another write")
					}
					immichPhotoRefreshCorpus(t, h, corpus)
					reopened, err := newImmichState(h)
					if mode == "primary-only" {
						// No indexed child reveals this uncheckpointed tail.
						// A complete index is a prerequisite, not something the
						// account root can prove without per-upload anchors.
						if err == nil {
							if _, err := reopened.Get("asset|" + a.ID); !errors.Is(err, sorted.ErrNotFound) {
								t.Fatalf("unindexed tail exposed an invented receipt: %v", err)
							}
							reopened.Close()
						}
					} else if err == nil {
						reopened.Close()
						t.Fatal("restart accepted a detectable partial photo publication")
					}
					if mode == "partial-index" {
						for key, value := range fault.deleted {
							if err := primary.index.Storage().Set(key, value); err != nil {
								t.Fatal(err)
							}
						}
					} else if _, err := blobserver.Receive(context.Background(), primary, fault.ref, strings.NewReader(fault.text)); err != nil {
						t.Fatal(err)
					}
					immichPhotoRefreshCorpus(t, h, corpus)
					repaired, err := newImmichState(h)
					if err != nil {
						t.Fatal(err)
					}
					defer repaired.Close()
					if id, err := repaired.Get("whole|" + a.WholeRef.String()); err != nil || id != a.ID {
						t.Fatalf("exact primary/index repair lost receipt: %q, %v", id, err)
					}
					if seq, err := repaired.Get("sequence"); err != nil || seq != "1" {
						t.Fatalf("repair lost photo-derived sequence: %q, %v", seq, err)
					}
					immichPhotoAccountUnchanged(t, h, account)
				})
			}
		}
	}
}

func immichPhotoDeleteIndexRows(t *testing.T, h *Handler, matches func(string, string) bool) int {
	t.Helper()
	kv := h.search.Index().(*index.Index).Storage()
	it := kv.Find("", "")
	var keys []string
	for it.Next() {
		if matches(it.Key(), it.Value()) {
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
	if len(keys) == 0 {
		t.Fatal("index-damage fixture did not remove any rows")
	}
	return len(keys)
}

func TestImmichPhotoStateMissingCheckpointedPrefixCannotBeMaskedByTail(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, false)
			first := immichStateTestAsset(t, h, 1)
			b := h.metadata.BeginBatch()
			immichStateTestSetAsset(t, b, first)
			if err := h.metadata.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			if err := h.metadata.Set("session|prefix-checkpoint", "checkpoint"); err != nil {
				t.Fatal(err)
			}
			tail := immichStateTestAsset(t, h, 2)
			if first.Size != tail.Size {
				t.Fatal("fixture requires equal-sized photos to mask total count and usage")
			}
			b = h.metadata.BeginBatch()
			immichStateTestSetAsset(t, b, tail)
			if err := h.metadata.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			immichPhotoDeleteIndexRows(t, h, func(key, _ string) bool {
				return strings.Contains(key, first.PermanodeRef.String())
			})
			immichPhotoRefreshCorpus(t, h, corpus)
			if reopened, err := newImmichState(h); err == nil {
				reopened.Close()
				t.Fatal("higher-sequence tail masked missing checkpointed prefix with equal total count/usage")
			}
		})
	}
}

func TestImmichPhotoStateCheckpointedPrefixUsageMustMatchExactly(t *testing.T) {
	for _, delta := range []int64{-1, 1} {
		t.Run(fmt.Sprintf("usageDelta=%d", delta), func(t *testing.T) {
			h := newImmichTestHandler(t)
			a := immichStateTestAsset(t, h, 1)
			b := h.metadata.BeginBatch()
			immichStateTestSetAsset(t, b, a)
			if err := h.metadata.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			if err := h.metadata.Set("session|usage-checkpoint", "checkpoint"); err != nil {
				t.Fatal(err)
			}
			// Emulate an independently signed inconsistent native record.
			// Count and sequence still match; both under- and over-counting
			// usage must violate the exact checkpointed prefix.
			a.Size += delta
			data, err := json.Marshal(a)
			if err != nil {
				t.Fatal(err)
			}
			ref := blob.RefFromBytes(data)
			if _, err := blobserver.Receive(context.Background(), h.storage, ref, bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			s := h.metadata.(*immichState)
			if _, err := h.storeSigned(context.Background(), schema.NewSetAttributeClaim(a.PermanodeRef, immichRecordAttr, ref.String()), s.lastDate.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if reopened, err := newImmichState(h); err == nil {
				reopened.Close()
				t.Fatal("rebuild accepted inconsistent checkpointed usage with unchanged prefix count and sequence")
			}
		})
	}
}

func TestImmichPhotoStateMissingIndexedReceiptChildFailsClosed(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		for _, attr := range []string{immichStateAttr, immichAssetIDAttr, immichChecksumAttr, immichRecordAttr} {
			t.Run(fmt.Sprintf("corpus=%t/child=%s", corpus, attr), func(t *testing.T) {
				h := newImmichTestHandlerWithCorpus(t, false)
				a := immichStateTestAsset(t, h, 1)
				b := h.metadata.BeginBatch()
				immichStateTestSetAsset(t, b, a)
				if err := h.metadata.CommitBatch(b); err != nil {
					t.Fatal(err)
				}
				immichPhotoDeleteIndexRows(t, h, func(key, value string) bool {
					return strings.HasPrefix(key, "claim|"+a.PermanodeRef.String()+"|") &&
						strings.Contains(value, "|"+attr+"|")
				})
				immichPhotoRefreshCorpus(t, h, corpus)
				if reopened, err := newImmichState(h); err == nil {
					reopened.Close()
					t.Fatalf("partial receipt with %s child missing was hidden or accepted", attr)
				}
			})
		}
	}
}

func TestImmichPhotoStateLegacyMixedEnvelopeLostAccountChildren(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, false)
			a := immichStateTestAsset(t, h, 1)
			s := h.metadata.(*immichState)
			before := immichPhotoAccount(t, h)
			b := s.BeginBatch()
			immichStateTestSetAsset(t, b, a)
			b.Set("session|mixed-envelope", "durable")
			if err := s.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			if s.tx != before.tx+1 {
				t.Fatal("mixed account/photo batch did not advance account history")
			}
			head := s.head
			claims, err := s.claims(context.Background(), a.PermanodeRef)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, cl := range claims {
				found = found || (cl.Attr == immichStateAttr && cl.BlobRef == head)
			}
			if !found {
				t.Fatal("fixture is not a single legacy cross-PN envelope")
			}
			immichPhotoDeleteIndexRows(t, h, func(key, _ string) bool {
				return strings.HasPrefix(key, "claim|"+s.account.String()+"|") && strings.Contains(key, head.String())
			})
			immichPhotoRefreshCorpus(t, h, corpus)
			if reopened, err := newImmichState(h); err == nil {
				reopened.Close()
				t.Fatal("mixed legacy claim with all account child rows missing was mistaken for a new photo-only tail")
			}
		})
	}
}

func TestImmichPhotoStateSequenceOnlyGapSurvivesRestart(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	id := immichUploadedID(t, immichTestUpload(t, h, token, "photo before sequence-only gap", nil), 201)
	account := immichPhotoAccount(t, h)
	if err := h.metadata.Set("sequence", "17"); err != nil {
		t.Fatal(err)
	}
	gap := immichPhotoAccount(t, h)
	if gap.tx != account.tx+1 || immichPhotoDurableSequence(t, h) != 17 {
		t.Fatal("sequence-only gap was discarded as an upload-derived mutation")
	}
	restarted := immichPhotoBlobOnlyRestart(t, h, false)
	if seq, err := restarted.mobileSequence(); err != nil || seq != 17 {
		t.Fatalf("sequence-only gap not restored: %d, %v", seq, err)
	}
	if a := immichDedupAsset(t, restarted, id); a.Sequence != 1 {
		t.Fatal("sequence gap rewrote a photo's original sequence")
	}
	next := immichUploadedID(t, immichTestUpload(t, restarted, token, "photo after sequence-only gap", nil), 201)
	if a := immichDedupAsset(t, restarted, next); a.Sequence != 18 {
		t.Fatalf("upload reused preference-only sequence: %d", a.Sequence)
	}
	immichPhotoAccountUnchanged(t, restarted, gap)
}

func TestImmichPhotoStateInvalidDurableAckFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, entity, ack string
	}{
		{"malformed", "AssetV2", "not an ACK"},
		{"type-key-mismatch", "AssetV2", "UserV1|1"},
		{"asset-ahead", "AssetV2", "AssetV2|2"},
		{"partial-metadata-ahead", "AssetMetadataV1", "AssetMetadataV1|2|0"},
		{"complete-ahead", "SyncCompleteV1", "SyncCompleteV1|2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newImmichTestHandler(t)
			token := immichTestLogin(t, h)
			immichUploadedID(t, immichTestUpload(t, h, token, "last committed photo before invalid ACK", nil), 201)
			request := httptest.NewRequest("GET", h.prefix+"sync/ack", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			// Real sessions include a "|" before the token hash. Splitting at
			// the first pipe would reject every healthy native HTTP checkpoint.
			session := h.sessionKey(request)
			if !strings.HasPrefix(session, "session|") {
				t.Fatalf("test lacks a real hashed session key: %q", session)
			}
			key := immichAckPrefix + session + "|" + tc.entity
			if err := h.metadata.Set(key, tc.ack); err != nil {
				// Rejecting an invalid ACK before it is published is also safe.
				return
			}
			if reopened, err := newImmichState(h); err == nil {
				reopened.Close()
				t.Fatal("invalid/ahead durable ACK was accepted or promoted into allocator authority")
			}
		})
	}
}

func TestImmichPhotoStateSoftDeleteCannotRewindSequence(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		for _, target := range []string{"photo-permanode", "receipt-claim"} {
			t.Run(fmt.Sprintf("corpus=%t/target=%s", corpus, target), func(t *testing.T) {
				h := newImmichTestHandlerWithCorpus(t, corpus)
				token := immichTestLogin(t, h)
				immichUploadedID(t, immichTestUpload(t, h, token, "earlier photo before native deletion", nil), 201)
				id := immichUploadedID(t, immichTestUpload(t, h, token, "latest uncheckpointed photo before native deletion", nil), 201)
				a := immichDedupAsset(t, h, id)
				s := h.metadata.(*immichState)
				account := immichPhotoAccount(t, h)
				if immichPhotoDurableSequence(t, h) >= a.Sequence {
					t.Fatal("fixture must delete an uncheckpointed photo tail")
				}
				ref := a.PermanodeRef
				if target == "receipt-claim" {
					ref = blob.Ref{}
					claims, err := s.claims(context.Background(), a.PermanodeRef)
					if err != nil {
						t.Fatal(err)
					}
					for _, cl := range claims {
						if cl.Attr == immichRecordAttr {
							ref = cl.BlobRef
						}
					}
					if !ref.Valid() {
						t.Fatal("fixture has no receipt claim")
					}
				}
				if _, err := h.storeSigned(context.Background(), schema.NewDeleteClaim(ref), s.lastDate.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				reopened, err := newImmichState(h)
				if err != nil {
					return // Fail-closed is preferable to silently dropping receipts.
				}
				defer reopened.Close()
				recovered := reopened.(*immichState)
				if recovered.sequence < a.Sequence {
					t.Fatalf("native soft deletion rewound committed sequence %d to %d", a.Sequence, recovered.sequence)
				}
				// If native deletion is represented without failing startup, the
				// next upload still must not reuse its committed sequence.
				oldMetadata := h.metadata
				h.metadata = reopened
				defer func() { h.metadata = oldMetadata }()
				next := immichUploadedID(t, immichTestUpload(t, h, token, "upload after native soft deletion", nil), 201)
				if got := immichDedupAsset(t, h, next); got.Sequence <= a.Sequence {
					t.Fatalf("upload reused soft-deleted receipt sequence: %d", got.Sequence)
				}
				immichPhotoAccountUnchanged(t, h, account)
			})
		}
	}
}
