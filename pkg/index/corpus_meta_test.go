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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/camtypes"
)

func TestMetaScanRanges(t *testing.T) {
	ranges := scanRanges("meta:")
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

// loadCorpusSerially loads the corpus from s the way scanFromStorage did
// before it was parallel: every row of every prefix in turn, merged into
// one corpus.
func loadCorpusSerially(t *testing.T, s sorted.KeyValue) *Corpus {
	t.Helper()
	c := newCorpus()
	c.building = true
	for _, prefix := range slurpPrefixes {
		r := prefixRange(prefix)
		it := s.Find(r.start, r.end)
		for it.Next() {
			var err error
			if typ := typeOfKey(prefix); typ == keySignerKeyID.name {
				signer, ok := c.blobParse(it.KeyBytes()[len(prefix):])
				if !ok {
					t.Fatalf("bad signer row %q", it.Key())
				}
				err = c.addKeyID(&mutationMap{signerBlobRef: signer, signerID: it.Value()})
			} else {
				err = corpusMergeFunc[typ](c, it.KeyBytes(), it.ValueBytes())
			}
			if err != nil {
				t.Fatalf("merging %q: %v", it.Key(), err)
			}
		}
		if err := it.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, pm := range c.permanodes {
		if err := pm.restoreInvariants(c.keyId); err != nil {
			t.Fatal(err)
		}
	}
	c.building = false
	return c
}

// TestScanFromStorageParallel checks that loading the corpus in parallel
// builds the same corpus as merging each row in turn.
func TestScanFromStorageParallel(t *testing.T) {
	kv := sorted.NewMemoryKeyValue()
	set := func(k, v string) {
		t.Helper()
		if err := kv.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	// ref returns a blobref of a hash type chosen by i, so rows are
	// spread across the scan ranges of every hash type.
	ref := func(kind string, i int) blob.Ref {
		sum := sha256.Sum256(fmt.Appendf(nil, "%s %d", kind, i))
		switch i % 3 {
		case 0:
			return blob.MustParse("sha1-" + hex.EncodeToString(sum[:20]))
		case 1:
			return blob.MustParse("sha224-" + hex.EncodeToString(sum[:28]))
		}
		return blob.MustParse("sha256-" + hex.EncodeToString(sum[:]))
	}
	meta := func(br blob.Ref, size int, camliType schema.CamliType) {
		mime := "application/octet-stream"
		if camliType != "" {
			mime = "application/json; camliType=" + string(camliType)
		}
		set("meta:"+br.String(), fmt.Sprintf("%d|%s", size, mime))
	}

	signer := ref("signer", 0)
	const keyID = "2931A67C26F5ABDA"
	meta(signer, 100, "")
	set("signerkeyid:"+signer.String(), keyID)

	const numPermanodes = 600
	pns := make([]blob.Ref, numPermanodes)
	for i := range pns {
		pns[i] = ref("permanode", i)
		meta(pns[i], 200, schema.TypePermanode)
	}
	t0 := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	for i, pn := range pns {
		for j := range 1 + i%5 {
			cl := ref(fmt.Sprintf("claim %d", i), j)
			if i%7 != 0 { // some claims have no meta row
				meta(cl, 300, schema.TypeClaim)
			}
			attr, value := "title", fmt.Sprintf("title %d.%d", i, j)
			switch j {
			case 1:
				// Points at another permanode, for claimBack.
				attr, value = "camliMember", pns[(i*31+7)%numPermanodes].String()
			case 2:
				attr, value = "camliNodeType", fmt.Sprintf("type%d", i%3)
			}
			date := t0.Add(time.Duration(i*10+(5-j)) * time.Minute) // not in key order within a permanode
			set(fmt.Sprintf("claim|%s|%s|%s|%s", pn, keyID, date.Format(time.RFC3339), cl),
				fmt.Sprintf("set-attribute|%s|%s|%s", urle(attr), urle(value), signer))
		}
	}

	for i := range 400 {
		file, whole := ref("file", i), ref("whole", i)
		meta(file, 400, schema.TypeFile)
		set("fileinfo|"+file.String(), fmt.Sprintf("%d|%s|%s|%s", 1000+i, urle(fmt.Sprintf("file %d.jpg", i)), urle("image/jpeg"), whole))
		if i%2 == 0 {
			set("filetimes|"+file.String(), urle("2001-02-03T04:05:06Z,2002-03-04T05:06:07Z"))
		}
		if i%3 == 0 {
			set("imagesize|"+file.String(), fmt.Sprintf("%d|%d", 640+i, 480+i))
			set("exifgps|"+whole.String(), fmt.Sprintf("%v|%v", 37.5+float64(i)/1000, -122.25))
		}
		set(fmt.Sprintf("wholetofile|%s|%s", whole, file), "1")
		if i%50 == 0 {
			set(fmt.Sprintf("mediatag|%s|album", whole), urle(fmt.Sprintf("album %d", i)))
		}
		dir := ref("dir", i/10)
		set(fmt.Sprintf("dirchild|%s|%s", dir, file), "1")
	}

	want := loadCorpusSerially(t, kv)
	got := newCorpus()
	if err := got.scanFromStorage(kv); err != nil {
		t.Fatal(err)
	}

	check := func(name string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s differs from loading serially", name)
		}
	}
	check("permanodes", got.permanodes, want.permanodes)
	check("files", got.files, want.files)
	check("imageInfo", got.imageInfo, want.imageInfo)
	check("fileWholeRef", got.fileWholeRef, want.fileWholeRef)
	check("gps", got.gps, want.gps)
	check("mediaTags", got.mediaTags, want.mediaTags)
	check("dirChildren", got.dirChildren, want.dirChildren)
	check("fileParents", got.fileParents, want.fileParents)
	check("permanodesSetByNodeType", got.permanodesSetByNodeType, want.permanodesSetByNodeType)
	check("hasLegacySHA1", got.hasLegacySHA1, want.hasLegacySHA1)
	// claimBack's slices aren't in any particular order.
	byRef := func(a, b *camtypes.Claim) int { return strings.Compare(a.BlobRef.String(), b.BlobRef.String()) }
	for _, m := range []map[blob.Ref][]*camtypes.Claim{got.claimBack, want.claimBack} {
		for _, claims := range m {
			slices.SortFunc(claims, byRef)
		}
	}
	check("claimBack", got.claimBack, want.claimBack)
	if len(got.permanodes) != numPermanodes || len(got.claimBack) == 0 || len(got.files) != 400 {
		t.Errorf("loaded %d permanodes, %d claimBack entries, %d files; test data didn't load as intended",
			len(got.permanodes), len(got.claimBack), len(got.files))
	}
}
