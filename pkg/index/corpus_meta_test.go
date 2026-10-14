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
	"fmt"
	"maps"
	"slices"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
)

func TestMetaScanRanges(t *testing.T) {
	ranges := metaScanRanges()
	sorted := slices.Clone(ranges)
	slices.SortFunc(sorted, func(a, b keyRange) int {
		if a.start < b.start {
			return -1
		}
		if a.start > b.start {
			return 1
		}
		return 0
	})
	// Sorted by start, the ranges must tile [meta:, meta;) exactly.
	if got := sorted[0].start; got != "meta:" {
		t.Errorf("first range starts at %q; want meta:", got)
	}
	if got := sorted[len(sorted)-1].end; got != "meta;" {
		t.Errorf("last range ends at %q; want meta;", got)
	}
	for i, r := range sorted {
		if r.start >= r.end {
			t.Errorf("empty or inverted range %q", r)
		}
		if i > 0 && sorted[i-1].end != r.start {
			t.Errorf("range %q doesn't start where %q ends", r, sorted[i-1])
		}
	}
	// The first ones cycle through the hash types.
	hashes := blob.HashFuncs()
	slices.Sort(hashes)
	for i, h := range hashes {
		if want := fmt.Sprintf("meta:%s-00", h); ranges[i].start != want {
			t.Errorf("range %d starts at %q; want %q", i, ranges[i].start, want)
		}
	}
}

// TestScanMeta checks that scanMeta loads the same blobs as merging
// each meta row in turn.
func TestScanMeta(t *testing.T) {
	kv := sorted.NewMemoryKeyValue()
	types := []schema.CamliType{"", "", "", schema.TypePermanode, schema.TypeFile, schema.TypeClaim}
	for i := range 3000 {
		var br blob.Ref
		switch i % 3 {
		case 0:
			br = blob.RefFromString(fmt.Sprint(i)) // the default hash
		case 1:
			br = blob.MustParse(fmt.Sprintf("sha1-%040x", i*7919))
		case 2:
			br = blob.MustParse(fmt.Sprintf("sha256-%064x", i*104729))
		}
		mime := "application/octet-stream"
		if typ := types[i%len(types)]; typ != "" {
			mime = "application/json; camliType=" + string(typ)
		}
		if err := kv.Set("meta:"+br.String(), fmt.Sprintf("%d|%s", i+1, mime)); err != nil {
			t.Fatal(err)
		}
	}

	want := newCorpus()
	it := kv.Find("meta:", "meta;")
	for it.Next() {
		if err := want.mergeMetaRow(it.KeyBytes(), it.ValueBytes()); err != nil {
			t.Fatal(err)
		}
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}

	got := newCorpus()
	got.building = true
	if err := got.scanMeta(kv); err != nil {
		t.Fatal(err)
	}

	if len(got.blobs) != 3000 {
		t.Errorf("loaded %d blobs; want 3000", len(got.blobs))
	}
	if !maps.EqualFunc(got.blobs, want.blobs, func(a, b *corpusBlobMeta) bool { return *a == *b }) {
		t.Error("blobs differ from merging each row")
	}
	if got.sumBlobBytes != want.sumBlobBytes {
		t.Errorf("sumBlobBytes = %d; want %d", got.sumBlobBytes, want.sumBlobBytes)
	}
	if len(got.camBlobs) != len(want.camBlobs) {
		t.Errorf("camBlobs has %d types; want %d", len(got.camBlobs), len(want.camBlobs))
	}
	for typ, wm := range want.camBlobs {
		if !maps.EqualFunc(got.camBlobs[typ], wm, func(a, b *corpusBlobMeta) bool { return *a == *b }) {
			t.Errorf("camBlobs[%q] differs from merging each row", typ)
		}
	}
}
