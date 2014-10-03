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

	"camlistore.org/pkg/blob"
)

// A MissingFieldError represents a missing JSON field in a schema blob.
type MissingFieldError string

func (e MissingFieldError) Error() string {
	return fmt.Sprintf("schema: missing field %q", string(e))
}

// IsMissingField returns whether error is of type MissingFieldError.
func IsMissingField(err error) bool {
	_, ok := err.(MissingFieldError)
	return ok
}

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
	br  blob.Ref
	str string
	ss  *superset
}

// Type returns the blob's "camliType" field.
func (b *Blob) Type() string { return b.ss.Type }

// BlobRef returns the schema blob's blobref.
func (b *Blob) BlobRef() blob.Ref { return b.br }

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

// ClaimDate returns the "claimDate" field.
// If there is no claimDate, the error will be a MissingFieldError.
func (b *Blob) ClaimDate() (time.Time, error) {
	var ct time.Time
	claimDate := b.ss.ClaimDate
	if claimDate.IsZero() {
		return ct, MissingFieldError("claimDate")
	}
	return claimDate.Time(), nil
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
	dec := json.NewDecoder(strings.NewReader(b.str))
	dec.UseNumber()
	err := dec.Decode(&m)
	if err != nil {
		panic("failed to decode previously-thought-valid Blob's JSON: " + err.Error())
	}
	return &Builder{m}
}

// AsClaim returns a Claim if the receiver Blob has all the required fields.
func (b *Blob) AsClaim() (c Claim, ok bool) {
	if b.ss.Signer.Valid() && b.ss.Sig != "" && b.ss.ClaimType != "" && !b.ss.ClaimDate.IsZero() {
		return Claim{b}, true
	}
	return
}

// AsShare returns a Share if the receiver Blob has all the required fields.
func (b *Blob) AsShare() (s Share, ok bool) {
	c, isClaim := b.AsClaim()
	if !isClaim {
		return
	}

	if ClaimType(b.ss.ClaimType) == ShareClaim && b.ss.AuthType == ShareHaveRef && (b.ss.Target.Valid() || b.ss.Search != nil) {
		return Share{c}, true
	}
	return s, false
}

// DirectoryEntries the "entries" field if valid and b's type is "directory".
func (b *Blob) DirectoryEntries() (br blob.Ref, ok bool) {
	if b.Type() != "directory" {
		return
	}
	return b.ss.Entries, true
}

func (b *Blob) StaticSetMembers() []blob.Ref {
	if b.Type() != "static-set" {
		return nil
	}
	s := make([]blob.Ref, 0, len(b.ss.Members))
	for _, ref := range b.ss.Members {
		if ref.Valid() {
			s = append(s, ref)
		}
	}
	return s
}

func (b *Blob) ShareAuthType() string {
	s, ok := b.AsShare()
	if !ok {
		return ""
	}
	return s.AuthType()
}

