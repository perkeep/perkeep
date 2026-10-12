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

package server

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/sorted"
)

// Compare the actual signed blobs, not just adapter receipts: a second
// permanode or any new claim on an existing user object is a regression.
func immichDedupPublished(t *testing.T, h *ImmichHandler) map[string]string {
	t.Helper()
	sto := h.storage.(*immichTestStorage)
	out := make(map[string]string)
	for _, ref := range sto.BlobrefStrings() {
		text, ok := sto.BlobContents(blob.MustParse(ref))
		if !ok {
			t.Fatalf("missing stored blob %s", ref)
		}
		var b struct{ CamliType string }
		if json.Unmarshal([]byte(text), &b) == nil &&
			(b.CamliType == "permanode" || b.CamliType == "claim") {
			out[ref] = text
		}
	}
	return out
}

func immichDedupAssertPublished(t *testing.T, h *ImmichHandler, want map[string]string) {
	t.Helper()
	if got := immichDedupPublished(t, h); !reflect.DeepEqual(got, want) {
		t.Fatalf("signed blobs changed: got %d permanodes/claims, want unchanged %d", len(got), len(want))
	}
}

func immichDedupAsset(t *testing.T, h *ImmichHandler, id string) *immichAsset {
	t.Helper()
	a, err := h.asset(id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func immichDedupChangedFields() map[string]string {
	return map[string]string{
		"filename":       "renamed-on-phone.jpg",
		"fileCreatedAt":  "2024-06-07T08:09:10Z",
		"fileModifiedAt": "2024-08-09T10:11:12Z",
	}
}

func immichDedupResetMetadata(t *testing.T, h *ImmichHandler) {
	t.Helper()
	if err := h.metadata.Close(); err != nil {
		t.Fatal(err)
	}
	h.metadata = sorted.NewMemoryKeyValue()
	h.uid = ""
	if err := h.initIdentity(); err != nil {
		t.Fatal(err)
	}
}

// This deliberately does not use a planned permanode or any Immich receipt.
// Its file schema differs from the mobile upload in both filename and mtime.
func immichDedupNativePermanode(t *testing.T, h *ImmichHandler, original string) (pn, file blob.Ref) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2018, 2, 3, 4, 5, 6, 0, time.UTC)
	var err error
	file, err = schema.WriteFileFromReaderWithModTime(ctx, h.storage, "native-upload.png", at, strings.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	pn, err = h.storeSigned(ctx, schema.NewUnsignedPermanode(), at)
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range []*schema.Builder{
		schema.NewSetAttributeClaim(pn, "camliContent", file.String()),
		schema.NewSetAttributeClaim(pn, "title", "My hand-edited title"),
		schema.NewAddAttributeClaim(pn, "tag", "family"),
		schema.NewAddAttributeClaim(pn, "tag", "keep this tag"),
		schema.NewSetAttributeClaim(pn, "dateCreated", at.Format(time.RFC3339)),
	} {
		if _, err := h.storeSigned(ctx, claim, at); err != nil {
			t.Fatal(err)
		}
	}
	return pn, file
}

func TestImmichDedupNativePermanode(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, corpus)
			token := immichTestLogin(t, h)
			const original = "original bytes already uploaded through pk-put"
			pn, nativeFile := immichDedupNativePermanode(t, h, original)
			before := immichDedupPublished(t, h)
			id := immichUploadedID(t, immichTestUpload(t, h, token, original, nil), 200)
			a := immichDedupAsset(t, h, id)
			if a.PermanodeRef != pn || a.WholeRef != blob.RefFromString(original) || a.FileRef == nativeFile {
				t.Fatalf("receipt did not reuse native object with a different file schema: %+v", a)
			}
			if got := immichUploadedID(t, immichTestUpload(t, h, token, original, immichDedupChangedFields()), 200); got != id {
				t.Fatalf("retry ID = %s, want %s", got, id)
			}
			immichDedupAssertPublished(t, h, before)
			described, err := h.search.Describe(context.Background(), &search.DescribeRequest{BlobRef: pn})
			if err != nil {
				t.Fatal(err)
			}
			attrs := described.Meta[pn.String()].Permanode.Attr
			slices.Sort(attrs["tag"])
			if attrs.Get("camliContent") != nativeFile.String() || attrs.Get("title") != "My hand-edited title" ||
				!reflect.DeepEqual(attrs["tag"], []string{"family", "keep this tag"}) ||
				attrs.Get("dateCreated") != "2018-02-03T04:05:06Z" {
				t.Fatalf("native user attributes changed: %v", attrs)
			}
			w := immichTestRequest(t, h, token, "GET", "assets/"+id+"/original", nil, "")
			if w.Code != 200 || w.Body.String() != original {
				t.Fatalf("original = %d %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestImmichDedupMetadataLoss(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		for _, native := range []bool{false, true} {
			t.Run(fmt.Sprintf("corpus=%t/native=%t", corpus, native), func(t *testing.T) {
				h := newImmichTestHandlerWithCorpus(t, corpus)
				oldToken := immichTestLogin(t, h)
				const original = "an original whose Immich database will be lost"
				status := 201
				if native {
					immichDedupNativePermanode(t, h, original)
					status = 200
				}
				oldID := immichUploadedID(t, immichTestUpload(t, h, oldToken, original, nil), status)
				old := immichDedupAsset(t, h, oldID)
				before := immichDedupPublished(t, h)
				oldUser := h.userID()
				immichDedupResetMetadata(t, h)
				if h.userID() == oldUser {
					t.Fatal("metadata reset unexpectedly retained the old user identity")
				}
				if w := immichTestRequest(t, h, oldToken, "GET", "users/me", nil, ""); w.Code != 401 {
					t.Fatalf("old session survived metadata loss: %d %s", w.Code, w.Body)
				}
				token := immichTestLogin(t, h)
				id := immichUploadedID(t, immichTestUpload(t, h, token, original, immichDedupChangedFields()), 200)
				a := immichDedupAsset(t, h, id)
				if a.PermanodeRef != old.PermanodeRef || a.WholeRef != old.WholeRef || a.FileRef == old.FileRef {
					t.Fatalf("metadata loss duplicated or failed to rediscover object: old=%+v new=%+v", old, a)
				}
				if a.Sequence != 1 {
					t.Fatalf("new receipt sequence = %d, want 1", a.Sequence)
				}
				immichDedupAssertPublished(t, h, before)
			})
		}
	}
}

