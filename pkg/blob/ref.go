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

// Package blob defines types to refer to and retrieve low-level Camlistore blobs.
package blob

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"hash"
	"reflect"
	"regexp"
	"strings"
)

// Pattern is the regular expression which matches a blobref.
// It does not contain ^ or $.
const Pattern = `\b([a-z][a-z0-9]*)-([a-f0-9]+)\b`

// whole blobref pattern
var blobRefPattern = regexp.MustCompile("^" + Pattern + "$")

// Ref is a reference to a Camlistore blob.
// It is used as a value type and supports equality (with ==) and the ability
// to use it as a map key.
type Ref struct {
	digest digestType
}

// SizedRef is like a Ref but includes a size.
// It should also be used as a value type and supports equality.
type SizedRef struct {
	Ref
	Size int64
}

func (sr SizedRef) String() string {
	return fmt.Sprintf("[%s; %d bytes]", sr.Ref.String(), sr.Size)
}

// digestType is an interface type, but any type implementing it must
// be of concrete type [N]byte, so it supports equality with ==,
// which is a requirement for ref.
type digestType interface {
	bytes() []byte
	digestName() string
	newHash() hash.Hash
}

func (r Ref) String() string {
	if r.digest == nil {
		return "<invalid-blob.Ref>"
	}
	// TODO: maybe memoize this.
	dname := r.digest.digestName()
	bs := r.digest.bytes()
	buf := getBuf(len(dname) + 1 + len(bs)*2)[:0]
	defer putBuf(buf)
	return string(r.appendString(buf))
}

func (r Ref) appendString(buf []byte) []byte {
	dname := r.digest.digestName()
	bs := r.digest.bytes()
	buf = append(buf, dname...)
	buf = append(buf, '-')
	for _, b := range bs {
		buf = append(buf, hexDigit[b>>4], hexDigit[b&0xf])
	}
	if o, ok := r.digest.(otherDigest); ok && o.odd {
		buf = buf[:len(buf)-1]
	}
	return buf
}

// HashName returns the lowercase hash function name of the reference.
// It panics if r is zero.
func (r Ref) HashName() string {
	if r.digest == nil {
		panic("HashName called on invalid Ref")
	}
	return r.digest.digestName()
}

// Digest returns the lower hex digest of the blobref, without
// the e.g. "sha1-" prefix. It panics if r is zero.
func (r Ref) Digest() string {
	if r.digest == nil {
		panic("Digest called on invalid Ref")
	}
	bs := r.digest.bytes()
	buf := getBuf(len(bs) * 2)[:0]
	defer putBuf(buf)
	for _, b := range bs {
		buf = append(buf, hexDigit[b>>4], hexDigit[b&0xf])
	}
	if o, ok := r.digest.(otherDigest); ok && o.odd {
		buf = buf[:len(buf)-1]
	}
	return string(buf)
}

func (r Ref) DigestPrefix(digits int) string {
	v := r.Digest()
	if len(v) < digits {
		return v
	}
	return v[:digits]
}

func (r Ref) DomID() string {
	if !r.Valid() {
		return ""
	}
	return "camli-" + r.String()
}

func (r Ref) Sum32() uint32 {
	var v uint32
	for _, b := range r.digest.bytes()[:4] {
		v = v<<8 | uint32(b)
	}
	return v
}

func (r Ref) Sum64() uint64 {
	var v uint64
	for _, b := range r.digest.bytes()[:8] {
		v = v<<8 | uint64(b)
	}
	return v
}

// Hash returns a new hash.Hash of r's type.
// It panics if r is zero.
func (r Ref) Hash() hash.Hash {
	return r.digest.newHash()
}

func (r Ref) HashMatches(h hash.Hash) bool {
	if r.digest == nil {
		return false
	}
	return bytes.Equal(h.Sum(nil), r.digest.bytes())
}

const hexDigit = "0123456789abcdef"

