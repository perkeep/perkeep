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
	"unique"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/types/camtypes"
)

// corpusBlobMeta is the compact, long-lived representation of a BlobMeta.
// On 64-bit platforms it is 32 bytes rather than 40, reducing each separately
// allocated record from the 48-byte size class to the 32-byte size class.
// A zero camliType handle represents a non-schema blob.
type corpusBlobMeta struct {
	Ref       blob.Ref
	Size      uint32
	camliType unique.Handle[schema.CamliType]
}

// commonCamliTypeHandles avoids unique.Make's global table lookups for the
// schema types used by almost all blobs. This map is immutable after init.
var commonCamliTypeHandles = func() map[schema.CamliType]unique.Handle[schema.CamliType] {
	types := []schema.CamliType{
		schema.TypeBytes,
		schema.TypeClaim,
		schema.TypeDirectory,
		schema.TypeFIFO,
		schema.TypeFile,
		schema.TypeInode,
		schema.TypeKeep,
		schema.TypePermanode,
		schema.TypeShare,
		schema.TypeSocket,
		schema.TypeStaticSet,
		schema.TypeSymlink,
	}
	m := make(map[schema.CamliType]unique.Handle[schema.CamliType], len(types))
	for _, typ := range types {
		m[typ] = unique.Make(typ)
	}
	return m
}()

func camliTypeHandle(typ schema.CamliType) unique.Handle[schema.CamliType] {
	if typ == "" {
		return unique.Handle[schema.CamliType]{}
	}
	if h, ok := commonCamliTypeHandles[typ]; ok {
		return h
	}
	// The index accepts arbitrary camliType names, not just the known types.
	return unique.Make(typ)
}

func newCorpusBlobMeta(bm camtypes.BlobMeta) *corpusBlobMeta {
	return &corpusBlobMeta{
		Ref:       bm.Ref,
		Size:      bm.Size,
		camliType: camliTypeHandle(bm.CamliType),
	}
}

// blobMeta expands the record at the corpus API boundary, preserving callers'
// string-based CamliType and the public BlobMeta's zero-value behavior.
func (bm *corpusBlobMeta) blobMeta() camtypes.BlobMeta {
	meta := camtypes.BlobMeta{Ref: bm.Ref, Size: bm.Size}
	if bm.camliType != (unique.Handle[schema.CamliType]{}) {
		meta.CamliType = bm.camliType.Value()
	}
	return meta
}