func TestImmichDedupPlannedPermanode(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, corpus)
			token := immichTestLogin(t, h)
			const original = "planned permanode identity uses original bytes alone"
			// This is the canonical pk-put --filenodes key and signing time.
			signed, err := schema.NewPlannedPermanode(blob.RefFromString(original).String()).
				SignAt(context.Background(), h.signer, time.Unix(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			want := blob.RefFromString(signed)
			id := immichUploadedID(t, immichTestUpload(t, h, token, original, nil), 201)
			if a := immichDedupAsset(t, h, id); a.PermanodeRef != want {
				t.Fatalf("permanode = %v, want canonical pk-put %v", a.PermanodeRef, want)
			}
			// Simulate an index that has not caught up, as well as lost receipts.
			// Ingestion still goes to the original synchronously indexed storage.
			empty := index.NewMemoryIndex()
			t.Cleanup(func() { empty.Close() })
			immichDedupSetSearch(t, h, empty, corpus)
			immichDedupResetMetadata(t, h)
			token = immichTestLogin(t, h)
			id = immichUploadedID(t, immichTestUpload(t, h, token, original, immichDedupChangedFields()), 201)
			if a := immichDedupAsset(t, h, id); a.PermanodeRef != want {
				t.Fatalf("query miss changed planned permanode: got %v, want %v", a.PermanodeRef, want)
			}
			var count int
			for ref, text := range immichDedupPublished(t, h) {
				var b struct{ CamliType string }
				if err := json.Unmarshal([]byte(text), &b); err != nil {
					t.Fatal(err)
				}
				if b.CamliType == "permanode" {
					count++
					if ref != want.String() || text != signed {
						t.Fatalf("noncanonical permanode %s: %s", ref, text)
					}
				}
			}
			if count != 1 {
				t.Fatalf("stored %d permanodes, want 1", count)
			}
		})
	}
}

