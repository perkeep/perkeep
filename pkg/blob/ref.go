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
	"crypto/sha1"
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

// digestType is an interface type, but any type implementing it must
// be of concrete type [N]byte, so it supports equality with ==,
// which is a requirement for ref.
type digestType interface {
	bytes() []byte
	digestName() string
}

func (r Ref) String() string {
	if r.digest == nil {
		return "<invalid-blob.Ref>"
	}
	// TODO: maybe memoize this.
	dname := r.digest.digestName()
	bs := r.digest.bytes()
	buf := make([]byte, 0, len(dname)+1+len(bs)*2)
	buf = append(buf, dname...)
	buf = append(buf, '-')
	for _, b := range bs {
		buf = append(buf, hexDigit[b>>4], hexDigit[b&0xf])
	}
	return string(buf)
}

const hexDigit = "0123456789abcdef"

func (r *Ref) Valid() bool { return r.digest != nil }

func (r *Ref) IsSupported() bool {
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
		return parseUnknown(s)
	}
	buf := getBuf(meta.size)
	defer putBuf(buf)
	if len(hex) != len(buf)*2 {
		return
	}
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

// parseUnknown parses s where s is a blobref of a digest type not known
// to this server. e.g. ("foo-ababab")
func parseUnknown(s string) (ref Ref, ok bool) {
	panic("TODO")
}

func fromSHA1Bytes(b []byte) digestType {
	var a sha1Digest
	if len(b) != len(a) {
		panic("bogus sha-1 length")
	}
	copy(a[:], b)
	return a
}

// FromHash returns a blobref representing the given hash.
// It panics if the hash isn't of a known type.
func FromHash(h hash.Hash) Ref {
	meta, ok := metaFromType[reflect.TypeOf(h)]
	if !ok {
		panic(fmt.Sprintf("Currently-unsupported hash type %T", h))
	}
	return Ref{meta.ctor(h.Sum(nil))}
}

// SHA1FromString returns a SHA-1 blobref of the provided string.
func SHA1FromString(s string) Ref {
	s1 := sha1.New()
	s1.Write([]byte(s))
	return FromHash(s1)
}

// SHA1FromBytes returns a SHA-1 blobref of the provided bytes.
func SHA1FromBytes(b []byte) Ref {
	s1 := sha1.New()
	s1.Write(b)
	return FromHash(s1)
}

type sha1Digest [20]byte

type otherDigest struct {
	name   string
	sum    [128]byte
	sumLen int // bytes in sum that are valid
}

func (s sha1Digest) digestName() string { return "sha1" }
func (s sha1Digest) bytes() []byte      { return s[:] }

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
