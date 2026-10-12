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

package immich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"
	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

// Start on the old memory-backed API view, rather than the default native test
// handler. Otherwise the fixture would already have published a native UID.
func newImmichLegacyTestHandler(t *testing.T) *Handler {
	t.Helper()
	signer, pub := newSigner(t)
	idx := index.NewMemoryIndex()
	idx.Logger = test.NewLogger(t, "")
	sto := &immichTestStorage{Fetcher: new(test.Fetcher), index: idx}
	idx.InitBlobSource(sto)
	corpus, err := idx.KeepInMemory()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blobserver.Receive(context.Background(), sto, blob.RefFromString(pub), strings.NewReader(pub)); err != nil {
		t.Fatal(err)
	}
	h := &Handler{
		storage: sto, signer: signer,
		search:   search.NewHandler(idx, index.NewOwner(signer.KeyIDLong(), blob.RefFromString(pub))),
		metadata: sorted.NewMemoryKeyValue(),
		email:    "photo@example.com", password: "secret", prefix: "/immich/api/", maxUploadBytes: 32 << 30,
	}
	h.search.SetCorpus(corpus)
	if err := h.initIdentity(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
		if err := idx.Close(); err != nil {
			t.Error(err)
		}
	})
	return h
}

func immichMigrationRows(t *testing.T, kv sorted.KeyValue) map[string]string {
	t.Helper()
	rows := make(map[string]string)
	it := kv.Find("", "")
	for it.Next() {
		rows[it.Key()] = it.Value()
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func immichMigrationExport(t *testing.T, rows map[string]string) (string, jsonconfig.Obj) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.leveldb")
	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	batch := new(leveldb.Batch)
	for key, value := range rows {
		batch.Put([]byte(key), []byte(value))
	}
	if err := db.Write(batch, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path, jsonconfig.Obj{"type": "leveldb", "file": path}
}

func immichMigrationFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	files, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string)
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(path, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[file.Name()] = string(data)
	}
	return out
}

func immichMigrationOpen(t *testing.T, h *Handler) {
	t.Helper()
	immichTestDiscardAcks(t, h)
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.metadata.Close(); err != nil {
		t.Fatal(err)
	}
	h.uid = ""
	var err error
	h.metadata, err = newImmichStateForMigration(h)
	if err != nil {
		t.Fatal(err)
	}
}

