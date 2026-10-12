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
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"perkeep.org/pkg/sorted"
)

type immichAckTestKV struct {
	sorted.KeyValue
	commits   [][]sorted.Mutation
	writeErr  error
	readErr   error
	closeErr  error
	closed    bool
	committed chan struct{}
}

func (kv *immichAckTestKV) Get(key string) (string, error) {
	if kv.readErr != nil {
		return "", kv.readErr
	}
	return kv.KeyValue.Get(key)
}

func (kv *immichAckTestKV) CommitBatch(b sorted.BatchMutation) error {
	kv.commits = append(kv.commits, b.(interface{ Mutations() []sorted.Mutation }).Mutations())
	if kv.writeErr != nil {
		return kv.writeErr
	}
	err := kv.KeyValue.CommitBatch(b)
	if err == nil && kv.committed != nil {
		kv.committed <- struct{}{}
	}
	return err
}

func (kv *immichAckTestKV) Close() error {
	kv.closed = true
	return errors.Join(kv.KeyValue.Close(), kv.closeErr)
}

// The fake clock is only accessed with h.mu, just like the timer callback.
func immichAckTestState(t *testing.T) (*Handler, *immichAckState, *immichAckTestKV, *time.Time) {
	t.Helper()
	kv := &immichAckTestKV{KeyValue: sorted.NewMemoryKeyValue()}
	h := &Handler{metadata: kv}
	a, err := newImmichAckState(h)
	if err != nil {
		t.Fatal(err)
	}
	h.acks = a
	now := a.lastFlush
	a.now = func() time.Time { return now }
	t.Cleanup(func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.acks.stopTimerLocked()
		h.acks.closed = true
		kv.KeyValue.Close()
	})
	return h, a, kv, &now
}

func TestImmichAckFixedDailyWindow(t *testing.T) {
	h, a, kv, now := immichAckTestState(t)
	h.mu.Lock()
	defer h.mu.Unlock()
	base := *now
	prefix := "syncack|session|"
	if err := a.flushLocked(true); err != nil {
		t.Fatal(err)
	}
	if err := a.setLocked(prefix, nil); err != nil {
		t.Fatal(err)
	}
	if len(kv.commits) != 0 || a.timer != nil {
		t.Fatal("clean/no-op state wrote or armed a timer")
	}
	*now = base.Add(23 * time.Hour)
	if err := a.setLocked(prefix, map[string]string{"AssetV2": "AssetV2|1"}); err != nil {
		t.Fatal(err)
	}
	timer := a.timer
	for n := 1; n <= 100; n++ {
		if err := a.setLocked(prefix, map[string]string{"AssetV2": fmt.Sprintf("AssetV2|%d", n)}); err != nil {
			t.Fatal(err)
		}
		if a.timer != timer {
			t.Fatal("ACK update debounced the fixed timer")
		}
	}
	copy, err := a.getLocked(prefix)
	if err != nil || copy["AssetV2"] != "AssetV2|100" {
		t.Fatalf("RAM read: %v, %v", copy, err)
	}
	copy["AssetV2"] = "not the stored ACK"
	if a.values[prefix+"AssetV2"] != "AssetV2|100" {
		t.Fatal("get exposed the mutable ACK map")
	}
	*now = base.Add(immichAckInterval - time.Nanosecond)
	if err := a.flushLocked(false); err != nil {
		t.Fatal(err)
	}
	if len(kv.commits) != 0 || !a.deadlineLocked().Equal(base.Add(immichAckInterval)) {
		t.Fatal("flushed before 24 hours or moved checkpoint deadline")
	}
	*now = base.Add(immichAckInterval)
	if err := a.flushLocked(false); err != nil {
		t.Fatal(err)
	}
	if len(kv.commits) != 1 || len(kv.commits[0]) != 2 || len(a.dirty) != 0 || a.timer != nil {
		t.Fatalf("checkpoint: commits=%v dirty=%v timer=%v", kv.commits, a.dirty, a.timer)
	}
	if got, _ := kv.Get(prefix + "AssetV2"); got != "AssetV2|100" {
		t.Fatalf("durable ACK = %q", got)
	}
	*now = now.Add(10 * immichAckInterval)
	if err := a.flushLocked(false); err != nil {
		t.Fatal(err)
	}
	if len(kv.commits) != 1 || !a.lastFlush.Equal(base.Add(immichAckInterval)) {
		t.Fatal("clean flush wrote or advanced the checkpoint clock")
	}
}