func (r Ref) Valid() bool { return r.digest != nil }

func (r Ref) IsSupported() bool {
	if !r.Valid() {
		return false
	}
	_, ok := metaFromString[r.digest.digestName()]
	return ok
}

// Parse parse s as a blobref and returns the ref and whether it was
// parsed successfully.
func Parse(s string) (ref Ref, ok bool) {
	i := strings.Index(s, "-")
	if i < 0 {
		return
	}
	name := s[:i] // e.g. "sha1"
	hex := s[i+1:]
	meta, ok := metaFromString[name]
	if !ok {
		return parseUnknown(name, hex)
	}
	if len(hex) != meta.size*2 {
		ok = false
		return
	}
	buf := getBuf(meta.size)
	defer putBuf(buf)
	bad := false
	for i := 0; i < len(hex); i += 2 {
		buf[i/2] = hexVal(hex[i], &bad)<<4 | hexVal(hex[i+1], &bad)
	}
	if bad {
		return
	}
	return Ref{meta.ctor(buf)}, true
}

// Parse parse s as a blobref. If s is invalid, a zero Ref is returned
// which can be tested with the Valid method.
func ParseOrZero(s string) Ref {
	ref, ok := Parse(s)
	if !ok {
		return Ref{}
	}
	return ref
}

// MustParse parse s as a blobref and panics on failure.
func MustParse(s string) Ref {
	ref, ok := Parse(s)
	if !ok {
		panic("Invalid blobref " + s)
	}
	return ref
}

// '0' => 0 ... 'f' => 15, else sets *bad to true.
func hexVal(b byte, bad *bool) byte {
	if '0' <= b && b <= '9' {
		return b - '0'
	}
	if 'a' <= b && b <= 'f' {
		return b - 'a' + 10
	}
	*bad = true
	return 0
}

func validDigestName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if 'a' <= r && r <= 'z' {
			continue
		}
		if '0' <= r && r <= '9' {
			continue
		}
		return false
	}
	return true
}

// parseUnknown parses a blobref where the digest type isn't known to this server.
// e.g. ("foo-ababab")
func parseUnknown(digest, hex string) (ref Ref, ok bool) {
	if !validDigestName(digest) {
		return
	}

	// TODO: remove this short hack and don't allow odd numbers of hex digits.
	odd := false
	if len(hex)%2 != 0 {
		hex += "0"
		odd = true
	}

	if len(hex) < 2 || len(hex)%2 != 0 || len(hex) > maxOtherDigestLen*2 {
		return
	}
	o := otherDigest{
		name:   digest,
		sumLen: len(hex) / 2,
		odd:    odd,
	}
	bad := false
	for i := 0; i < len(hex); i += 2 {
		o.sum[i/2] = hexVal(hex[i], &bad)<<4 | hexVal(hex[i+1], &bad)
	}
	if bad {
		return
	}
	return Ref{o}, true
}

func fromSHA1Bytes(b []byte) digestType {
	var a sha1Digest
	if len(b) != len(a) {
		panic("bogus sha-1 length")
	}
	copy(a[:], b)
	return a
}

// RefFromHash returns a blobref representing the given hash.
// It panics if the hash isn't of a known type.
func RefFromHash(h hash.Hash) Ref {
	meta, ok := metaFromType[reflect.TypeOf(h)]
	if !ok {
		panic(fmt.Sprintf("Currently-unsupported hash type %T", h))
	}
	return Ref{meta.ctor(h.Sum(nil))}
}

// RefFromString returns a blobref from the given string, for the currently
// recommended hash function
func RefFromString(s string) Ref {
	return SHA1FromString(s)
}

// SHA1FromString returns a SHA-1 blobref of the provided string.
func SHA1FromString(s string) Ref {
	s1 := sha1.New()
	s1.Write([]byte(s))
	return RefFromHash(s1)
}

