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

var supportedDigests = map[string]func()hash.Hash{
	"sha1": func() hash.Hash {
		return sha1.New()
	},
}

type BlobRef struct {
	hashName  string
	digest    string
}

type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

type Fetcher interface {
	Fetch(*BlobRef) (file ReadSeekCloser, size int64, err os.Error)
}

func (b *BlobRef) HashName() string {
	return b.hashName
}

func (b *BlobRef) Digest() string {
	return b.digest
}

func (o *BlobRef) String() string {
	return fmt.Sprintf("%s-%s", o.hashName, o.digest)
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

func blobIfValid(hashname, digest string) *BlobRef {
	expectedSize := kExpectedDigestSize[hashname]
	if expectedSize != 0 && len(digest) != expectedSize {
		return nil
	}
	return &BlobRef{hashname, digest}
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
