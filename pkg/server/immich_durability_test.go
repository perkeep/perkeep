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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/schema"
)

func immichDurabilityPublished(t *testing.T, storage *immichTestStorage) map[string]string {
	t.Helper()
	published := make(map[string]string)
	for _, ref := range storage.BlobrefStrings() {
		text, _ := storage.BlobContents(blob.MustParse(ref))
		var meta struct{ CamliType string }
		if json.Unmarshal([]byte(text), &meta) == nil &&
			(meta.CamliType == "permanode" || meta.CamliType == "claim") {
			published[ref] = text
		}
	}
	return published
}

func immichDurabilityNoReceipt(t *testing.T, h *ImmichHandler) {
	t.Helper()
	for _, prefix := range []string{"asset|", "whole|", "checksum|", "change|", "sequence", "usage"} {
		it := h.metadata.Find(prefix, prefix+"~")
		found := it.Next()
		if err := it.Close(); err != nil {
			t.Fatal(err)
		}
		if found {
			t.Errorf("failed native verification published %s metadata", prefix)
		}
	}
}

// Model the partial success allowed by the normal synchronous replica: the
// index accepts the multi-claim, but primary storage fails to persist it.
type immichDurabilityPartialStorage struct {
	*immichTestStorage
	missing blob.Ref
	text    string
}

func (s *immichDurabilityPartialStorage) ReceiveBlob(ctx context.Context, br blob.Ref, src io.Reader) (blob.SizedRef, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return blob.SizedRef{}, err
	}
	var meta struct{ CamliType string }
	if json.Unmarshal(data, &meta) == nil && meta.CamliType == "claim" && !s.missing.Valid() {
		if _, err := s.index.ReceiveBlob(ctx, br, bytes.NewReader(data)); err != nil {
			return blob.SizedRef{}, err
		}
		s.missing, s.text = br, string(data)
		return blob.SizedRef{}, errors.New("primary storage failed after index succeeded")
	}
	return s.immichTestStorage.ReceiveBlob(ctx, br, bytes.NewReader(data))
}

func TestImmichNativeClaimPartialReplicaRetry(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, corpus)
			token := immichTestLogin(t, h)
			primary := h.storage.(*immichTestStorage)
			partial := &immichDurabilityPartialStorage{immichTestStorage: primary}
			h.storage = partial
			const original = "multi-claim indexed, primary write failed"
			if w := immichTestUpload(t, h, token, original, nil); w.Code != 500 {
				t.Fatalf("partial replica upload = %d %s", w.Code, w.Body)
			}
			if !partial.missing.Valid() {
				t.Fatal("claim publication fault was not exercised")
			}
			if _, ok := primary.BlobContents(partial.missing); ok {
				t.Fatal("fixture unexpectedly stored the indexed claim")
			}
			h.storage = primary
			before := immichDurabilityPublished(t, primary)
			if len(before) != 1 {
				t.Fatalf("primary has %d signed blobs; want only the permanode", len(before))
			}
			whole := blob.RefFromString(original)
			if pn, err := h.findPermanode(context.Background(), whole); err == nil || pn.Valid() {
				t.Fatalf("index-only claim was accepted: %v, %v", pn, err)
			}
			if w := immichTestUpload(t, h, token, original, nil); w.Code != 500 {
				t.Fatalf("retry acknowledged missing native claim = %d %s", w.Code, w.Body)
			}
			if after := immichDurabilityPublished(t, primary); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed retry created or modified a native object: %v", after)
			}
			immichDurabilityNoReceipt(t, h)

			// An exact repair of the missing native envelope restores safe
			// reuse; the retry must not manufacture replacement attributes.
			if _, err := blobserver.Receive(context.Background(), primary, partial.missing, strings.NewReader(partial.text)); err != nil {
				t.Fatal(err)
			}
			repaired := immichDurabilityPublished(t, primary)
			id := immichUploadedID(t, immichTestUpload(t, h, token, original, nil), 200)
			a, err := h.asset(id)
			if err != nil || !a.PermanodeRef.Valid() {
				t.Fatalf("repaired receipt = %v, %v", a, err)
			}
			if _, ok := before[a.PermanodeRef.String()]; !ok {
				t.Fatalf("repair created a different permanode: %v", a.PermanodeRef)
			}
			if after := immichDurabilityPublished(t, primary); !reflect.DeepEqual(after, repaired) {
				t.Fatal("healthy reuse rewrote native attributes")
			}
		})
	}
}