// SHA1FromBytes returns a SHA-1 blobref of the provided bytes.
func SHA1FromBytes(b []byte) Ref {
	s1 := sha1.New()
	s1.Write(b)
	return RefFromHash(s1)
}

type sha1Digest [20]byte

func (s sha1Digest) digestName() string { return "sha1" }
func (s sha1Digest) bytes() []byte      { return s[:] }
func (s sha1Digest) newHash() hash.Hash { return sha1.New() }

const maxOtherDigestLen = 128

type otherDigest struct {
	name   string
	sum    [maxOtherDigestLen]byte
	sumLen int  // bytes in sum that are valid
	odd    bool // odd number of hex digits in input
}

func (d otherDigest) digestName() string { return d.name }
func (d otherDigest) bytes() []byte      { return d.sum[:d.sumLen] }
func (d otherDigest) newHash() hash.Hash { return nil }

var sha1Meta = &digestMeta{
	ctor: fromSHA1Bytes,
	size: sha1.Size,
}

var metaFromString = map[string]*digestMeta{
	"sha1": sha1Meta,
}

var sha1Type = reflect.TypeOf(sha1.New())

var metaFromType = map[reflect.Type]*digestMeta{
	sha1Type: sha1Meta,
}

type digestMeta struct {
	ctor func(b []byte) digestType
	size int // bytes of digest
}

func getBuf(size int) []byte {
	// TODO: pool
	return make([]byte, size)
}

func putBuf(b []byte) {
	// TODO: pool
}

// NewHash returns a new hash.Hash of the currently recommended hash type.
// Currently this is just SHA-1, but will likely change within the next
// year or so.
func NewHash() hash.Hash {
	return sha1.New()
}

func ValidRefString(s string) bool {
	// TODO: optimize to not allocate
	return ParseOrZero(s).Valid()
}

func (r *Ref) UnmarshalJSON(d []byte) error {
	if r.digest != nil {
		return errors.New("Can't UnmarshalJSON into a non-zero Ref")
	}
	if len(d) < 2 || d[0] != '"' || d[len(d)-1] != '"' {
		return fmt.Errorf("blob: expecting a JSON string to unmarshal, got %q", d)
	}
	refStr := string(d[1 : len(d)-1])
	p, ok := Parse(refStr)
	if !ok {
		return fmt.Errorf("blobref: invalid blobref %q (%d)", refStr, len(refStr))
	}
	*r = p
	return nil
}

func (r Ref) MarshalJSON() ([]byte, error) {
	dname := r.digest.digestName()
	bs := r.digest.bytes()
	buf := make([]byte, 0, 3+len(dname)+len(bs)*2)
	buf = append(buf, '"')
	buf = r.appendString(buf)
	buf = append(buf, '"')
	return buf, nil
}

// MarshalBinary implements Go's encoding.BinaryMarshaler interface.
func (r Ref) MarshalBinary() (data []byte, err error) {
	dname := r.digest.digestName()
	bs := r.digest.bytes()
	data = make([]byte, 0, len(dname)+1+len(bs))
	data = append(data, dname...)
	data = append(data, '-')
	data = append(data, bs...)
	return
}

// UnmarshalBinary implements Go's encoding.BinaryUnmarshaler interface.
func (r *Ref) UnmarshalBinary(data []byte) error {
	if r.digest != nil {
		return errors.New("Can't UnmarshalBinary into a non-zero Ref")
	}
	i := bytes.IndexByte(data, '-')
	if i < 1 {
		return errors.New("no digest name")
	}

	digName := string(data[:i])
	buf := data[i+1:]

	meta, ok := metaFromString[digName]
	if !ok {
		r2, ok := parseUnknown(digName, fmt.Sprintf("%x", buf))
		if !ok {
			return errors.New("invalid blobref binary data")
		}
		*r = r2
		return nil
	}
	if len(buf) != meta.size {
		return errors.New("wrong size of data for digest " + digName)
	}
	r.digest = meta.ctor(buf)
	return nil
}