func TestImmichDedupMetadataCommitRetry(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, corpus)
			token := immichTestLogin(t, h)
			const original = "original published before a receipt commit failed"
			metadata := h.metadata
			h.metadata = immichFailMetadata{metadata}
			w := immichTestUpload(t, h, token, original, nil)
			if w.Code != 500 {
				t.Fatalf("failed receipt commit = %d %s", w.Code, w.Body)
			}
			before := immichDedupPublished(t, h)
			var permanodes, claims int
			for _, text := range before {
				var b struct{ CamliType string }
				if err := json.Unmarshal([]byte(text), &b); err != nil {
					t.Fatal(err)
				}
				if b.CamliType == "permanode" {
					permanodes++
				} else {
					claims++
				}
			}
			if permanodes != 1 || claims == 0 {
				t.Fatalf("failed receipt commit published %d permanodes, %d claims; want 1 permanode with claims", permanodes, claims)
			}
			for _, key := range []string{"sequence", "usage", "whole|" + blob.RefFromString(original).String()} {
				if _, err := metadata.Get(key); !errors.Is(err, sorted.ErrNotFound) {
					t.Fatalf("failed receipt commit left %s: %v", key, err)
				}
			}
			h.metadata = metadata
			id := immichUploadedID(t, immichTestUpload(t, h, token, original, immichDedupChangedFields()), 200)
			a := immichDedupAsset(t, h, id)
			if _, ok := before[a.PermanodeRef.String()]; !ok || a.Sequence != 1 {
				t.Fatalf("retry did not reuse published object: %+v", a)
			}
			immichDedupAssertPublished(t, h, before)
		})
	}
}

func TestImmichDedupPreservesRepointedCanonicalPermanode(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		for _, detached := range []bool{false, true} {
			t.Run(fmt.Sprintf("corpus=%t/detached=%t", corpus, detached), func(t *testing.T) {
				h := newImmichTestHandlerWithCorpus(t, corpus)
				token := immichTestLogin(t, h)
				ctx := context.Background()
				const original = "the original bytes used as the canonical permanode key"
				whole := blob.RefFromString(original)
				pn, err := h.storeSigned(ctx, schema.NewPlannedPermanode(whole.String()), time.Unix(0, 0))
				if err != nil {
					t.Fatal(err)
				}
				at := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
				file, err := schema.WriteFileFromReaderWithModTime(ctx, h.storage, "before-edit.jpg", at, strings.NewReader(original))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := h.storeSigned(ctx, schema.NewSetAttributeClaim(pn, "camliContent", file.String()), at); err != nil {
					t.Fatal(err)
				}
				var edit *schema.Builder
				if detached {
					edit = schema.NewDelAttributeClaim(pn, "camliContent", "")
				} else {
					file, err = schema.WriteFileFromReaderWithModTime(ctx, h.storage, "replacement.jpg", at, strings.NewReader("user replacement, not the original"))
					if err != nil {
						t.Fatal(err)
					}
					edit = schema.NewSetAttributeClaim(pn, "camliContent", file.String())
				}
				if _, err := h.storeSigned(ctx, edit, at.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				if _, err := h.storeSigned(ctx, schema.NewSetAttributeClaim(pn, "title", "User changed this object"), at.Add(2*time.Hour)); err != nil {
					t.Fatal(err)
				}
				if got, err := h.findPermanode(ctx, whole); err != nil || got.Valid() {
					t.Fatalf("fixture still matches original: %v, %v", got, err)
				}
				before := immichDedupPublished(t, h)
				w := immichTestUpload(t, h, token, original, immichDedupChangedFields())
				if w.Code != 409 {
					t.Fatalf("upload clobbered a repointed canonical object: %d %s", w.Code, w.Body)
				}
				immichDedupAssertPublished(t, h, before)
				for _, prefix := range []string{"asset|", "whole|", "checksum|", "change|"} {
					it := h.metadata.Find(prefix, prefix+"~")
					found := it.Next()
					if err := it.Close(); err != nil {
						t.Fatal(err)
					}
					if found {
						t.Fatalf("conflicting upload published %s metadata", prefix)
					}
				}
				for _, key := range []string{"sequence", "usage"} {
					if _, err := h.metadata.Get(key); !errors.Is(err, sorted.ErrNotFound) {
						t.Fatalf("conflicting upload published %s: %v", key, err)
					}
				}
			})
		}
	}
}

