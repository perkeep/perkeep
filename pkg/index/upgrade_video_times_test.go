/*
Copyright 2026 The Perkeep Authors.

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
	"bytes"
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

// upgradeVideoTimes upgrades the version 11 index kv to version 12 with
// addVideoTimes, reading files from fetcher.
func upgradeVideoTimes(t *testing.T, kv sorted.KeyValue, fetcher blob.Fetcher) {
	t.Helper()
	ix, err := New(kv)
	if !errors.Is(err, errMissingVideoTimes) {
		t.Fatalf("New of version 11 index: error %v; want errMissingVideoTimes", err)
	}
	ix.Logger = test.NewLogger(t, "")
	if err := ix.addVideoTimes(context.Background(), fetcher); err != nil {
		t.Fatalf("addVideoTimes: %v", err)
	}
	if _, err := New(kv); err != nil {
		t.Fatalf("New after upgrade: %v", err)
	}
}

// TestAddVideoTimes checks that upgrading a version 11 index to version 12
// gives videos the times that indexing them at version 12 does, and
// leaves other files' times alone.
func TestAddVideoTimes(t *testing.T) {
	ctx := context.Background()
	exported := time.Date(2026, 10, 31, 17, 28, 2, 0, time.UTC)
	recorded := liveVideo(map[string]string{"com.apple.quicktime.creationdate": "2024-05-30T18:48:50-0700"})
	files := []struct {
		name    string
		data    []byte
		modTime time.Time // or zero, for none
	}{
		{"exported.MOV", recorded, exported},      // gains its earlier recording time
		{"no-modtime.MOV", recorded, time.Time{}}, // gains its only time
		{"unrecorded.MOV", trackedVideo("qt  ", 640, 480, 0), exported},
		{"a.txt", []byte("not a video"), exported},
	}
	sto := new(memory.Storage)
	ix := newLoggingIndex(t)
	mm := &mutationMap{kv: make(map[string]string)}
	oldTimes := make(map[string]string) // keyFileTimes rows, as version 11 indexed them
	for _, f := range files {
		if _, err := sto.ReceiveBlob(ctx, blob.RefFromBytes(f.data), bytes.NewReader(f.data)); err != nil {
			t.Fatal(err)
		}
		bb := schema.NewFileMap(f.name)
		if !f.modTime.IsZero() {
			bb.SetModTime(f.modTime)
		}
		if err := bb.PopulateParts(int64(len(f.data)), []schema.BytesPart{{BlobRef: blob.RefFromBytes(f.data), Size: uint64(len(f.data))}}); err != nil {
			t.Fatal(err)
		}
		file := bb.Blob()
		if _, err := sto.ReceiveBlob(ctx, file.BlobRef(), strings.NewReader(file.JSON())); err != nil {
			t.Fatal(err)
		}
		if err := ix.populateFile(ctx, &missTrackFetcher{fetcher: sto}, file, mm); err != nil {
			t.Fatalf("populateFile(%s): %v", f.name, err)
		}
		oldTimes[keyFileTimes.Key(file.BlobRef())] = keyFileTimes.Val(fileTimesValue([]time.Time{f.modTime}))
	}
	want := maps.Clone(mm.kv)
	want[keySchemaVersion.name] = "12"
	for k, v := range map[string]string{
		"exported.MOV":   "2024-05-31T01:48:50Z,2026-10-31T17:28:02Z",
		"no-modtime.MOV": "2024-05-31T01:48:50Z",
		"unrecorded.MOV": "2026-10-31T17:28:02Z",
		"a.txt":          "2026-10-31T17:28:02Z",
	} {
		found := false
		for key, row := range want {
			if strings.HasPrefix(key, keyFileInfo.name+"|") && strings.Contains(row, "|"+k+"|") {
				fileRef := strings.TrimPrefix(key, keyFileInfo.name+"|")
				if got := urld(want[keyFileTimes.name+"|"+fileRef]); got != v {
					t.Fatalf("version 12 indexing of %s: times %q; want %q", k, got, v)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("no fileinfo row of %s", k)
		}
	}

	kv := sorted.NewMemoryKeyValue()
	t.Cleanup(func() { kv.Close() })
	for k, v := range want {
		if old, ok := oldTimes[k]; ok {
			v = old
		}
		if k == keySchemaVersion.name {
			v = "11"
		}
		if err := kv.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	upgradeVideoTimes(t, kv, sto)

	got := make(map[string]string)
	it := kv.Find("", "")
	for it.Next() {
		got[it.Key()] = it.Value()
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("after upgrade, row %q = %q; want %q", k, got[k], v)
		}
	}
	for k, v := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("after upgrade, unexpected row %q = %q", k, v)
		}
	}
}