func TestImmichAckLoadLegacyAndPersistedClock(t *testing.T) {
	h, a, kv, _ := immichAckTestState(t)
	h.mu.Lock()
	defer h.mu.Unlock()
	legacy := map[string]string{
		"syncack|first|AssetV2":         "AssetV2|4",
		"syncack|first|AssetMetadataV1": "AssetMetadataV1|4|2",
		"syncack|second|AssetV2":        "AssetV2|1",
	}
	for key, value := range legacy {
		if err := kv.KeyValue.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	base := a.lastFlush.Add(-48 * time.Hour).UTC()
	if err := kv.KeyValue.Set(immichAckCheckpointKey, base.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	restored, err := newImmichAckState(h)
	if err != nil {
		t.Fatal(err)
	}
	h.acks = restored
	if !reflect.DeepEqual(restored.values, legacy) || len(restored.dirty) != 0 ||
		restored.timer != nil || !restored.lastFlush.Equal(base) || !a.closed {
		t.Fatalf("loaded state: %+v", restored)
	}
	if err := restored.flushLocked(false); err != nil || len(kv.commits) != 0 {
		t.Fatalf("startup published clean ACKs: %v", err)
	}
	// An overdue stored clock makes the next dirty update due immediately;
	// restarting does not give it another 24-hour grace period.
	now := base.Add(49 * time.Hour)
	restored.now = func() time.Time { return now }
	if err := restored.setLocked("syncack|first|", map[string]string{"AssetV2": "AssetV2|5"}); err != nil {
		t.Fatal(err)
	}
	if err := restored.flushLocked(false); err != nil || len(kv.commits) != 1 {
		t.Fatalf("overdue ACK not checkpointed: %v, %d commits", err, len(kv.commits))
	}
	got, err := restored.getLocked("syncack|first|")
	if err != nil || len(got) != 2 || got["AssetMetadataV1"] != "AssetMetadataV1|4|2" {
		t.Fatalf("legacy entity offset lost: %v, %v", got, err)
	}
	other, err := restored.getLocked("syncack|second|")
	if err != nil || other["AssetV2"] != "AssetV2|1" {
		t.Fatalf("session scope lost: %v, %v", other, err)
	}
	if err := kv.KeyValue.Set(immichAckCheckpointKey, "corrupt timestamp"); err != nil {
		t.Fatal(err)
	}
	if _, err := newImmichAckState(h); err == nil || restored.closed {
		t.Fatal("invalid clock accepted or failed replacement closed live state")
	}
}

func TestImmichAckFailurePreservesDirtyAndBacksOff(t *testing.T) {
	h, a, kv, now := immichAckTestState(t)
	h.mu.Lock()
	defer h.mu.Unlock()
	prefix := "syncack|session|"
	if err := a.setLocked(prefix, map[string]string{"AssetV2": "AssetV2|1"}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(immichAckInterval)
	failure := errors.New("checkpoint failed")
	kv.writeErr = failure
	if err := a.flushLocked(false); !errors.Is(err, failure) {
		t.Fatalf("checkpoint error = %v", err)
	}
	if len(a.dirty) != 1 || a.timer == nil || !a.retryAfter.Equal(now.Add(immichAckInterval)) {
		t.Fatalf("failed checkpoint discarded dirty state or retry: %+v", a)
	}
	if err := a.flushLocked(false); err != nil || len(kv.commits) != 1 {
		t.Fatalf("failed checkpoint spun: %v, %d attempts", err, len(kv.commits))
	}
	// Reads and RAM updates must not bypass a newly poisoned native backend.
	kv.readErr = errors.New("native backend poisoned")
	if _, err := a.getLocked(prefix); !errors.Is(err, kv.readErr) {
		t.Fatalf("poisoned get = %v", err)
	}
	if err := a.setLocked(prefix, map[string]string{"AssetV2": "AssetV2|2"}); !errors.Is(err, kv.readErr) {
		t.Fatalf("poisoned set = %v", err)
	}
	if err := a.deleteLocked(prefix, nil); !errors.Is(err, kv.readErr) {
		t.Fatalf("poisoned delete = %v", err)
	}
	kv.readErr, kv.writeErr = nil, nil
	*now = now.Add(immichAckInterval)
	if err := a.flushLocked(false); err != nil || len(a.dirty) != 0 || a.timer != nil {
		t.Fatalf("retry did not recover: %v", err)
	}
}

func TestImmichAckDeleteDurableBarrier(t *testing.T) {
	h, a, kv, _ := immichAckTestState(t)
	h.mu.Lock()
	defer h.mu.Unlock()
	prefix := "syncack|session|"
	old := map[string]string{"AssetV2": "AssetV2|8", "AssetMetadataV1": "AssetMetadataV1|8|2"}
	if err := a.setLocked(prefix, old); err != nil {
		t.Fatal(err)
	}
	if err := a.flushLocked(true); err != nil {
		t.Fatal(err)
	}
	if err := a.setLocked(prefix, map[string]string{"AssetV2": "AssetV2|9"}); err != nil {
		t.Fatal(err)
	}
	kv.writeErr = errors.New("reset barrier failed")
	if err := a.deleteLocked(prefix, []string{"AssetV2"}); !errors.Is(err, kv.writeErr) {
		t.Fatalf("failed barrier = %v", err)
	}
	if a.values[prefix+"AssetV2"] != "AssetV2|9" || a.dirty[prefix+"AssetV2"] != "AssetV2|9" {
		t.Fatal("failed barrier changed RAM")
	}
	kv.writeErr = nil
	if err := a.deleteLocked(prefix, []string{"AssetV2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Get(prefix + "AssetV2"); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("old higher durable ACK survived reset: %v", err)
	}
	if _, ok := a.values[prefix+"AssetV2"]; ok || len(a.dirty) != 0 || a.timer != nil {
		t.Fatal("deleted ACK retained in RAM/pending timer")
	}
	if got, _ := kv.Get(prefix + "AssetMetadataV1"); got != old["AssetMetadataV1"] {
		t.Fatal("selective reset deleted an unrelated entity")
	}
	if err := a.deleteLocked(prefix, []string{}); err != nil {
		t.Fatal(err)
	}
	if err := a.deleteLocked(prefix, nil); err != nil {
		t.Fatal(err)
	}
	restored, err := newImmichAckState(h)
	if err != nil {
		t.Fatal(err)
	}
	h.acks = restored
	if len(restored.values) != 0 {
		t.Fatalf("reset resurrected ACKs on restart: %v", restored.values)
	}
}

func TestImmichAckCloseAlwaysClosesBackend(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%v", fail), func(t *testing.T) {
			h, a, kv, _ := immichAckTestState(t)
			h.mu.Lock()
			err := a.setLocked("syncack|device|", map[string]string{"AssetV2": "AssetV2|3"})
			h.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				kv.writeErr = errors.New("checkpoint failed on close")
				kv.closeErr = errors.New("backend close failed")
			}
			err = h.Close()
			if !kv.closed || !a.closed || a.timer != nil || len(kv.commits) != 1 {
				t.Fatalf("close did not cancel timer/attempt flush/close base: %v", err)
			}
			if fail {
				if !errors.Is(err, kv.writeErr) || !errors.Is(err, kv.closeErr) || len(a.dirty) != 1 {
					t.Fatalf("close lost error or dirty state: %v", err)
				}
			} else if err != nil || len(a.dirty) != 0 {
				t.Fatalf("graceful checkpoint failed: %v", err)
			}
		})
	}
}

