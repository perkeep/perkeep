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
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"unique"
	"unsafe"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/types/camtypes"
	"tailscale.com/util/set"
)

func TestCorpusBlobMeta(t *testing.T) {
	if got := (&corpusBlobMeta{}).blobMeta(); got != (camtypes.BlobMeta{}) {
		t.Fatalf("zero record = %+v; want zero BlobMeta", got)
	}
	types := []schema.CamliType{
		"", schema.TypeBytes, schema.TypeClaim, schema.TypeDirectory,
		schema.TypeFIFO, schema.TypeFile, schema.TypeInode, schema.TypeKeep,
		schema.TypePermanode, schema.TypeShare, schema.TypeSocket,
		schema.TypeStaticSet, schema.TypeSymlink, "future-schema-type",
	}
	for _, typ := range types {
		t.Run(string(typ), func(t *testing.T) {
			want := camtypes.BlobMeta{Ref: blob.RefFromString(string(typ)), Size: 1234, CamliType: typ}
			meta := newCorpusBlobMeta(want)
			if got := meta.blobMeta(); got != want {
				t.Fatalf("round trip = %+v; want %+v", got, want)
			}
			if typ == "" {
				if meta.camliType != (unique.Handle[schema.CamliType]{}) {
					t.Fatal("empty type did not use zero handle")
				}
			} else {
				if meta.camliType != unique.Make(typ) {
					t.Fatal("type handle is not canonical")
				}
				if typ != "future-schema-type" && meta.camliType != commonCamliTypeHandles[typ] {
					t.Fatal("known type is missing its cached handle")
				}
			}
		})
	}
	if unsafe.Sizeof(uintptr(0)) == 8 {
		if got := unsafe.Sizeof(corpusBlobMeta{}); got != 32 {
			t.Errorf("64-bit corpusBlobMeta size = %d; want 32", got)
		}
	}
}

func TestCorpusBlobMetaAPIs(t *testing.T) {
	c := newCorpus()
	want := make(map[blob.Ref]camtypes.BlobMeta)
	for _, typ := range []schema.CamliType{"", schema.TypeFile, schema.TypePermanode, "future-schema-type"} {
		bm := camtypes.BlobMeta{Ref: blob.RefFromString(string(typ)), Size: 1234, CamliType: typ}
		want[bm.Ref] = bm
		if err := c.mergeBlobMeta(bm); err != nil {
			t.Fatal(err)
		}
		if typ != "" && c.camBlobs[typ][bm.Ref] != c.blobs[bm.Ref] {
			t.Fatal("blobs and camBlobs did not share the compact record")
		}
		got, err := c.GetBlobMeta(context.Background(), bm.Ref)
		if err != nil || got != bm {
			t.Fatalf("GetBlobMeta = (%+v, %v); want (%+v, nil)", got, err, bm)
		}
	}
	if got, want := c.sumBlobBytes, int64(1234*len(want)); got != want {
		t.Errorf("sumBlobBytes = %d; want %d", got, want)
	}
	missing := blob.RefFromString("missing")
	if got, err := c.GetBlobMeta(context.Background(), missing); !errors.Is(err, os.ErrNotExist) || got != (camtypes.BlobMeta{}) {
		t.Fatalf("missing GetBlobMeta = (%+v, %v); want (zero, os.ErrNotExist)", got, err)
	}
	pn := blob.RefFromString(string(schema.TypePermanode))
	c.permanodesSetByNodeType["test-node"] = set.Set[blob.Ref]{pn: {}}
	filteredTypes := map[string]schema.CamliType{
		"File":       schema.TypeFile,
		"Unknown":    "future-schema-type",
		"Single":     schema.TypePermanode,
		"Permanodes": schema.TypePermanode,
		"NodeTypes":  schema.TypePermanode,
	}
	for _, tc := range []struct {
		name      string
		enumerate func(func(camtypes.BlobMeta) bool)
		wantCount int
	}{
		{"All", c.EnumerateBlobMeta, 4},
		{"Schema", func(fn func(camtypes.BlobMeta) bool) { c.EnumerateCamliBlobs("", fn) }, 3},
		{"File", func(fn func(camtypes.BlobMeta) bool) { c.EnumerateCamliBlobs(schema.TypeFile, fn) }, 1},
		{"Unknown", func(fn func(camtypes.BlobMeta) bool) { c.EnumerateCamliBlobs("future-schema-type", fn) }, 1},
		{"Single", func(fn func(camtypes.BlobMeta) bool) { c.EnumerateSingleBlob(fn, pn) }, 1},
		{"Missing", func(fn func(camtypes.BlobMeta) bool) { c.EnumerateSingleBlob(fn, missing) }, 0},
		{"Permanodes", func(fn func(camtypes.BlobMeta) bool) { c.enumeratePermanodes(fn, []pnAndTime{{pn: pn}}) }, 1},
		{"NodeTypes", func(fn func(camtypes.BlobMeta) bool) { c.EnumeratePermanodesByNodeTypes(fn, []string{"test-node"}) }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(map[blob.Ref]bool)
			tc.enumerate(func(bm camtypes.BlobMeta) bool {
				if expected, ok := want[bm.Ref]; !ok || bm != expected {
					t.Errorf("enumerated %+v; want %+v", bm, expected)
				}
				if typ, ok := filteredTypes[tc.name]; ok && bm.CamliType != typ {
					t.Errorf("enumerated type %q; want %q", bm.CamliType, typ)
				}
				if tc.name == "Schema" && bm.CamliType == "" {
					t.Error("schema enumeration included a non-schema blob")
				}
				if seen[bm.Ref] {
					t.Errorf("duplicate blob %v", bm.Ref)
				}
				seen[bm.Ref] = true
				return true
			})
			if len(seen) != tc.wantCount {
				t.Errorf("enumerated %d blobs; want %d", len(seen), tc.wantCount)
			}
			calls := 0
			tc.enumerate(func(camtypes.BlobMeta) bool { calls++; return false })
			if calls != min(1, tc.wantCount) {
				t.Errorf("early stop called %d times; want %d", calls, min(1, tc.wantCount))
			}
		})
	}
}

var corpusBlobMetaSink *corpusBlobMeta
var publicBlobMetaSink *camtypes.BlobMeta
var expandedBlobMetaSink camtypes.BlobMeta

func BenchmarkCorpusBlobMeta(b *testing.B) {
	for _, typ := range []schema.CamliType{"", schema.TypeFile, "future-schema-type"} {
		name := string(typ)
		if name == "" {
			name = "Empty"
		}
		bm := camtypes.BlobMeta{Ref: blob.RefFromString(name), Size: 1234, CamliType: typ}
		b.Run(name+"/Public", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				copy := bm
				publicBlobMetaSink = &copy
			}
		})
		b.Run(name+"/Compact", func(b *testing.B) {
			warm := newCorpusBlobMeta(bm)
			b.ReportAllocs()
			for b.Loop() {
				corpusBlobMetaSink = newCorpusBlobMeta(bm)
			}
			runtime.KeepAlive(warm)
		})
		b.Run(name+"/Expand", func(b *testing.B) {
			meta := newCorpusBlobMeta(bm)
			b.ReportAllocs()
			for b.Loop() {
				expandedBlobMetaSink = meta.blobMeta()
			}
		})
	}
}
