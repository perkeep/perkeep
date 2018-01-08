/*
Copyright 2013 The Perkeep Authors

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

// Package blob defines types to refer to and retrieve low-level Perkeep blobs.
package blob // import "perkeep.org/pkg/blob"

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"reflect"
	"strings"

	"perkeep.org/internal/testhooks"
)

// Pattern is the regular expression which matches a blobref.
// It does not contain ^ or $.
const Pattern = `\b([a-z][a-z0-9]*)-([a-f0-9]+)\b`

// Ref is a reference to a Perkeep blob.
// It is used as a value type and supports equality (with ==) and the ability
// to use it as a map key.
type Ref struct {
	digest digestType
}

// SizedRef is like a Ref but includes a size.
// It should also be used as a value type and supports equality.
type SizedRef struct {
	Ref  Ref    `json:"blobRef"`
	Size uint32 `json:"size"`
}

// Less reports whether sr sorts before o. Invalid references blobs sort first.
func (sr SizedRef) Less(o SizedRef) bool {
	return sr.Ref.Less(o.Ref)
}

func (sr SizedRef) Valid() bool { return sr.Ref.Valid() }

func (sr SizedRef) HashMatches(h hash.Hash) bool { return sr.Ref.HashMatches(h) }

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
	equalString(string) bool
	hasPrefix(string) bool
}

func (r Ref) String() string {
	if r.digest == nil {
		return "<invalid-blob.Ref>"
	}
	dname := r.digest.digestName()
	bs := r.digest.bytes()
	buf := getBuf(len(dname) + 1 + len(bs)*2)[:0]
	defer putBuf(buf)
	return string(r.appendString(buf))
}

// StringMinusOne returns the first string that's before String.
func (r Ref) StringMinusOne() string {
	if r.digest == nil {
		return "<invalid-blob.Ref>"
	}
	dname := r.digest.digestName()
	bs := r.digest.bytes()
	buf := getBuf(len(dname) + 1 + len(bs)*2)[:0]
	defer putBuf(buf)
	buf = r.appendString(buf)
	buf[len(buf)-1]-- // no need to deal with carrying underflow (no 0 bytes ever)
	return string(buf)
}

// EqualString reports whether r.String() is equal to s.
// It does not allocate.
func (r Ref) EqualString(s string) bool { return r.digest.equalString(s) }

// HasPrefix reports whether s is a prefix of r.String(). It returns false if s
// does not contain at least the digest name prefix (e.g. "sha224-") and one byte of
// digest.
// It does not allocate.
func (r Ref) HasPrefix(s string) bool { return r.digest.hasPrefix(s) }

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
// the e.g. "sha224-" prefix. It panics if r is zero.
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

// ParseKnown is like Parse, but only parse blobrefs known to this
// server. It returns ok == false for well-formed but unsupported
// blobrefs.
func ParseKnown(s string) (ref Ref, ok bool) {
	return parse(s, false)
}

// Parse parse s as a blobref and returns the ref and whether it was
// parsed successfully.
func Parse(s string) (ref Ref, ok bool) {
	return parse(s, true)
}

func parse(s string, allowAll bool) (ref Ref, ok bool) {
	i := strings.Index(s, "-")
	if i < 0 {
		return
	}
	name := s[:i] // e.g. "sha1", "sha224"
	hex := s[i+1:]
	meta, ok := metaFromString[name]
	if !ok {
		if allowAll || testRefType[name] {
			return parseUnknown(name, hex)
		}
		return
	}
	if len(hex) != meta.size*2 {
		ok = false
		return
	}
	dt, ok := meta.ctors(hex)
	if !ok {
		return
	}
	return Ref{dt}, true
}

var testRefType = map[string]bool{
	"fakeref": true,
	"testref": true,
	"perma":   true,
}

// ParseBytes is like Parse, but parses from a byte slice.
func ParseBytes(s []byte) (ref Ref, ok bool) {
	i := bytes.IndexByte(s, '-')
	if i < 0 {
		return
	}
	name := s[:i] // e.g. "sha1", "sha224"
	hex := s[i+1:]
	meta, ok := metaFromBytes(name)
	if !ok {
		return parseUnknown(string(name), string(hex))
	}
	if len(hex) != meta.size*2 {
		ok = false
		return
	}
	dt, ok := meta.ctorb(hex)
	if !ok {
		return
	}
	return Ref{dt}, true
}

// ParseOrZero parses as a blobref. If s is invalid, a zero Ref is
// returned which can be tested with the Valid method.
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

func sha1FromBinary(b []byte) digestType {
	var d sha1Digest
	if len(d) != len(b) {
		panic("bogus sha-1 length")
	}
	copy(d[:], b)
	return d
}

func sha1FromHexString(hex string) (digestType, bool) {
	var d sha1Digest
	var bad bool
	for i := 0; i < len(hex); i += 2 {
		d[i/2] = hexVal(hex[i], &bad)<<4 | hexVal(hex[i+1], &bad)
	}
	if bad {
		return nil, false
	}
	return d, true
}

// yawn. exact copy of sha1FromHexString.
func sha1FromHexBytes(hex []byte) (digestType, bool) {
	var d sha1Digest
	var bad bool
	for i := 0; i < len(hex); i += 2 {
		d[i/2] = hexVal(hex[i], &bad)<<4 | hexVal(hex[i+1], &bad)
	}
	if bad {
		return nil, false
	}
	return d, true
}

func sha224FromBinary(b []byte) digestType {
	var d sha224Digest
	if len(d) != len(b) {
		panic("bogus sha-224 length")
	}
	copy(d[:], b)
	return d
}

func sha224FromHexString(hex string) (digestType, bool) {
	var d sha224Digest
	var bad bool
	for i := 0; i < len(hex); i += 2 {
		d[i/2] = hexVal(hex[i], &bad)<<4 | hexVal(hex[i+1], &bad)
	}
	if bad {
		return nil, false
	}
	return d, true
}

// yawn. exact copy of sha224FromHexString.
func sha224FromHexBytes(hex []byte) (digestType, bool) {
	var d sha224Digest
	var bad bool
	for i := 0; i < len(hex); i += 2 {
		d[i/2] = hexVal(hex[i], &bad)<<4 | hexVal(hex[i+1], &bad)
	}
	if bad {
		return nil, false
	}
	return d, true
}

// RefFromHash returns a blobref representing the given hash.
// It panics if the hash isn't of a known type.
func RefFromHash(h hash.Hash) Ref {
	meta, ok := metaFromType[hashSig{reflect.TypeOf(h), h.Size()}]
	if !ok {
		panic(fmt.Sprintf("Currently-unsupported hash type %T", h))
	}
	return Ref{meta.ctor(h.Sum(nil))}
}

// RefFromString returns a blobref from the given string, for the currently
// recommended hash function.
func RefFromString(s string) Ref {
	h := NewHash()
	io.WriteString(h, s)
	return RefFromHash(h)
}

// RefFromBytes returns a blobref from the given string, for the currently
// recommended hash function.
func RefFromBytes(b []byte) Ref {
	h := NewHash()
	h.Write(b)
	return RefFromHash(h)
}

type sha1Digest [20]byte

func (d sha1Digest) digestName() string { return "sha1" }
func (d sha1Digest) bytes() []byte      { return d[:] }
func (d sha1Digest) newHash() hash.Hash { return sha1.New() }
func (d sha1Digest) equalString(s string) bool {
	if len(s) != 45 {
		return false
	}
	if !strings.HasPrefix(s, "sha1-") {
		return false
	}
	s = s[len("sha1-"):]
	for i, b := range d[:] {
		if s[i*2] != hexDigit[b>>4] || s[i*2+1] != hexDigit[b&0xf] {
			return false
		}
	}
	return true
}

func (d sha1Digest) hasPrefix(s string) bool {
	if len(s) > 45 {
		return false
	}
	if len(s) == 45 {
		return d.equalString(s)
	}
	if !strings.HasPrefix(s, "sha1-") {
		return false
	}
	s = s[len("sha1-"):]
	if len(s) == 0 {
		// we want at least one digest char to match on
		return false
	}
	for i, b := range d[:] {
		even := i * 2
		if even == len(s) {
			break
		}
		if s[even] != hexDigit[b>>4] {
			return false
		}
		odd := i*2 + 1
		if odd == len(s) {
			break
		}
		if s[odd] != hexDigit[b&0xf] {
			return false
		}
	}
	return true
}

type sha224Digest [28]byte

const sha224StrLen = 63 // len("sha224-d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f")

func (d sha224Digest) digestName() string { return "sha224" }
func (d sha224Digest) bytes() []byte      { return d[:] }
func (d sha224Digest) newHash() hash.Hash { return sha256.New224() }
func (d sha224Digest) equalString(s string) bool {
	if len(s) != sha224StrLen {
		return false
	}
	if !strings.HasPrefix(s, "sha224-") {
		return false
	}
	s = s[len("sha224-"):]
	for i, b := range d[:] {
		if s[i*2] != hexDigit[b>>4] || s[i*2+1] != hexDigit[b&0xf] {
			return false
		}
	}
	return true
}

func (d sha224Digest) hasPrefix(s string) bool {
	if len(s) > sha224StrLen {
		return false
	}
	if len(s) == sha224StrLen {
		return d.equalString(s)
	}
	if !strings.HasPrefix(s, "sha224-") {
		return false
	}
	s = s[len("sha224-"):]
	if len(s) == 0 {
		// we want at least one digest char to match on
		return false
	}
	for i, b := range d[:] {
		even := i * 2
		if even == len(s) {
			break
		}
		if s[even] != hexDigit[b>>4] {
			return false
		}
		odd := i*2 + 1
		if odd == len(s) {
			break
		}
		if s[odd] != hexDigit[b&0xf] {
			return false
		}
	}
	return true
}

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
func (d otherDigest) equalString(s string) bool {
	wantLen := len(d.name) + len("-") + 2*d.sumLen
	if d.odd {
		wantLen--
	}
	if len(s) != wantLen || !strings.HasPrefix(s, d.name) || s[len(d.name)] != '-' {
		return false
	}
	s = s[len(d.name)+1:]
	for i, b := range d.sum[:d.sumLen] {
		if s[i*2] != hexDigit[b>>4] {
			return false
		}
		if i == d.sumLen-1 && d.odd {
			break
		}
		if s[i*2+1] != hexDigit[b&0xf] {
			return false
		}
	}
	return true
}

func (d otherDigest) hasPrefix(s string) bool {
	maxLen := len(d.name) + len("-") + 2*d.sumLen
	if d.odd {
		maxLen--
	}
	if len(s) > maxLen || !strings.HasPrefix(s, d.name) || s[len(d.name)] != '-' {
		return false
	}
	if len(s) == maxLen {
		return d.equalString(s)
	}
	s = s[len(d.name)+1:]
	if len(s) == 0 {
		// we want at least one digest char to match on
		return false
	}
	for i, b := range d.sum[:d.sumLen] {
		even := i * 2
		if even == len(s) {
			break
		}
		if s[even] != hexDigit[b>>4] {
			return false
		}
		odd := i*2 + 1
		if odd == len(s) {
			break
		}
		if i == d.sumLen-1 && d.odd {
			break
		}
		if s[odd] != hexDigit[b&0xf] {
			return false
		}
	}
	return true
}

var (
	sha1Meta = &digestMeta{
		ctor:  sha1FromBinary,
		ctors: sha1FromHexString,
		ctorb: sha1FromHexBytes,
		size:  sha1.Size,
	}
	sha224Meta = &digestMeta{
		ctor:  sha224FromBinary,
		ctors: sha224FromHexString,
		ctorb: sha224FromHexBytes,
		size:  sha256.Size224,
	}
)

var metaFromString = map[string]*digestMeta{
	"sha1":   sha1Meta,
	"sha224": sha224Meta,
}

type blobTypeAndMeta struct {
	name []byte
	meta *digestMeta
}

var metas []blobTypeAndMeta

func metaFromBytes(name []byte) (meta *digestMeta, ok bool) {
	for _, bm := range metas {
		if bytes.Equal(name, bm.name) {
			return bm.meta, true
		}
	}
	return
}

func init() {
	for name, meta := range metaFromString {
		metas = append(metas, blobTypeAndMeta{
			name: []byte(name),
			meta: meta,
		})
	}
}

// HashFuncs returns the names of the supported hash functions.
func HashFuncs() []string {
	hashes := make([]string, len(metas))
	for i, m := range metas {
		hashes[i] = string(m.name)
	}
	return hashes
}

var (
	sha1Type   = reflect.TypeOf(sha1.New())
	sha224Type = reflect.TypeOf(sha256.New224())
)

// hashSig is the tuple (reflect.Type, hash size), for use as a map key.
// The size disambiguates SHA-256 vs SHA-224, both of which have the same
// reflect.Type (crypto/sha256.digest, but one has is224 bool set true).
type hashSig struct {
	rt   reflect.Type
	size int
}

var metaFromType = map[hashSig]*digestMeta{
	{sha1Type, sha1.Size}:        sha1Meta,
	{sha224Type, sha256.Size224}: sha224Meta,
}

type digestMeta struct {
	ctor  func(binary []byte) digestType
	ctors func(hex string) (digestType, bool)
	ctorb func(hex []byte) (digestType, bool)
	size  int // bytes of digest
}

var bufPool = make(chan []byte, 20)

func getBuf(size int) []byte {
	for {
		select {
		case b := <-bufPool:
			if cap(b) >= size {
				return b[:size]
			}
		default:
			return make([]byte, size)
		}
	}
}

func putBuf(b []byte) {
	select {
	case bufPool <- b:
	default:
	}
}

// NewHash returns a new hash.Hash of the currently recommended hash type.
// Currently this is SHA-224, but is subject to change over time.
func NewHash() hash.Hash {
	if testhooks.UseSHA1() {
		return sha1.New()
	}
	return sha256.New224()
}

func ValidRefString(s string) bool {
	// TODO: optimize to not allocate
	return ParseOrZero(s).Valid()
}

var null = []byte(`null`)

func (r *Ref) UnmarshalJSON(d []byte) error {
	if r.digest != nil {
		return errors.New("Can't UnmarshalJSON into a non-zero Ref")
	}
	if len(d) == 0 || bytes.Equal(d, null) {
		return nil
	}
	if len(d) < 2 || d[0] != '"' || d[len(d)-1] != '"' {
		return fmt.Errorf("blob: expecting a JSON string to unmarshal, got %q", d)
	}
	d = d[1 : len(d)-1]
	p, ok := ParseBytes(d)
	if !ok {
		return fmt.Errorf("blobref: invalid blobref %q (%d)", d, len(d))
	}
	*r = p
	return nil
}

func (r Ref) MarshalJSON() ([]byte, error) {
	if !r.Valid() {
		return null, nil
	}
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

// Less reports whether r sorts before o. Invalid references blobs sort first.
func (r Ref) Less(o Ref) bool {
	if r.Valid() != o.Valid() {
		return o.Valid()
	}
	if !r.Valid() {
		return false
	}
	if n1, n2 := r.digest.digestName(), o.digest.digestName(); n1 != n2 {
		return n1 < n2
	}
	return bytes.Compare(r.digest.bytes(), o.digest.bytes()) < 0
}

// ByRef sorts blob references.
type ByRef []Ref

func (s ByRef) Len() int           { return len(s) }
func (s ByRef) Less(i, j int) bool { return s[i].Less(s[j]) }
func (s ByRef) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// SizedByRef sorts SizedRefs by their blobref.
type SizedByRef []SizedRef

func (s SizedByRef) Len() int           { return len(s) }
func (s SizedByRef) Less(i, j int) bool { return s[i].Less(s[j]) }
func (s SizedByRef) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// TypeAlphabet returns the valid characters in the given blobref type.
// It returns the empty string if the typ is unknown.
func TypeAlphabet(typ string) string {
	switch typ {
	case "sha1":
		return hexDigit
	case "sha224":
		return hexDigit
	}
	return ""
}