func TestImmichDedupLegacyReceiptMigration(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, corpus)
			token := immichTestLogin(t, h)
			const original = "legacy receipt has only an Immich SHA-1 checksum"
			id := immichUploadedID(t, immichTestUpload(t, h, token, original, nil), 201)
			a := immichDedupAsset(t, h, id)
			pn, whole, seq := a.PermanodeRef, a.WholeRef, a.Sequence
			before := immichDedupPublished(t, h)
			immichDedupMakeLegacy(t, h, a)
			again := immichUploadedID(t, immichTestUpload(t, h, token, original, immichDedupChangedFields()), 200)
			if again != id {
				t.Fatalf("legacy migration changed asset ID: got %s, want %s", again, id)
			}
			migrated := immichDedupAsset(t, h, id)
			if migrated.WholeRef != whole || migrated.PermanodeRef != pn || migrated.Sequence != seq {
				t.Fatalf("incorrect migrated receipt: %+v", migrated)
			}
			if got, err := h.metadata.Get("whole|" + whole.String()); err != nil || got != id {
				t.Fatalf("migrated whole-file mapping = %q, %v; want %s", got, err, id)
			}
			if got, err := h.metadata.Get("sequence"); err != nil || got != "1" {
				t.Fatalf("migration allocated a sequence: %q, %v", got, err)
			}
			immichDedupAssertPublished(t, h, before)
		})
	}
}

func immichDedupMakeLegacy(t *testing.T, h *ImmichHandler, a *immichAsset) {
	t.Helper()
	if err := h.metadata.Delete("whole|" + a.WholeRef.String()); err != nil {
		t.Fatal(err)
	}
	a.WholeRef = blob.Ref{}
	immichDedupSaveAsset(t, h, a)
}

func immichDedupSaveAsset(t *testing.T, h *ImmichHandler, a *immichAsset) {
	t.Helper()
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.metadata.Set("asset|"+a.ID, string(data)); err != nil {
		t.Fatal(err)
	}
}

func TestImmichDedupChecksumCollision(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("corpus=%t/legacy=%t", corpus, legacy), func(t *testing.T) {
				h := newImmichTestHandlerWithCorpus(t, corpus)
				token := immichTestLogin(t, h)
				oldID := immichUploadedID(t, immichTestUpload(t, h, token, "unrelated original bytes", nil), 201)
				old := immichDedupAsset(t, h, oldID)
				oldPN := old.PermanodeRef
				const original = "different bytes must never be acknowledged by SHA-1 alone"
				// Model a SHA-1 collision without having to generate one. Both the
				// compatibility mapping and receipt claim this incoming checksum,
				// but their original bytes (and native wholeRef) are different.
				sum := sha1.Sum([]byte(original))
				old.Checksum = hex.EncodeToString(sum[:])
				if legacy {
					immichDedupMakeLegacy(t, h, old)
				} else {
					immichDedupSaveAsset(t, h, old)
				}
				if err := h.metadata.Set("checksum|"+old.Checksum, oldID); err != nil {
					t.Fatal(err)
				}
				id := immichUploadedID(t, immichTestUpload(t, h, token, original, nil), 201)
				a := immichDedupAsset(t, h, id)
				if id == oldID || a.PermanodeRef == oldPN || a.WholeRef != blob.RefFromString(original) || a.Sequence != 2 {
					t.Fatalf("SHA-1 collision falsely reused an unrelated asset: %+v", a)
				}
				if got := immichDedupAsset(t, h, oldID); got.PermanodeRef != oldPN {
					t.Fatalf("SHA-1 collision modified original receipt: %+v", got)
				}
				w := immichTestRequest(t, h, token, "GET", "assets/"+id+"/original", nil, "")
				if w.Code != 200 || w.Body.String() != original {
					t.Fatalf("collision original = %d %q", w.Code, w.Body.String())
				}
			})
		}
	}
}

func immichDedupSetSearch(t *testing.T, h *ImmichHandler, idx *index.Index, corpus bool) {
	t.Helper()
	owner := index.NewOwner(h.signer.KeyIDLong(), h.search.Owner())
	h.search = search.NewHandler(idx, owner)
	if corpus {
		c, err := idx.KeepInMemory()
		if err != nil {
			t.Fatal(err)
		}
		h.search.SetCorpus(c)
	}
}

// search.NewHandler requires a concrete *index.Index, so inject failures in
// its sorted backing store rather than replacing index.Interface.
type immichDedupFaultIndexStore struct {
	sorted.KeyValue
	getKey, findPrefix string
	hits               atomic.Int64
}

func (s *immichDedupFaultIndexStore) Get(key string) (string, error) {
	if s.getKey != "" && key == s.getKey {
		s.hits.Add(1)
		return "", errors.New("injected dedup query failure")
	}
	return s.KeyValue.Get(key)
}