func TestImmichAckTimerAndReplacement(t *testing.T) {
	h, a, kv, now := immichAckTestState(t)
	h.mu.Lock()
	kv.committed = make(chan struct{}, 1)
	*now = now.Add(immichAckInterval)
	err := a.setLocked("syncack|device|", map[string]string{"AssetV2": "AssetV2|1"})
	h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-kv.committed:
	case <-time.After(5 * time.Second):
		t.Fatal("due timer did not publish checkpoint")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if a.timer != nil || len(a.dirty) != 0 {
		t.Fatal("completed timer retained pending work")
	}
	// Queue an immediately due old callback behind mu, then replace the view.
	// It must not publish its pending ACK through the new metadata/view.
	*now = now.Add(immichAckInterval)
	if err := a.setLocked("syncack|device|", map[string]string{"AssetV2": "AssetV2|2"}); err != nil {
		t.Fatal(err)
	}
	oldTimer := a.timer
	replacement, err := newImmichAckState(h)
	if err != nil {
		t.Fatal(err)
	}
	h.acks = replacement
	if !a.closed || a.timer != nil || oldTimer.Stop() {
		t.Fatal("replacement did not cancel the old timer")
	}
	if len(kv.commits) != 1 || replacement.values["syncack|device|AssetV2"] != "AssetV2|1" {
		t.Fatal("replacement accidentally flushed old RAM state")
	}
}