func immichDurabilityNativeFixture(t *testing.T, h *ImmichHandler, original string) (pn, file, oldClaim, currentClaim, chunk blob.Ref) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2019, 1, 2, 3, 4, 5, 0, time.UTC)
	var err error
	file, err = schema.WriteFileFromReaderWithModTime(ctx, h.storage, "native-original.jpg", at, strings.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	pn, err = h.storeSigned(ctx, schema.NewUnsignedPermanode(), at)
	if err != nil {
		t.Fatal(err)
	}
	for i, target := range []*blob.Ref{&oldClaim, &currentClaim} {
		*target, err = h.storeSigned(ctx, schema.NewSetAttributeClaim(pn, "camliContent", file.String()), at.Add(time.Duration(i)*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
	}
	fr, err := schema.NewFileReader(ctx, h.storage, file)
	if err != nil {
		t.Fatal(err)
	}
	defer fr.Close()
	if err := fr.ForeachChunk(ctx, func(_ []blob.Ref, part schema.BytesPart) error {
		chunk = part.BlobRef
		return nil
	}); err != nil || !chunk.Valid() {
		t.Fatalf("native chunk = %v, %v", chunk, err)
	}
	return
}

// Keep one primary read unavailable even if receiving a retry repairs its
// chunks. Successful writes alone cannot justify acknowledging unreadable data.
type immichDurabilityReadStorage struct {
	*immichTestStorage
	unreadable blob.Ref
	statErr    error
}

func (s immichDurabilityReadStorage) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	if br == s.unreadable {
		return nil, 0, os.ErrNotExist
	}
	return s.immichTestStorage.Fetch(ctx, br)
}

func (s immichDurabilityReadStorage) StatBlobs(ctx context.Context, refs []blob.Ref, fn func(blob.SizedRef) error) error {
	if s.statErr != nil {
		return s.statErr
	}
	return s.immichTestStorage.StatBlobs(ctx, refs, fn)
}

func TestImmichNativeReuseRequiresStoredData(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		for _, missing := range []string{"permanode", "historical-content-claim", "current-content-claim", "file-schema", "chunk"} {
			t.Run(fmt.Sprintf("corpus=%t/missing=%s", corpus, missing), func(t *testing.T) {
				h := newImmichTestHandlerWithCorpus(t, corpus)
				token := immichTestLogin(t, h)
				const original = "native original with missing primary storage data"
				pn, file, oldClaim, currentClaim, chunk := immichDurabilityNativeFixture(t, h, original)
				primary := h.storage.(*immichTestStorage)
				ref := map[string]blob.Ref{
					"permanode": pn, "historical-content-claim": oldClaim,
					"current-content-claim": currentClaim, "file-schema": file, "chunk": chunk,
				}[missing]
				if err := primary.RemoveBlobs(context.Background(), []blob.Ref{ref}); err != nil {
					t.Fatal(err)
				}
				before := immichDurabilityPublished(t, primary)
				if got, err := h.findPermanode(context.Background(), blob.RefFromString(original)); err == nil || got.Valid() {
					t.Fatalf("missing %s accepted: %v, %v", missing, got, err)
				}
				if missing == "chunk" {
					h.storage = immichDurabilityReadStorage{immichTestStorage: primary, unreadable: chunk}
				}
				if w := immichTestUpload(t, h, token, original, nil); w.Code != 500 {
					t.Fatalf("missing %s upload = %d %s", missing, w.Code, w.Body)
				}
				if after := immichDurabilityPublished(t, primary); !reflect.DeepEqual(after, before) {
					t.Fatalf("verification failure changed signed objects: %v", after)
				}
				immichDurabilityNoReceipt(t, h)
			})
		}
	}
}

func TestImmichNativeReuseChecksActualDigest(t *testing.T) {
	for _, corpus := range []bool{false, true} {
		t.Run(fmt.Sprintf("corpus=%t", corpus), func(t *testing.T) {
			h := newImmichTestHandlerWithCorpus(t, corpus)
			token := immichTestLogin(t, h)
			_, file, _, _, _ := immichDurabilityNativeFixture(t, h, "actual native bytes")
			primary := h.storage.(*immichTestStorage)
			const uploaded = "different uploaded bytes"
			whole := blob.RefFromString(uploaded)
			// ExistingFileSchemas is deliberately only an index hint. A
			// stale/wrong mapping must not override actual primary bytes.
			if err := primary.index.Storage().Set("wholetofile|"+whole.String()+"|"+file.String(), "1"); err != nil {
				t.Fatal(err)
			}
			before := immichDurabilityPublished(t, primary)
			if pn, err := h.findPermanode(context.Background(), whole); err == nil || pn.Valid() {
				t.Fatalf("wrong native digest was accepted: %v, %v", pn, err)
			}
			if w := immichTestUpload(t, h, token, uploaded, nil); w.Code != 500 {
				t.Fatalf("wrong native digest upload = %d %s", w.Code, w.Body)
			}
			if after := immichDurabilityPublished(t, primary); !reflect.DeepEqual(after, before) {
				t.Fatal("digest failure created or modified native objects")
			}
			immichDurabilityNoReceipt(t, h)
		})
	}
}

func TestImmichNativeReuseStatFailure(t *testing.T) {
	h := newImmichTestHandler(t)
	const original = "native original during stat failure"
	immichDurabilityNativeFixture(t, h, original)
	sentinel := errors.New("primary stat storage failure")
	h.storage = immichDurabilityReadStorage{immichTestStorage: h.storage.(*immichTestStorage), statErr: sentinel}
	if pn, err := h.findPermanode(context.Background(), blob.RefFromString(original)); !errors.Is(err, sentinel) || pn.Valid() {
		t.Fatalf("primary stat failure = %v, %v; want error %v", pn, err, sentinel)
	}
}