func (s *immichDedupFaultIndexStore) Find(start, end string) sorted.Iterator {
	it := s.KeyValue.Find(start, end)
	if s.findPrefix != "" && strings.HasPrefix(start, s.findPrefix) {
		s.hits.Add(1)
		return immichDedupErrorIterator{it}
	}
	return it
}

type immichDedupErrorIterator struct{ sorted.Iterator }

func (it immichDedupErrorIterator) Next() bool { return false }
func (it immichDedupErrorIterator) Close() error {
	if err := it.Iterator.Close(); err != nil {
		return err
	}
	return errors.New("injected whole-file lookup failure")
}

func TestImmichDedupLookupFailureNeverAcknowledged(t *testing.T) {
	for _, mode := range []string{"whole-file", "whole-file-with-corpus", "query"} {
		t.Run(mode, func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, mode == "whole-file-with-corpus")
			token := immichTestLogin(t, h)
			const original = "native original while duplicate lookup is unavailable"
			pn, _ := immichDedupNativePermanode(t, h, original)
			before := immichDedupPublished(t, h)
			fault := &immichDedupFaultIndexStore{KeyValue: sorted.NewMemoryKeyValue()}
			// Copy the index, including native PN/file rows, before enabling
			// faults. The upload path itself keeps using healthy ingestion.
			it := h.storage.(*immichTestStorage).index.Storage().Find("", "")
			for it.Next() {
				if err := fault.Set(it.Key(), it.Value()); err != nil {
					t.Fatal(err)
				}
			}
			if err := it.Close(); err != nil {
				t.Fatal(err)
			}
			idx, err := index.New(fault)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { idx.Close() })
			immichDedupSetSearch(t, h, idx, mode == "whole-file-with-corpus")
			if mode == "query" {
				// Candidate enumeration works, but describing the native PN
				// within the actual search.Query fails.
				fault.getKey = "meta:" + pn.String()
			} else {
				fault.findPrefix = "wholetofile|"
			}
			if got, err := h.findPermanode(context.Background(), blob.RefFromString(original)); err == nil || got.Valid() {
				t.Fatalf("lookup failure = %v, %v; want no PN and an error", got, err)
			}
			w := immichTestUpload(t, h, token, original, nil)
			if w.Code != 500 {
				t.Fatalf("failed duplicate lookup = %d %s", w.Code, w.Body)
			}
			if fault.hits.Load() == 0 {
				t.Fatal("index fault was not exercised")
			}
			for _, key := range []string{"sequence", "whole|" + blob.RefFromString(original).String()} {
				if _, err := h.metadata.Get(key); !errors.Is(err, sorted.ErrNotFound) {
					t.Fatalf("failed lookup published %s: %v", key, err)
				}
			}
			immichDedupAssertPublished(t, h, before)
		})
	}
}

func TestImmichDedupRejectUploadedDerivatives(t *testing.T) {
	for _, part := range []string{"thumbnailData", "previewData"} {
		for _, first := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/first=%t", part, first), func(t *testing.T) {
				h := newImmichTestHandler(t)
				token := immichTestLogin(t, h)
				sto := h.storage.(*immichTestStorage)
				before := sto.BlobrefStrings()
				body := new(bytes.Buffer)
				mw := multipart.NewWriter(body)
				parts := []string{"assetData", part}
				if first {
					parts[0], parts[1] = parts[1], parts[0]
				}
				for _, name := range parts {
					p, err := mw.CreateFormFile(name, name+".jpg")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := io.WriteString(p, "bytes for "+name); err != nil {
						t.Fatal(err)
					}
				}
				for _, name := range []string{"fileCreatedAt", "fileModifiedAt"} {
					if err := mw.WriteField(name, "2025-01-02T03:04:05Z"); err != nil {
						t.Fatal(err)
					}
				}
				if err := mw.Close(); err != nil {
					t.Fatal(err)
				}
				w := immichTestRequest(t, h, token, "POST", "assets", body, mw.FormDataContentType())
				if w.Code != 400 {
					t.Fatalf("uploaded derivative accepted: %d %s", w.Code, w.Body)
				}
				if got := sto.BlobrefStrings(); !reflect.DeepEqual(got, before) {
					t.Fatalf("rejected upload stored blobs: before=%v after=%v", before, got)
				}
				if _, err := h.metadata.Get("sequence"); !errors.Is(err, sorted.ErrNotFound) {
					t.Fatalf("rejected upload published a receipt: %v", err)
				}
			})
		}
	}
}