func TestImmichLegacyMigration(t *testing.T) {
	h := newImmichLegacyTestHandler(t)
	uid := h.userID()
	token := immichTestLogin(t, h)
	video := immichUploadedID(t, immichTestUpload(t, h, token, "legacy video", map[string]string{
		"filename": "motion.mov", "visibility": "hidden",
	}), 201)
	const metadata = `[{"key":"test","value":{"x":1},"extra":"accepted by upload"}]`
	still := immichUploadedID(t, immichTestUpload(t, h, token, "legacy still", map[string]string{
		"livePhotoVideoId": video, "isFavorite": "true", "metadata": metadata,
	}), 201)
	if w := immichTestRequest(t, h, token, "GET", "users/me", nil, ""); w.Code != 200 {
		t.Fatalf("profile: %d %s", w.Code, w.Body)
	}
	if w := immichTestRequest(t, h, token, "PUT", "users/me/preferences",
		strings.NewReader(`{"albums":{"defaultAssetOrder":"asc"}}`), "application/json"); w.Code != 200 {
		t.Fatalf("preferences: %d %s", w.Code, w.Body)
	}
	head, err := h.mobileSequence()
	if err != nil || head != 3 {
		t.Fatalf("preference sequence = %d, %v", head, err)
	}
	ack := fmt.Sprintf("AssetV1|%d", head)
	// This fixture represents the old adapter, which persisted every ACK.
	// A modern HTTP ACK remains in RAM, and forcing its checkpoint would add
	// a modern timestamp key that is deliberately not a legacy input.
	request := httptest.NewRequest("POST", h.prefix+"sync/ack", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	h.mu.Lock()
	err = h.metadata.Set(h.mobileAckPrefix(request)+"AssetV1", ack)
	h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// Older installations have SHA-1-only receipts; migration computes their
	// whole-file identity without allocating a new asset ID or sequence.
	older := immichDedupAsset(t, h, still)
	wantWhole := older.WholeRef
	if err := h.metadata.Delete("whole|" + older.WholeRef.String()); err != nil {
		t.Fatal(err)
	}
	older.WholeRef = blob.Ref{}
	data, _ := json.Marshal(older)
	if err := h.metadata.Set("asset|"+still, string(data)); err != nil {
		t.Fatal(err)
	}
	rows := immichMigrationRows(t, h.metadata)
	path, conf := immichMigrationExport(t, rows)
	filesBefore := immichMigrationFiles(t, path)
	immichMigrationOpen(t, h)
	if err := h.importLegacyImmich(conf); err != nil {
		t.Fatal(err)
	}
	if got := immichMigrationFiles(t, path); !reflect.DeepEqual(got, filesBefore) {
		t.Fatal("read-only migration changed the legacy database files")
	}
	if err := h.initIdentity(); err != nil {
		t.Fatal(err)
	}
	if h.userID() != uid {
		t.Fatalf("owner = %q, want %q", h.userID(), uid)
	}
	if a := immichDedupAsset(t, h, still); a.WholeRef != wantWhole || a.LivePhotoVideoID != video ||
		!a.IsFavorite || a.Sequence != 2 || string(a.Metadata) != metadata {
		t.Fatalf("migrated still = %+v", a)
	}
	for _, key := range []string{"sequence", "usage", "user|preferences", "user|mobileProfile"} {
		got, err := h.metadata.Get(key)
		if err != nil || got != rows[key] {
			t.Errorf("%s = %q, %v; want %q", key, got, err, rows[key])
		}
	}
	for key, want := range rows {
		if strings.HasPrefix(key, "session|") || strings.HasPrefix(key, "syncack|") {
			if got, err := h.metadata.Get(key); err != nil || got != want {
				t.Errorf("%s changed: %q, %v", key, got, err)
			}
		}
	}
	if w := immichTestRequest(t, h, token, "POST", "auth/validateToken", nil, ""); w.Code != 200 {
		t.Fatalf("preserved session: %d %s", w.Code, w.Body)
	}
	if w := immichTestRequest(t, h, token, "GET", "sync/ack", nil, ""); w.Code != 200 || !strings.Contains(w.Body.String(), ack) {
		t.Fatalf("preserved ACK: %d %s", w.Code, w.Body)
	}
	// Neither the reusable bearer secret nor the configured password becomes
	// part of native state; only the existing token/credential hashes do.
	for _, ref := range h.storage.(*immichTestStorage).BlobrefStrings() {
		text, _ := h.storage.(*immichTestStorage).BlobContents(blob.MustParse(ref))
		if strings.Contains(text, token) || strings.Contains(text, `"secret"`) {
			t.Fatalf("raw credentials in native blob %s", ref)
		}
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	immichTestDiscardAcks(t, h)
	func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if err := h.metadata.Close(); err != nil {
			t.Fatal(err)
		}
		h.metadata, err = newImmichState(h)
		if err != nil {
			t.Fatal(err)
		}
	}()
	if err := h.importLegacyImmich(conf); err != nil {
		t.Fatalf("initialized native state reopened deleted legacy path: %v", err)
	}
	// Even obsolete/invalid arguments are harmless after native activation.
	if err := h.importLegacyImmich(jsonconfig.Obj{"type": "anything", "delete": true}); err != nil {
		t.Fatalf("activated state inspected obsolete config: %v", err)
	}
	if err := h.initIdentity(); err != nil || h.userID() != uid {
		t.Fatalf("native reload identity = %q, %v", h.userID(), err)
	}
	if got := immichUploadedID(t, immichTestUpload(t, h, token, "legacy still", nil), 200); got != still {
		t.Fatalf("migrated duplicate = %s, want %s", got, still)
	}
}

func TestImmichLegacyMigrationMissingSource(t *testing.T) {
	h := newImmichLegacyTestHandler(t)
	immichMigrationOpen(t, h)
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0700); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(root, "regular")
	if err := os.WriteFile(regular, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{missing, empty, regular} {
		if err := h.importLegacyImmich(jsonconfig.Obj{"type": "leveldb", "file": path}); err == nil {
			t.Errorf("accepted invalid source %s", path)
		}
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing database was created: %v", err)
	}
	if files, err := os.ReadDir(empty); err != nil || len(files) != 0 {
		t.Errorf("empty directory was modified: %v, %v", files, err)
	}
	for _, conf := range []jsonconfig.Obj{
		{"type": "memory", "file": missing},
		{"type": "leveldb", "file": missing, "wipe": true},
		{"type": "leveldb", "file": missing, "readOnly": true},
		{"type": "leveldb", "file": missing, "bloomFilterBits": 10},
	} {
		if err := h.importLegacyImmich(conf); err == nil {
			t.Errorf("accepted unsafe source config: %v", conf)
		}
	}
	if _, err := h.metadata.Get("user|id"); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("invalid source activated native state: %v", err)
	}
}

