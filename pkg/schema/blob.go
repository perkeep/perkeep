/*
Copyright 2013 Google Inc.

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

package schema

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"camlistore.org/pkg/blobref"
)

// AnyBlob represents any type of schema blob.
type AnyBlob interface {
	Blob() *Blob
}

// Buildable returns a Builder from a base.
type Buildable interface {
	Builder() *Builder
}

// A Blob represents a Camlistore schema blob.
// It is immutable.
type Blob struct {
	br  *blobref.BlobRef
	str string
	ss  *Superset
}

// Type returns the blob's "camliType" field.
func (b *Blob) Type() string { return b.ss.Type }

// BlobRef returns the schema blob's blobref.
func (b *Blob) BlobRef() *blobref.BlobRef { return b.br }

// JSON returns the JSON bytes of the schema blob.
func (b *Blob) JSON() string { return b.str }

// Blob returns itself, so it satisifies the AnyBlob interface.
func (b *Blob) Blob() *Blob { return b }

// PartsSize returns the number of bytes represented by the "parts" field.
// TODO: move this off *Blob to a specialized type.
func (b *Blob) PartsSize() int64 {
	n := int64(0)
	for _, part := range b.ss.Parts {
		n += int64(part.Size)
	}
	return n
}

// FileName returns the file, directory, or symlink's filename, or the empty string.
// TODO: move this off *Blob to a specialized type.
func (b *Blob) FileName() string {
	return b.ss.FileNameString()
}

// ByteParts returns the "parts" field. The caller owns the returned
// slice.
func (b *Blob) ByteParts() []BytesPart {
	// TODO: move this method off Blob, and make the caller go
	// through a (*Blob).ByteBackedBlob() comma-ok accessor first.
	s := make([]BytesPart, len(b.ss.Parts))
	for i, part := range b.ss.Parts {
		s[i] = *part
	}
	return s
}

func (b *Blob) Builder() *Builder {
	var m map[string]interface{}
	err := json.Unmarshal([]byte(b.str), &m)
	if err != nil {
		panic("failed to decode previously-thought-valid Blob's JSON: " + err.Error())
	}
	return &Builder{m}
}

// AsClaim returns a Claim if the receiver Blob has all the required fields.
func (b *Blob) AsClaim() (c Claim, ok bool) {
	if blobref.Parse(b.ss.Signer) != nil && b.ss.Sig != "" && b.ss.ClaimType != "" && b.ss.ClaimDate != "" {
		return Claim{b}, true
	}
	return
}

// DirectoryEntries the "entries" field if valid and b's type is "directory", else
// it returns nil
func (b *Blob) DirectoryEntries() *blobref.BlobRef {
	if b.Type() != "directory" {
		return nil
	}
	return blobref.Parse(b.ss.Entries)
}

func (b *Blob) StaticSetMembers() []*blobref.BlobRef {
	if b.Type() != "static-set" {
		return nil
	}
	s := make([]*blobref.BlobRef, 0, len(b.ss.Members))
	for _, refstr := range b.ss.Members {
		if ref := blobref.Parse(refstr); ref != nil {
			s = append(s, ref)
		}
	}
	return s
}

// ModTime returns the "unixMtime" field, or the zero time.
func (b *Blob) ModTime() time.Time { return b.ss.ModTime() }

// A Claim is a Blob that is signed.
type Claim struct {
	b *Blob
}

// Blob returns the claim's Blob.
func (c Claim) Blob() *Blob { return c.b }

// ClaimDate returns the blob's "claimDate" field.
func (c Claim) ClaimDateString() string { return c.b.ss.ClaimDate }

// ClaimType returns the blob's "claimType" field.
func (c Claim) ClaimType() string { return c.b.ss.ClaimType }

// Attribute returns the "attribute" field, if set.
func (c Claim) Attribute() string { return c.b.ss.Attribute }

// Value returns the "value" field, if set.
func (c Claim) Value() string { return c.b.ss.Value }

// ModifiedPermanode returns the claim's "permaNode" field, if it's
// a claim that modifies a permanode. Otherwise nil is returned.
func (c Claim) ModifiedPermanode() *blobref.BlobRef {
	return blobref.Parse(c.b.ss.Permanode)
}

// A Builder builds a JSON blob.
// After mutating the Builder, call Blob to get the built blob.
type Builder struct {
	m map[string]interface{}
}

// NewBuilder returns a new blob schema builder.
// The "camliVersion" field is set to "1" by default and the required
// "camliType" field is NOT set.
func NewBuilder() *Builder {
	return &Builder{map[string]interface{}{
		"camliVersion": "1",
	}}
}

// SetRawStringField sets a raw string field in the underlying map.
func (bb *Builder) SetRawStringField(key, value string) *Builder {
	bb.m[key] = value
	return bb
}

// Blob builds the Blob. The builder continues to be usable after a call to Build.
func (bb *Builder) Blob() *Blob {
	json, err := mapJSON(bb.m)
	if err != nil {
		panic(err)
	}
	ss, err := ParseSuperset(strings.NewReader(json))
	if err != nil {
		panic(err)
	}
	h := blobref.NewHash()
	h.Write([]byte(json))
	return &Blob{
		str: json,
		ss:  ss,
		br:  blobref.FromHash(h),
	}
}

// Builder returns a clone of itself and satisifies the Buildable interface.
func (bb *Builder) Builder() *Builder {
	return &Builder{clone(bb.m).(map[string]interface{})}
}

// JSON returns the JSON of the blob as built so far.
func (bb *Builder) JSON() (string, error) {
	return mapJSON(bb.m)
}

// SetSigner sets the camliSigner field.
func (bb *Builder) SetSigner(signer *blobref.BlobRef) *Builder {
	bb.m["camliSigner"] = signer.String()
	return bb
}

// SetType sets the camliType field.
func (bb *Builder) SetType(t string) *Builder {
	bb.m["camliType"] = t
	return bb
}

// Type returns the camliType value.
func (bb *Builder) Type() string {
	if s, ok := bb.m["camliType"].(string); ok {
		return s
	}
	return ""
}

// SetFileName sets the fileName or fileNameBytes field.
// The filename is truncated to just the base.
func (bb *Builder) SetFileName(name string) *Builder {
	baseName := filepath.Base(name)
	if utf8.ValidString(baseName) {
		bb.m["fileName"] = baseName
	} else {
		bb.m["fileNameBytes"] = []uint8(baseName)
	}
	return bb
}

// SetSymlinkTarget sets bb to be of type "symlink" and sets the symlink's target.
func (bb *Builder) SetSymlinkTarget(target string) *Builder {
	bb.SetType("symlink")
	if utf8.ValidString(target) {
		bb.m["symlinkTarget"] = target
	} else {
		bb.m["symlinkTargetBytes"] = []uint8(target)
	}
	return bb
}

// SetClaimDate sets the "claimDate" on a claim.
// It is a fatal error to call SetClaimDate if the Map isn't of Type "claim".
func (bb *Builder) SetClaimDate(t time.Time) *Builder {
	if t := bb.Type(); t != "claim" {
		// This is a little gross, using panic here, but I
		// don't want all callers to check errors.  This is
		// really a programming error, not a runtime error
		// that would arise from e.g. random user data.
		panic("SetClaimDate called on non-claim *Builder; camliType=" + t)
	}
	bb.m["claimDate"] = RFC3339FromTime(t)
	return bb
}

// SetModTime sets the "unixMtime" field.
func (bb *Builder) SetModTime(t time.Time) *Builder {
	bb.m["unixMtime"] = RFC3339FromTime(t)
	return bb
}

// ModTime returns the "unixMtime" modtime field, if set.
func (bb *Builder) ModTime() (t time.Time, ok bool) {
	s, ok := bb.m["unixMtime"].(string)
	if !ok {
		return
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return
	}
	return t, true
}

// PopulateDirectoryMap sets the type of *Builder to "directory" and sets
// the "entries" field to the provided staticSet blobref.
func (bb *Builder) PopulateDirectoryMap(staticSetRef *blobref.BlobRef) *Builder {
	bb.m["camliType"] = "directory"
	bb.m["entries"] = staticSetRef.String()
	return bb
}

// PartsSize returns the number of bytes represented by the "parts" field.
func (bb *Builder) PartsSize() int64 {
	n := int64(0)
	if parts, ok := bb.m["parts"].([]BytesPart); ok {
		for _, part := range parts {
			n += int64(part.Size)
		}
	}
	return n
}

func clone(i interface{}) interface{} {
	switch t := i.(type) {
	case map[string]interface{}:
		m2 := make(map[string]interface{})
		for k, v := range t {
			m2[k] = clone(v)
		}
		return m2
	case string, int, int64, float64:
		return t
	case []interface{}:
		s2 := make([]interface{}, len(t))
		for i, v := range t {
			s2[i] = clone(v)
		}
		return s2
	}
	panic(fmt.Sprintf("unsupported clone type %T", i))
}
