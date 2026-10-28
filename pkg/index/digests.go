/*
Copyright 2026 The Perkeep Authors.

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
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"strconv"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/sorted"
)

// DigestTypes are the hash functions of the digests that the index keeps
// of each file's contents, in the order it lists them: "crc32" (the
// IEEE checksum of zip files), "md5", "sha1", "sha224", and "sha256".
//
// A digest is written like a blob ref, as its type, a hyphen, and its
// value in lowercase hex, which for "crc32" is 8 digits, big-endian, such
// as "crc32-cbf43926". The index doesn't check that other services'
// digests are of the same bytes; they may be of a different encoding of
// the same photo, say.
var DigestTypes = []string{"crc32", "md5", "sha1", "sha224", "sha256"}

// digestHexLen maps each of DigestTypes to the length of its digests'
// hex values.
var digestHexLen = map[string]int{
	"crc32":  8,
	"md5":    32,
	"sha1":   40,
	"sha224": 56,
	"sha256": 64,
}

// ValidDigest reports whether d is a digest of one of DigestTypes, in
// its canonical form.
func ValidDigest(d string) bool {
	typ, val, ok := strings.Cut(d, "-")
	n, known := digestHexLen[typ]
	if !ok || !known || len(val) != n {
		return false
	}
	for _, c := range []byte(val) {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// DigestType returns the type of digest d, the part before its hyphen.
func DigestType(d string) string {
	typ, _, _ := strings.Cut(d, "-")
	return typ
}

// fileDigester computes all of DigestTypes of what's written to it, and
// its wholeRef.
type fileDigester struct {
	crc32  hash.Hash32
	hashes []hash.Hash // in the order of DigestTypes, after crc32
	whole  hash.Hash   // of the type blob.NewHash returns; usually one of hashes
	w      io.Writer
}

// wholeHashName is the hash type of new wholeRefs.
var wholeHashName = blob.RefFromHash(blob.NewHash()).HashName()

func newFileDigester() *fileDigester {
	d := &fileDigester{
		crc32:  crc32.NewIEEE(),
		hashes: []hash.Hash{md5.New(), sha1.New(), sha256.New224(), sha256.New()},
	}
	ws := []io.Writer{d.crc32}
	for i, h := range d.hashes {
		ws = append(ws, h)
		if DigestTypes[i+1] == wholeHashName {
			d.whole = h
		}
	}
	if d.whole == nil {
		d.whole = blob.NewHash()
		ws = append(ws, d.whole)
	}
	d.w = io.MultiWriter(ws...)
	return d
}

func (d *fileDigester) Write(p []byte) (int, error) { return d.w.Write(p) }

// wholeRef returns the wholeRef of what's been written.
func (d *fileDigester) wholeRef() blob.Ref { return blob.RefFromHash(d.whole) }

// digests returns the digests of what's been written, in the order of
// DigestTypes.
func (d *fileDigester) digests() []string {
	ds := []string{fmt.Sprintf("crc32-%08x", d.crc32.Sum32())}
	for i, h := range d.hashes {
		ds = append(ds, DigestTypes[i+1]+"-"+hex.EncodeToString(h.Sum(nil)))
	}
	return ds
}

// setDigestRows adds to mm the rows of a file's contents wholeRef having
// digests.
func setDigestRows(mm *mutationMap, wholeRef blob.Ref, digests []string) {
	mm.Set(keyFileDigests.Key(wholeRef), keyFileDigests.Val(strings.Join(digests, "|")))
	for _, d := range digests {
		mm.Set(keyDigestToWhole.Key(d, wholeRef), "1")
	}
}

// parseDigestsValue returns the valid digests in v, a keyFileDigests
// row's value.
func parseDigestsValue(v string) []string {
	var ds []string
	for d := range strings.SplitSeq(v, "|") {
		if ValidDigest(d) {
			ds = append(ds, d)
		}
	}
	return ds
}

// crc32OfDigests returns the CRC-32 in digests, if any.
func crc32OfDigests(digests []string) (sum uint32, ok bool) {
	for _, d := range digests {
		if hexVal, ok := strings.CutPrefix(d, "crc32-"); ok {
			v, err := strconv.ParseUint(hexVal, 16, 32)
			return uint32(v), err == nil
		}
	}
	return 0, false
}

// GetFileDigests returns the digests of the contents of the file fileRef,
// in the order of DigestTypes. It returns os.ErrNotExist if fileRef isn't
// an indexed file, or its contents' digests aren't indexed. It always
// reads them from the index storage, as the corpus only keeps their
// CRC-32s.
func (x *Index) GetFileDigests(ctx context.Context, fileRef blob.Ref) ([]string, error) {
	fi, err := x.GetFileInfo(ctx, fileRef)
	if err != nil {
		return nil, err
	}
	if !fi.WholeRef.Valid() {
		return nil, os.ErrNotExist
	}
	v, err := x.s.Get(keyFileDigests.Key(fi.WholeRef))
	if errors.Is(err, sorted.ErrNotFound) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	return parseDigestsValue(v), nil
}

// AppendWholeRefsOfDigest appends to dst the wholeRefs of the indexed
// file contents having digest d, which must be valid.
func (x *Index) AppendWholeRefsOfDigest(ctx context.Context, dst []blob.Ref, d string) (_ []blob.Ref, err error) {
	it := x.queryPrefix(keyDigestToWhole, d)
	defer closeIterator(it, &err)
	for it.Next() {
		// digesttowhole|digest|wholeRef = "1"
		k := it.KeyBytes()
		pipe := strings.LastIndexByte(string(k), '|')
		if wholeRef, ok := blob.ParseBytes(k[pipe+1:]); ok {
			dst = append(dst, wholeRef)
		}
	}
	return dst, nil
}