func TestImmichLegacyMigrationNativeSession(t *testing.T) {
	h := newImmichLegacyTestHandler(t)
	uid := h.userID()
	token := immichTestLogin(t, h)
	rows := immichMigrationRows(t, h.metadata)
	for key, value := range rows {
		if !strings.HasPrefix(key, "session|") {
			continue
		}
		var session immichSession
		if err := json.Unmarshal([]byte(value), &session); err != nil {
			t.Fatal(err)
		}
		session.Credential = "perkeep:" + uid
		data, _ := json.Marshal(session)
		rows[key] = string(data)
	}
	// Session revocation historically leaves its hashed-token ACKs behind.
	orphan := "syncack|session|" + strings.Repeat("0", 64) + "|AssetV1"
	rows[orphan] = "AssetV1|0"
	_, conf := immichMigrationExport(t, rows)
	h.perkeepAuth = true
	immichMigrationOpen(t, h)
	if err := h.importLegacyImmich(conf); err != nil {
		t.Fatal(err)
	}
	if err := h.initIdentity(); err != nil || h.userID() != uid {
		t.Fatalf("native-auth identity = %s, %v", h.userID(), err)
	}
	request := httptest.NewRequest("GET", h.prefix+"users/me", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if _, err := h.session(request); err != nil {
		t.Fatalf("native-auth session was not preserved: %v", err)
	}
	if got, err := h.metadata.Get(orphan); err != nil || got != rows[orphan] {
		t.Fatalf("orphan ACK = %q, %v", got, err)
	}
}

func TestImmichLegacyMigrationRejectCorruptionBeforePublication(t *testing.T) {
	h := newImmichLegacyTestHandler(t)
	token := immichTestLogin(t, h)
	id := immichUploadedID(t, immichTestUpload(t, h, token, "original to validate", nil), 201)
	second := immichUploadedID(t, immichTestUpload(t, h, token, "second original", nil), 201)
	base := immichMigrationRows(t, h.metadata)
	mutateAsset := func(rows map[string]string, id string, fn func(*immichAsset)) {
		var a immichAsset
		if err := json.Unmarshal([]byte(rows["asset|"+id]), &a); err != nil {
			t.Fatal(err)
		}
		fn(&a)
		data, _ := json.Marshal(&a)
		rows["asset|"+id] = string(data)
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]string)
	}{
		{"unknown-key", func(rows map[string]string) { rows["password"] = "do not publish this" }},
		{"missing-owner", func(rows map[string]string) { delete(rows, "user|id") }},
		{"bad-owner", func(rows map[string]string) { rows["user|id"] = "not-a-uuid" }},
		{"sequence-behind", func(rows map[string]string) { rows["sequence"] = "1" }},
		{"wrong-usage", func(rows map[string]string) { rows["usage"] = "0" }},
		{"unknown-asset-field", func(rows map[string]string) {
			rows["asset|"+id] = strings.TrimSuffix(rows["asset|"+id], "}") + `,"password":"do not publish"}`
		}},
		{"duplicate-sequence", func(rows map[string]string) {
			mutateAsset(rows, second, func(a *immichAsset) { a.Sequence = 1 })
		}},
		{"missing-change", func(rows map[string]string) { delete(rows, "change|00000000000000000001") }},
		{"bogus-change", func(rows map[string]string) { rows["change|00000000000000000099"] = id }},
		{"wrong-original-size", func(rows map[string]string) {
			mutateAsset(rows, id, func(a *immichAsset) { a.Size++ })
			n, _ := strconv.ParseInt(rows["usage"], 10, 64)
			rows["usage"] = strconv.FormatInt(n+1, 10)
		}},
		{"wrong-original-sha1", func(rows map[string]string) {
			mutateAsset(rows, id, func(a *immichAsset) {
				delete(rows, "checksum|"+a.Checksum)
				a.Checksum = strings.Repeat("0", 40)
				rows["checksum|"+a.Checksum] = id
			})
		}},
		{"wrong-original-whole", func(rows map[string]string) {
			mutateAsset(rows, id, func(a *immichAsset) {
				delete(rows, "whole|"+a.WholeRef.String())
				a.WholeRef = blob.RefFromString("different bytes")
				rows["whole|"+a.WholeRef.String()] = id
			})
		}},
		{"usage-overflow", func(rows map[string]string) {
			mutateAsset(rows, id, func(a *immichAsset) { a.Size = 1<<63 - 1 })
		}},
		{"future-ack", func(rows map[string]string) {
			rows["syncack|session|"+strings.Repeat("0", 64)+"|AssetV1"] = "AssetV1|99"
		}},
		{"raw-token-session-key", func(rows map[string]string) { rows["session|a-raw-bearer-token"] = `{}` }},
		{"missing-live-video", func(rows map[string]string) {
			mutateAsset(rows, id, func(a *immichAsset) { a.LivePhotoVideoID = "00000000-0000-4000-8000-000000000000" })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := make(map[string]string, len(base))
			for key, value := range base {
				rows[key] = value
			}
			tc.mutate(rows)
			_, conf := immichMigrationExport(t, rows)
			immichMigrationOpen(t, h)
			before := immichDedupPublished(t, h)
			if err := h.importLegacyImmich(conf); err == nil {
				t.Fatal("corrupt legacy state was accepted")
			}
			immichDedupAssertPublished(t, h, before)
			if _, err := h.metadata.Get("user|id"); !errors.Is(err, sorted.ErrNotFound) {
				t.Fatalf("corrupt legacy state activated: %v", err)
			}
		})
	}
}

