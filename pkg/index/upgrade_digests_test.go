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

// TestAddDigests checks that upgrading a version 10 index to version 11
// adds the digest rows that indexing the files at version 11 does,
// reading each content once, and skipping those that already have them.
func TestAddDigests(t *testing.T) {
	ctx := context.Background()
	files := []struct{ name, data string }{
		{"a.txt", "hello"},
		{"b.txt", "hello"}, // the same contents as a.txt
		{"c.txt", "already has digests"},
		{"d.txt", "indexed with the wrong wholeRef"},
	}
	sto := new(memory.Storage)
	ix := newLoggingIndex(t)
	mm := &mutationMap{kv: make(map[string]string)}
	fileRefs := make(map[string]blob.Ref)
	for _, f := range files {
		data := []byte(f.data)
		if _, err := sto.ReceiveBlob(ctx, blob.RefFromBytes(data), bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		bb := schema.NewFileMap(f.name).SetModTime(time.Unix(1700000000, 0).UTC())
		if err := bb.PopulateParts(int64(len(data)), []schema.BytesPart{{BlobRef: blob.RefFromBytes(data), Size: uint64(len(data))}}); err != nil {
			t.Fatal(err)
		}
		file := bb.Blob()
		if _, err := sto.ReceiveBlob(ctx, file.BlobRef(), strings.NewReader(file.JSON())); err != nil {
			t.Fatal(err)
		}
		if err := ix.populateFile(ctx, &missTrackFetcher{fetcher: sto}, file, mm); err != nil {
			t.Fatalf("populateFile(%s): %v", f.name, err)
		}
		fileRefs[f.name] = file.BlobRef()
	}
	want := maps.Clone(mm.kv)
	want[keySchemaVersion.name] = "11"
	for _, data := range []string{"hello", "already has digests"} {
		wantDigestRows := make(map[string]string)
		addWantDigestRows(wantDigestRows, []byte(data))
		for k, v := range wantDigestRows {
			if want[k] != v {
				t.Fatalf("version 11 indexing of %q: row %q = %q; want %q", data, k, want[k], v)
			}
		}
	}

	// d.txt's fileinfo row has the wholeRef of other contents, so its
	// digests don't get indexed, under either.
	dWhole := blob.RefFromString("indexed with the wrong wholeRef")
	wrongWhole := blob.RefFromString("other contents")
	dInfo := keyFileInfo.Key(fileRefs["d.txt"])
	want[dInfo] = strings.Replace(want[dInfo], dWhole.String(), wrongWhole.String(), 1)
	for k := range want {
		if strings.Contains(k, "|"+dWhole.String()) && !strings.HasPrefix(k, keyWholeToFileRef.name+"|") {
			delete(want, k)
		}
	}

	kv := sorted.NewMemoryKeyValue()
	t.Cleanup(func() { kv.Close() })
	cWhole := blob.RefFromString("already has digests")
	for k, v := range want {
		isV11Row := strings.HasPrefix(k, keyFileDigests.name+"|") || strings.HasPrefix(k, keyDigestToWhole.name+"|")
		if isV11Row && !strings.Contains(k, cWhole.String()) {
			continue
		}
		if k == keySchemaVersion.name {
			v = "10"
		}
		if err := kv.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}

	old, err := New(kv)
	if !errors.Is(err, errMissingDigests) {
		t.Fatalf("New of version 10 index: error %v; want errMissingDigests", err)
	}
	old.Logger = test.NewLogger(t, "")
	fetched0, _ := sto.Stats()
	if err := old.addDigests(ctx, sto); err != nil {
		t.Fatalf("addDigests: %v", err)
	}
	// Each read is of a file schema and its one content blob: the
	// contents "hello" (once, for both files) and d.txt's.
	if fetched, _ := sto.Stats(); fetched-fetched0 != 4 {
		t.Errorf("addDigests fetched %d blobs; want 4", fetched-fetched0)
	}
	if _, err := New(kv); err != nil {
		t.Fatalf("New after upgrade: %v", err)
	}

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
