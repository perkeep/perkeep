package main

import (
	"crypto/sha1"
	"fmt"
	"hash"
	"regexp"
)

var kGetPutPattern *regexp.Regexp = regexp.MustCompile(`^/camli/([a-z0-9]+)-([a-f0-9]+)$`)
var kBlobRefPattern *regexp.Regexp = regexp.MustCompile(`^([a-z0-9]+)-([a-f0-9]+)$`)

type BlobRef struct {
	HashName string
	Digest   string
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

func blobFromPattern(r *regexp.Regexp, s string) *BlobRef {
	matches := r.FindStringSubmatch(s)
	if len(matches) != 3 {
		return nil
	}
	return blobIfValid(matches[1], matches[2])
}

func ParseBlobRef(ref string) *BlobRef {
	return blobFromPattern(kBlobRefPattern, ref)
}

func ParsePath(path string) *BlobRef {
	return blobFromPattern(kGetPutPattern, path)
}

func (o *BlobRef) IsSupported() bool {
	if o.HashName == "sha1" {
		return true
	}
	return false
}

func (o *BlobRef) String() string {
	return fmt.Sprintf("%s-%s", o.HashName, o.Digest)
}

func (o *BlobRef) Hash() hash.Hash {
	if o.HashName == "sha1" {
		return sha1.New()
	}
	return nil
}

func (o *BlobRef) FileBaseName() string {
	return fmt.Sprintf("%s-%s.dat", o.HashName, o.Digest)
}

func (o *BlobRef) DirectoryName() string {
	return fmt.Sprintf("%s/%s/%s/%s",
		*flagStorageRoot, o.HashName, o.Digest[0:3], o.Digest[3:6])
}

func (o *BlobRef) FileName() string {
	return fmt.Sprintf("%s/%s-%s.dat", o.DirectoryName(), o.HashName, o.Digest)
}