func TestImmichAckConcurrentUpdatesAndClose(t *testing.T) {
	h, a, kv, _ := immichAckTestState(t)
	var wg sync.WaitGroup
	for n := range 32 {
		wg.Go(func() {
			prefix := fmt.Sprintf("syncack|device-%d|", n)
			for index := range 30 {
				h.mu.Lock()
				err := a.setLocked(prefix, map[string]string{"AssetMetadataV1": fmt.Sprintf("AssetMetadataV1|1|%d", index)})
				h.mu.Unlock()
				if err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	h.mu.Lock()
	if len(a.values) != 32 || len(a.dirty) != 32 || len(kv.commits) != 0 {
		t.Errorf("concurrent ACKs: values=%d dirty=%d commits=%d", len(a.values), len(a.dirty), len(kv.commits))
	}
	h.mu.Unlock()
	for range 16 {
		wg.Go(func() {
			if err := h.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(kv.commits) != 1 || !a.closed || a.timer != nil {
		t.Fatal("concurrent close duplicated checkpoints or retained timer")
	}
}

func TestImmichAckHTTPProgressStaysInRAM(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	other := immichTestLogin(t, h)
	items := make([]map[string]any, 100)
	for n := range items {
		items[n] = map[string]any{"key": fmt.Sprintf("item-%d", n), "value": map[string]any{"number": n}}
	}
	data, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	immichUploadedID(t, immichTestUpload(t, h, token, "many metadata ACKs", map[string]string{"metadata": string(data)}), 201)
	events := immichMobileTestStream(t, h, token, "AssetsV2", "AssetMetadataV1")
	before := immichDedupPublished(t, h)
	metadataEvents := 0
	for _, e := range events {
		if e.Type != "AssetV2" && e.Type != "AssetMetadataV1" {
			continue
		}
		immichMobileTestAck(t, h, token, e.Ack)
		if e.Type == "AssetMetadataV1" {
			metadataEvents++
			if metadataEvents == 1 {
				resumed := immichMobileTestStream(t, h, token, "AssetMetadataV1")
				if len(resumed) != 101 || resumed[0].Data.(map[string]any)["key"] != "item-1" {
					t.Fatalf("RAM metadata offset lost: %+v", resumed)
				}
			}
		}
	}
	if metadataEvents != 100 {
		t.Fatalf("metadata events = %d", metadataEvents)
	}
	w := immichTestRequest(t, h, token, "GET", "sync/ack", nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "AssetMetadataV1|1|99") {
		t.Fatalf("GET did not see latest RAM progress: %d %s", w.Code, w.Body)
	}
	if events := immichMobileTestStream(t, h, token, "AssetsV2"); len(events) != 1 {
		t.Fatalf("stream ignored latest RAM checkpoint: %+v", events)
	}
	if events := immichMobileTestStream(t, h, other, "AssetsV2"); len(events) != 3 {
		t.Fatalf("ACKs leaked between sessions: %+v", events)
	}
	immichDedupAssertPublished(t, h, before)
	if it := h.metadata.Find(immichAckPrefix, immichAckPrefix+"\xff"); it.Next() {
		it.Close()
		t.Fatal("ordinary POST persisted an ACK before the daily checkpoint")
	} else if err := it.Close(); err != nil {
		t.Fatal(err)
	}
}

// Model a crash/rebuild, deliberately bypassing graceful ACK flushing.
func immichAckTestCrashRestart(t *testing.T, h *Handler) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.acks.stopTimerLocked()
	h.acks.closed = true
	if err := h.metadata.Close(); err != nil {
		t.Fatal(err)
	}
	metadata, err := newImmichState(h)
	if err != nil {
		t.Fatal(err)
	}
	h.metadata = metadata
	h.acks, err = newImmichAckState(h)
	if err != nil {
		t.Fatal(err)
	}
}

func TestImmichAckDailyNativeRestartAndReset(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	first := immichUploadedID(t, immichTestUpload(t, h, token, "checkpointed first photo", nil), 201)
	second := immichUploadedID(t, immichTestUpload(t, h, token, "uncheckpointed second photo", nil), 201)
	events := immichMobileTestStream(t, h, token, "AssetsV2")
	immichMobileTestAck(t, h, token, events[0].Ack)
	h.mu.Lock()
	a := h.acks
	now := a.lastFlush.Add(immichAckInterval)
	a.now = func() time.Time { return now }
	before := immichDedupPublished(t, h)
	err := a.flushLocked(false)
	h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if after := immichDedupPublished(t, h); len(after) != len(before)+1 {
		t.Fatalf("daily checkpoint created %d signed blobs, want one account claim", len(after)-len(before))
	}
	immichMobileTestAck(t, h, token, events[1].Ack)
	immichAckTestCrashRestart(t, h)
	if !h.acks.lastFlush.Equal(now) {
		t.Fatal("restart forgot persisted checkpoint clock")
	}
	replayed := immichMobileTestStream(t, h, token, "AssetsV2")
	if len(replayed) != 3 || replayed[0].Data.(map[string]any)["id"] != second {
		t.Fatalf("crash did not replay only uncheckpointed work: %+v", replayed)
	}
	immichMobileTestAck(t, h, token, replayed[0].Ack)
	h.mu.Lock()
	now = now.Add(immichAckInterval)
	h.acks.now = func() time.Time { return now }
	err = h.acks.flushLocked(false)
	h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	immichAckTestCrashRestart(t, h)
	if got := immichMobileTestStream(t, h, token, "AssetsV2"); len(got) != 1 {
		t.Fatalf("successful daily checkpoint not restored: %+v", got)
	}
	// Reset persists immediately, even inside the next daily window.
	w := immichMobileTestJSON(t, h, token, "DELETE", "sync/ack", map[string]any{"types": []string{"AssetV2"}})
	if w.Code != 204 {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	immichMobileTestAck(t, h, token, events[0].Ack)
	immichAckTestCrashRestart(t, h)
	if got := immichMobileTestStream(t, h, token, "AssetsV2"); len(got) != 4 ||
		got[0].Data.(map[string]any)["id"] != first || got[1].Data.(map[string]any)["id"] != second {
		t.Fatalf("old higher ACK resurrected after reset: %+v", got)
	}
}

func TestImmichAckOversizedNativeCheckpointPreservesRAM(t *testing.T) {
	h := newImmichTestHandler(t)
	h.mu.Lock()
	a, err := newImmichAckState(h)
	if err != nil {
		h.mu.Unlock()
		t.Fatal(err)
	}
	h.acks = a
	h.mu.Unlock()
	func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		// Each row is small and individually valid, but enough sessions can
		// exceed the native schema's limit for one combined account claim.
		const count = 10000
		for n := range count {
			if err := a.setLocked(fmt.Sprintf("syncack|session-%05d|", n), map[string]string{"AssetV2": "AssetV2|0"}); err != nil {
				t.Fatal(err)
			}
		}
		before := immichDedupPublished(t, h)
		base := a.lastFlush
		now := base.Add(immichAckInterval)
		a.now = func() time.Time { return now }
		if err := a.flushLocked(false); err == nil || !strings.Contains(err.Error(), "native schema limit") {
			t.Fatalf("oversized checkpoint = %v", err)
		}
		if len(a.dirty) != count || len(a.values) != count || !a.lastFlush.Equal(base) {
			t.Fatalf("oversized checkpoint lost state: dirty=%d values=%d", len(a.dirty), len(a.values))
		}
		got, err := a.getLocked("syncack|session-09999|")
		if err != nil || got["AssetV2"] != "AssetV2|0" {
			t.Fatalf("RAM no longer usable after oversized batch: %v, %v", got, err)
		}
		immichDedupAssertPublished(t, h, before)
		// Validation failed before any publication, so native state must not
		// be poisoned or prevent unrelated account/photo transactions.
		if err := h.metadata.Set("user|oversizedAckRecovery", "still usable"); err != nil {
			t.Fatalf("oversized ACK batch poisoned native backend: %v", err)
		}
	}()
	// Do not gracefully retry the deliberately oversized dirty checkpoint.
	immichAckTestCrashRestart(t, h)
	if got, err := h.metadata.Get("user|oversizedAckRecovery"); err != nil || got != "still usable" {
		t.Fatalf("native recovery write did not survive restart: %q, %v", got, err)
	}
}

func TestImmichAckLogoutThenCheckpointNeverRevivesSession(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	immichMobileTestAck(t, h, token, "AssetV2|0")
	r := httptest.NewRequest("GET", h.prefix+"sync/ack", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	sessionKey := h.sessionKey(r)
	w := immichTestRequest(t, h, token, "POST", "auth/logout", nil, "")
	if w.Code != 201 {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	h.mu.Lock()
	now := h.acks.lastFlush.Add(immichAckInterval)
	h.acks.now = func() time.Time { return now }
	err := h.acks.flushLocked(false)
	h.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	immichAckTestCrashRestart(t, h)
	if _, err := h.metadata.Get(sessionKey); !errors.Is(err, sorted.ErrNotFound) {
		t.Fatalf("ACK flush resurrected logged-out session: %v", err)
	}
	if w := immichTestRequest(t, h, token, "GET", "sync/ack", nil, ""); w.Code != 401 {
		t.Fatalf("logged-out session usable after checkpoint/reopen: %d %s", w.Code, w.Body)
	}
}

func TestImmichAckNativePoisonFailsClosed(t *testing.T) {
	h := newImmichTestHandler(t)
	token := immichTestLogin(t, h)
	immichMobileTestAck(t, h, token, "AssetV2|0")
	h.mu.Lock()
	// Model the native poison set after an ambiguous claim publication.
	native := h.metadata.(*immichState)
	native.mu.Lock()
	poison := native.failPublication(errors.New("ambiguous publication"))
	native.mu.Unlock()
	_, err := h.acks.getLocked("syncack|device|")
	h.mu.Unlock()
	if !errors.Is(err, poison) {
		t.Fatalf("RAM get bypassed native poison: %v", err)
	}
	w := immichTestRequest(t, h, token, "GET", "sync/ack", nil, "")
	if w.Code != 500 {
		t.Fatalf("ACK RAM bypassed native poison: %d %s", w.Code, w.Body)
	}
	// Reopening the native backend, not returning stale RAM, repairs it.
	immichAckTestCrashRestart(t, h)
}