func (b *Blob) ShareTarget() blob.Ref {
	s, ok := b.AsShare()
	if !ok {
		return blob.Ref{}
	}
	return s.Target()
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
func (c Claim) ClaimDateString() string { return c.b.ss.ClaimDate.String() }

// ClaimType returns the blob's "claimType" field.
func (c Claim) ClaimType() string { return c.b.ss.ClaimType }

// Attribute returns the "attribute" field, if set.
func (c Claim) Attribute() string { return c.b.ss.Attribute }

// Value returns the "value" field, if set.
func (c Claim) Value() string { return c.b.ss.Value }

// ModifiedPermanode returns the claim's "permaNode" field, if it's
// a claim that modifies a permanode. Otherwise a zero blob.Ref is
// returned.
func (c Claim) ModifiedPermanode() blob.Ref {
	return c.b.ss.Permanode
}

// Target returns the blob referenced by the Share if it's
// a ShareClaim claim, or the object being deleted if it's a
// DeleteClaim claim.
// Otherwise a zero blob.Ref is returned.
func (c Claim) Target() blob.Ref {
	return c.b.ss.Target
}

// A Share is a claim for giving access to a user's blob(s).
// When returned from (*Blob).AsShare, it always represents
// a valid share with all required fields.
type Share struct {
	Claim
}

// AuthType returns the AuthType of the Share.
func (s Share) AuthType() string {
	return s.b.ss.AuthType
}

// IsTransitive returns whether the Share transitively
// gives access to everything reachable from the referenced
// blob.
func (s Share) IsTransitive() bool {
	return s.b.ss.Transitive
}

// IsExpired reports whether this share has expired.
func (s Share) IsExpired() bool {
	t := time.Time(s.b.ss.Expires)
	return !t.IsZero() && clockNow().After(t)
}

// A StaticFile is a Blob representing a file, symlink fifo or socket
// (or device file, when support for these is added).
type StaticFile struct {
	b *Blob
}

// FileName returns the StaticFile's FileName if is not the empty string, otherwise it returns its FileNameBytes concatenated into a string.
func (sf StaticFile) FileName() string {
	return sf.b.ss.FileNameString()
}

// AsStaticFile returns the Blob as a StaticFile if it represents
// one. Otherwise, it returns false in the boolean parameter and the
// zero value of StaticFile.
func (b *Blob) AsStaticFile() (sf StaticFile, ok bool) {
	// TODO (marete) Add support for device files to
	// Camlistore and change the implementation of StaticFile to
	// reflect that.
	t := b.ss.Type
	if t == "file" || t == "symlink" || t == "fifo" || t == "socket" {
		return StaticFile{b}, true
	}

	return
}

// A StaticFIFO is a StaticFile that is also a fifo.
type StaticFIFO struct {
	StaticFile
}

// A StaticSocket is a StaticFile that is also a socket.
type StaticSocket struct {
	StaticFile
}

// A StaticSymlink is a StaticFile that is also a symbolic link.
type StaticSymlink struct {
	// We name it `StaticSymlink' rather than just `Symlink' since
	// a type called Symlink is already in schema.go.
	StaticFile
}

// SymlinkTargetString returns the field symlinkTarget if is
// non-empty. Otherwise it returns the contents of symlinkTargetBytes
// concatenated as a string.
func (sl StaticSymlink) SymlinkTargetString() string {
	return sl.StaticFile.b.ss.SymlinkTargetString()
}

// AsStaticSymlink returns the StaticFile as a StaticSymlink if the
// StaticFile represents a symlink. Othwerwise, it retuns the zero
// value of StaticSymlink and false.
func (sf StaticFile) AsStaticSymlink() (s StaticSymlink, ok bool) {
	if sf.b.ss.Type == "symlink" {
		return StaticSymlink{sf}, true
	}

	return
}

// AsStaticFIFO returns the StatifFile as a StaticFIFO if the
// StaticFile represents a fifo. Otherwise, it returns the zero value
// of StaticFIFO and false.
func (sf StaticFile) AsStaticFIFO() (fifo StaticFIFO, ok bool) {
	if sf.b.ss.Type == "fifo" {
		return StaticFIFO{sf}, true
	}

	return
}

// AsSataticSocket returns the StaticFile as a StaticSocket if the
// StaticFile represents a socket. Otherwise, it returns the zero
// value of StaticSocket and false.
func (sf StaticFile) AsStaticSocket() (ss StaticSocket, ok bool) {
	if sf.b.ss.Type == "socket" {
		return StaticSocket{sf}, true
	}

	return
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

// SetShareTarget sets the target of share claim.
// It panics if bb isn't a "share" claim type.
func (bb *Builder) SetShareTarget(t blob.Ref) *Builder {
	if bb.Type() != "claim" || bb.ClaimType() != ShareClaim {
		panic("called SetShareTarget on non-share")
	}
	bb.m["target"] = t.String()
	return bb
}

// SetShareSearch sets the search of share claim.
// q is assumed to be of type *search.SearchQuery.
// It panics if bb isn't a "share" claim type.
func (bb *Builder) SetShareSearch(q SearchQuery) *Builder {
	if bb.Type() != "claim" || bb.ClaimType() != ShareClaim {
		panic("called SetShareSearch on non-share")
	}
	bb.m["search"] = q
	return bb
}

// SetShareExpiration sets the expiration time on share claim.
// It panics if bb isn't a "share" claim type.
// If t is zero, the expiration is removed.
func (bb *Builder) SetShareExpiration(t time.Time) *Builder {
	if bb.Type() != "claim" || bb.ClaimType() != ShareClaim {
		panic("called SetShareExpiration on non-share")
	}
	if t.IsZero() {
		delete(bb.m, "expires")
	} else {
		bb.m["expires"] = RFC3339FromTime(t)
	}
	return bb
}

func (bb *Builder) SetShareIsTransitive(b bool) *Builder {
	if bb.Type() != "claim" || bb.ClaimType() != ShareClaim {
		panic("called SetShareIsTransitive on non-share")
	}
	if !b {
		delete(bb.m, "transitive")
	} else {
		bb.m["transitive"] = true
	}
	return bb
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
	ss, err := parseSuperset(strings.NewReader(json))
	if err != nil {
		panic(err)
	}
	h := blob.NewHash()
	h.Write([]byte(json))
	return &Blob{
		str: json,
		ss:  ss,
		br:  blob.RefFromHash(h),
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
// Calling SetSigner is unnecessary if using Sign.
func (bb *Builder) SetSigner(signer blob.Ref) *Builder {
	bb.m["camliSigner"] = signer.String()
	return bb
}

// SignAt sets the blob builder's camliSigner field with SetSigner
// and returns the signed JSON using the provided signer.
func (bb *Builder) Sign(signer *Signer) (string, error) {
	return bb.SignAt(signer, time.Time{})
}

// SignAt sets the blob builder's camliSigner field with SetSigner
// and returns the signed JSON using the provided signer.
// The provided sigTime is the time of the signature, used mostly
// for planned permanodes. If the zero value, the current time is used.
func (bb *Builder) SignAt(signer *Signer, sigTime time.Time) (string, error) {
	switch bb.Type() {
	case "permanode", "claim":
	default:
		return "", fmt.Errorf("can't sign camliType %q", bb.Type())
	}
	return signer.SignJSON(bb.SetSigner(signer.pubref).Blob().JSON(), sigTime)
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

// ClaimType returns the claimType value, or the empty string.
func (bb *Builder) ClaimType() ClaimType {
	if s, ok := bb.m["claimType"].(string); ok {
		return ClaimType(s)
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
		bb.m["fileNameBytes"] = mixedArrayFromString(baseName)
	}
	return bb
}

// SetSymlinkTarget sets bb to be of type "symlink" and sets the symlink's target.
func (bb *Builder) SetSymlinkTarget(target string) *Builder {
	bb.SetType("symlink")
	if utf8.ValidString(target) {
		bb.m["symlinkTarget"] = target
	} else {
		bb.m["symlinkTargetBytes"] = mixedArrayFromString(target)
	}
	return bb
}

// IsClaimType returns whether this blob builder is for a type
// which should be signed. (a "claim" or "permanode")
func (bb *Builder) IsClaimType() bool {
	switch bb.Type() {
	case "claim", "permanode":
		return true
	}
	return false
}

// SetClaimDate sets the "claimDate" on a claim.
// It is a fatal error to call SetClaimDate if the Map isn't of Type "claim".
func (bb *Builder) SetClaimDate(t time.Time) *Builder {
	if !bb.IsClaimType() {
		// This is a little gross, using panic here, but I
		// don't want all callers to check errors.  This is
		// really a programming error, not a runtime error
		// that would arise from e.g. random user data.
		panic("SetClaimDate called on non-claim *Builder; camliType=" + bb.Type())
	}
	bb.m["claimDate"] = RFC3339FromTime(t)
	return bb
}

// SetModTime sets the "unixMtime" field.
func (bb *Builder) SetModTime(t time.Time) *Builder {
	bb.m["unixMtime"] = RFC3339FromTime(t)
	return bb
}

// CapCreationTime caps the "unixCtime" field to be less or equal than "unixMtime"
func (bb *Builder) CapCreationTime() *Builder {
	ctime, ok := bb.m["unixCtime"].(string)
	if !ok {
		return bb
	}
	mtime, ok := bb.m["unixMtime"].(string)
	if ok && ctime > mtime {
		bb.m["unixCtime"] = mtime
	}
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
func (bb *Builder) PopulateDirectoryMap(staticSetRef blob.Ref) *Builder {
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
	case string, int, int64, float64, json.Number:
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