func TestImmichLegacyMigrationMissingPrimary(t *testing.T) {
	for _, missing := range []string{"original", "permanode", "claim"} {
		t.Run(missing, func(t *testing.T) {
			h := newImmichLegacyTestHandler(t)
			token := immichTestLogin(t, h)
			const original = "legacy original whose primary blob is missing"
			id := immichUploadedID(t, immichTestUpload(t, h, token, original, nil), 201)
			a := immichDedupAsset(t, h, id)
			_, conf := immichMigrationExport(t, immichMigrationRows(t, h.metadata))
			immichMigrationOpen(t, h)
			var ref blob.Ref
			switch missing {
			case "original":
				ref = blob.RefFromString(original)
			case "permanode":
				ref = a.PermanodeRef
			case "claim":
				ix := h.search.Index()
				ix.RLock()
				claims, err := ix.AppendClaims(context.Background(), nil, a.PermanodeRef, h.signer.KeyIDLong(), "camliContent")
				ix.RUnlock()
				if err != nil || len(claims) == 0 {
					t.Fatalf("owner content claims: %v, %v", claims, err)
				}
				ref = claims[0].BlobRef
			}
			if err := h.storage.RemoveBlobs(context.Background(), []blob.Ref{ref}); err != nil {
				t.Fatal(err)
			}
			before := immichDedupPublished(t, h)
			if err := h.importLegacyImmich(conf); err == nil {
				t.Fatalf("accepted missing primary %s %v", missing, ref)
			}
			immichDedupAssertPublished(t, h, before)
			if _, err := h.metadata.Get("user|id"); !errors.Is(err, sorted.ErrNotFound) {
				t.Fatalf("missing primary data activated migration: %v", err)
			}
		})
	}
}

type immichMigrationFailKV struct {
	sorted.KeyValue
	commits int
	failAt  int
	maxSize int
}

