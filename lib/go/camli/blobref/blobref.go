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

// SizedBlobRef is like a BlobRef but includes because it includes a
// potentially mutable 'Size', this should be used as a stack value,
// not a *SizedBlobRef.
type SizedBlobRef struct {
	*BlobRef
	Size int64
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

func (b *BlobRef) String() string {
	if b == nil {
		return "<nil-BlobRef>"
	}
	return b.strValue
}

func (o *BlobRef) Equals(other *BlobRef) bool {
	return o.hashName == other.hashName && o.digest == other.digest
}

func (o *BlobRef) Hash() hash.Hash {
	fn, ok := supportedDigests[o.hashName]
	if !ok {
		return nil
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

func FromHash(name string, h hash.Hash) *BlobRef {
	return newBlob(name, fmt.Sprintf("%x", h.Sum()))
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
