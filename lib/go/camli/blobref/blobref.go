/*
Copyright 2011 Google Inc.

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

package blobref

import (
	"crypto/sha1"
	"fmt"
	"hash"
	"io"
	"os"
	"regexp"
)

var kBlobRefPattern *regexp.Regexp = regexp.MustCompile(`^([a-z0-9]+)-([a-f0-9]+)$`)

var supportedDigests = map[string]func() hash.Hash{
	"sha1": func() hash.Hash {
		return sha1.New()
	},
}

// BlobRef is an immutable reference to a blob.
type BlobRef struct {
	hashName string
	digest   string

	strValue string // "<hashname>-<digest>"
}

func (br *BlobRef) GobEncode() ([]byte, os.Error) {
	return []byte(br.String()), nil
}

func (br *BlobRef) GobDecode(b []byte) os.Error {
	dec := Parse(string(b))
	if dec == nil {
		return fmt.Errorf("invalid blobref %q", string(b))
	}
	*br = *dec
	return nil
}

// SizedBlobRef is like a BlobRef but includes because it includes a
// potentially mutable 'Size', this should be used as a stack value,
// not a *SizedBlobRef.
type SizedBlobRef struct {
	*BlobRef
	Size int64
}

func (sb *SizedBlobRef) Equal(o SizedBlobRef) bool {
	return sb.Size == o.Size && sb.BlobRef.String() == o.BlobRef.String()
}

func (sb SizedBlobRef) String() string {
	return fmt.Sprintf("[%s; %d bytes]", sb.BlobRef.String(), sb.Size)
}

type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

func (b *BlobRef) HashName() string {
	return b.hashName
}

func (b *BlobRef) Digest() string {
	return b.digest
}

func (b *BlobRef) DigestPrefix(digits int) string {
	if len(b.digest) < digits {
		return b.digest
	}
	return b.digest[:digits]
}

func (b *BlobRef) String() string {
	if b == nil {
		return "<nil-BlobRef>"
	}
	return b.strValue
}

func (b *BlobRef) DomID() string {
	if b == nil {
		return ""
	}
	return "camli-" + b.String()
}

func (o *BlobRef) Equals(other *BlobRef) bool {
	return o.hashName == other.hashName && o.digest == other.digest
}

func (o *BlobRef) Hash() hash.Hash {
	fn, ok := supportedDigests[o.hashName]
	if !ok {
		return nil // TODO: return an error here, not nil
	}
	return fn()
}

func (o *BlobRef) HashMatches(h hash.Hash) bool {
	return fmt.Sprintf("%x", h.Sum()) == o.digest
}

func (o *BlobRef) IsSupported() bool {
	_, ok := supportedDigests[o.hashName]
	return ok
}

func (o *BlobRef) Sum32() uint32 {
	var h32 uint32
	n, err := fmt.Sscanf(o.digest[len(o.digest)-8:], "%8x", &h32)
	if err != nil {
		panic(err)
	}
	if n != 1 {
		panic("sum32")
	}
	return h32
}

var kExpectedDigestSize = map[string]int{
	"md5":  32,
	"sha1": 40,
}

func newBlob(hashName, digest string) *BlobRef {
	strValue := fmt.Sprintf("%s-%s", hashName, digest)
	return &BlobRef{strValue[0:len(hashName)],
		strValue[len(hashName)+1:],
		strValue}
}

func blobIfValid(hashname, digest string) *BlobRef {
	expectedSize := kExpectedDigestSize[hashname]
	if expectedSize != 0 && len(digest) != expectedSize {
		return nil
	}
	return newBlob(hashname, digest)
}

func FromHash(hashfunc string, h hash.Hash) *BlobRef {
	return newBlob(hashfunc, fmt.Sprintf("%x", h.Sum()))
}

func Sha1FromString(s string) *BlobRef {
	s1 := sha1.New()
	s1.Write([]byte(s))
	return FromHash("sha1", s1)
}

// FromPattern takes a pattern and if it matches 's' with two exactly two valid
// submatches, returns a BlobRef, else returns nil.
func FromPattern(r *regexp.Regexp, s string) *BlobRef {
	matches := r.FindStringSubmatch(s)
	if len(matches) != 3 {
		return nil
	}
	return blobIfValid(matches[1], matches[2])
}

func Parse(ref string) *BlobRef {
	return FromPattern(kBlobRefPattern, ref)
}

func (br *BlobRef) UnmarshalJSON(d []byte) os.Error {
	if len(d) < 2 || d[0] != '"' || d[len(d)-1] != '"' {
		return fmt.Errorf("blobref: expecting a JSON string to unmarshal, got %q", d)
	}
	refStr := string(d[1 : len(d)-1])
	p := Parse(refStr)
	if p == nil {
		return fmt.Errorf("blobref: invalid blobref %q (%d)", refStr, len(refStr))
	}
	*br = *p
	return nil
}

func (br *BlobRef) MarshalJSON() ([]byte, os.Error) {
	return []byte(fmt.Sprintf("%q", br.String())), nil
}

func MustParse(ref string) *BlobRef {
	br := Parse(ref)
	if br == nil {
		panic("Failed to parse blobref: " + ref)
	}
	return br
}

// May return nil in list positions where the blobref could not be parsed.
func ParseMulti(refs []string) (parsed []*BlobRef) {
	parsed = make([]*BlobRef, 0, len(refs))
	for _, ref := range refs {
		parsed = append(parsed, Parse(ref))
	}
	return
}