func (kv *immichMigrationFailKV) CommitBatch(b sorted.BatchMutation) error {
	// The generic mutation view is part of sorted's public batch contract.
	if mutations, ok := b.(interface{ Mutations() []sorted.Mutation }); ok {
		assets := 0
		for _, m := range mutations.Mutations() {
			if !m.IsDelete() && strings.HasPrefix(m.Key(), "asset|") {
				assets++
			}
		}
		kv.maxSize = max(kv.maxSize, assets)
	}
	kv.commits++
	if kv.commits == kv.failAt {
		return errors.New("interrupted migration")
	}
	return kv.KeyValue.CommitBatch(b)
}

func TestImmichLegacyMigrationResume(t *testing.T) {
	h := newImmichLegacyTestHandler(t)
	uid := h.userID()
	token := immichTestLogin(t, h)
	// One more than a publication batch forces a durable partial catalog.
	for i := range immichMigrationBatchSize + 1 {
		immichUploadedID(t, immichTestUpload(t, h, token, fmt.Sprintf("legacy original %d", i), nil), 201)
	}
	// Exercise bounded account publication too, without any raw bearer token.
	for i := range immichMigrationBatchSize + 1 {
		rows := immichMigrationRows(t, h.metadata)
		for key, value := range rows {
			if strings.HasPrefix(key, "session|") {
				var session immichSession
				json.Unmarshal([]byte(value), &session)
				session.ID, _ = immichUUID()
				data, _ := json.Marshal(session)
				if err := h.metadata.Set(fmt.Sprintf("session|%064x", i), string(data)); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
	}
	rows := immichMigrationRows(t, h.metadata)
	path, conf := immichMigrationExport(t, rows)
	beforeFiles := immichMigrationFiles(t, path)
	immichMigrationOpen(t, h)
	fault := &immichMigrationFailKV{KeyValue: h.metadata, failAt: 2}
	h.mu.Lock()
	h.metadata = fault
	h.mu.Unlock()
	if err := h.importLegacyImmich(conf); err == nil || !strings.Contains(err.Error(), "interrupted migration") {
		t.Fatalf("interruption = %v", err)
	}
	if fault.maxSize > immichMigrationBatchSize {
		t.Fatalf("oversized publication batch: %d assets", fault.maxSize)
	}
	partial := immichMigrationRows(t, h.metadata)
	count := 0
	for key := range partial {
		if strings.HasPrefix(key, "asset|") {
			count++
		}
	}
	if count != immichMigrationBatchSize {
		t.Fatalf("partial assets = %d, want %d", count, immichMigrationBatchSize)
	}
	if _, err := h.metadata.Get("user|id"); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("interrupted import activated: %v", err)
	}
	if ordinary, err := newImmichState(h); err == nil {
		ordinary.Close()
		t.Fatal("ordinary startup accepted an incomplete migration")
	}
	if got := immichMigrationFiles(t, path); !reflect.DeepEqual(got, beforeFiles) {
		t.Fatal("failed migration changed legacy source files")
	}
	immichMigrationOpen(t, h)
	// Fail during the second account batch on the resumed import: the photo
	// catalog is complete, but a missing session must still prevent activation.
	fault = &immichMigrationFailKV{KeyValue: h.metadata, failAt: 3}
	h.mu.Lock()
	h.metadata = fault
	h.mu.Unlock()
	if err := h.importLegacyImmich(conf); err == nil {
		t.Fatal("account interruption did not fail")
	}
	if _, err := h.metadata.Get("user|id"); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("partial account state activated: %v", err)
	}
	immichMigrationOpen(t, h)
	if err := h.importLegacyImmich(conf); err != nil {
		t.Fatal(err)
	}
	if err := h.initIdentity(); err != nil || h.userID() != uid {
		t.Fatalf("resumed owner = %s, %v", h.userID(), err)
	}
	for key, want := range rows {
		got, err := h.metadata.Get(key)
		if err != nil || got != want {
			t.Errorf("resumed %s differs: %q, %v; want %q", key, got, err, want)
		}
	}
	immichTestDiscardAcks(t, h)
	func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if err := h.metadata.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		h.metadata, err = newImmichState(h)
		if err != nil {
			t.Fatalf("complete migration did not reopen normally: %v", err)
		}
	}()
}
