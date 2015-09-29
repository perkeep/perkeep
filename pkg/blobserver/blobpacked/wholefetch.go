/*
Copyright 2015 The Camlistore AUTHORS

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

package blobpacked

// TODO: the test coverage is a little weak here. TestPacked has a large
// integration test, but some Read/Close paths aren't tested well.

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/conv"
)

// zipPart is some of the state from the "w:sha1-xxx:n" meta rows.
// See docs on storage.meta.
type zipPart struct {
	idx    uint32 // 0, 1, ..., N-1 (of N zips making up the wholeref)
	zipRef blob.Ref
	zipOff uint32 // offset inside zip to get the uncompressed content
	len    uint32
}

// byZipIndex sorts zip parts by their numeric index.
// This was necessary because they may appear in lexical order from
// the sorted.KeyValue as "1", "10", "11", "2", "3".
type byZipIndex []zipPart

func (s byZipIndex) Len() int           { return len(s) }
func (s byZipIndex) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s byZipIndex) Less(i, j int) bool { return s[i].idx < s[j].idx }

func (s *storage) OpenWholeRef(wholeRef blob.Ref, offset int64) (rc io.ReadCloser, wholeSize int64, err error) {
	// See comment before the storage.meta field for the keys/values
	// being scanned here.
	startKey := wholeMetaPrefix + wholeRef.String()
	it := s.meta.Find(startKey, startKey+";")
	if it == nil {
		panic("nil iterator")
	}
	//defer it.Close()
	rows := 0
	var parts []zipPart
	var nZipWant uint32
	for it.Next() {
		rows++
		k := it.KeyBytes()
		if len(k) == len(startKey) {
			if rows != 1 {
				// Should be first. Confused.
				break
			}
			if err := conv.ParseFields(it.ValueBytes(), &wholeSize, &nZipWant); err != nil {
				return nil, 0, err
			}
			continue
		}
		var zp zipPart
		if k[len(startKey)] != ':' {
			// Unexpected key. Confused.
			break
		}
		if err := conv.ParseFields(k[len(startKey)+len(":"):], &zp.idx); err != nil {
			return nil, 0, fmt.Errorf("blobpacked: error parsing meta key %q: %v", k, err)
		}
		// "<zipchunk-blobref> <offset-in-zipchunk-blobref> <offset-in-whole_u64> <length_u32>"
		var ignore uint64
		if err := conv.ParseFields(it.ValueBytes(), &zp.zipRef, &zp.zipOff, &ignore, &zp.len); err != nil {
			return nil, 0, fmt.Errorf("blobpacked: error parsing meta key %q = %q: %v", k, it.ValueBytes(), err)
		}
		parts = append(parts, zp)
	}
	if err := it.Close(); err != nil {
		return nil, 0, err
	}
	if rows == 0 || uint32(len(parts)) != nZipWant {
		return nil, 0, os.ErrNotExist
	}
	sort.Sort(byZipIndex(parts))
	for i, zp := range parts {
		if zp.idx != uint32(i) {
			log.Printf("blobpacked: discontigous or overlapping index for wholeref %v", wholeRef)
			return nil, 0, os.ErrNotExist
		}
	}
	needSkip := offset
	for len(parts) > 0 && needSkip >= int64(parts[0].len) {
		needSkip -= int64(parts[0].len)
		parts = parts[1:]
	}
	if len(parts) > 0 && needSkip > 0 {
		parts[0].zipOff += uint32(needSkip)
		parts[0].len -= uint32(needSkip)
	}
	rc = &wholeFromZips{
		src:    s.large,
		remain: wholeSize - offset,
		zp:     parts,
	}
	return rc, wholeSize, nil
}

// wholeFromZips is an io.ReadCloser that stitches together
// a wholeRef from the inside of 0+ blobpacked zip files.
type wholeFromZips struct {
	src    blob.SubFetcher
	err    error // sticky
	closed bool
	remain int64

	// cur if non-nil is the reader to read on the next Read.
	// It has curRemain bytes remaining.
	cur       io.ReadCloser
	curRemain uint32

	// zp are the zip parts to open next, as cur is exhausted.
	zp []zipPart
}

func (zr *wholeFromZips) Read(p []byte) (n int, err error) {
	if zr.closed {
		return 0, errors.New("blobpacked: Read on Closed wholeref reader")
	}
	zr.initCur()
	if zr.err != nil {
		return 0, zr.err
	}
	if uint32(len(p)) > zr.curRemain {
		p = p[:zr.curRemain]
	}
	n, err = zr.cur.Read(p)
	if int64(n) > int64(zr.curRemain) || n < 0 {
		panic("Reader returned bogus number of bytes read")
	}
	zr.curRemain -= uint32(n)
	zr.remain -= int64(n)
	if zr.curRemain == 0 {
		zr.cur.Close()
		zr.cur = nil
	}
	if err == io.EOF && zr.remain > 0 {
		err = nil
	} else if err == nil && zr.remain == 0 {
		err = io.EOF
	}
	return n, err
}

func (zr *wholeFromZips) initCur() {
	if zr.err != nil || zr.cur != nil {
		return
	}
	if zr.remain <= 0 {
		zr.err = io.EOF
		return
	}
	if len(zr.zp) == 0 {
		zr.err = io.ErrUnexpectedEOF
		return
	}
	zp := zr.zp[0]
	zr.zp = zr.zp[1:]
	rc, err := zr.src.SubFetch(zp.zipRef, int64(zp.zipOff), int64(zp.len))
	if err != nil {
		if err == os.ErrNotExist {
			err = fmt.Errorf("blobpacked: error opening next part of file: %v", err)
		}
		zr.err = err
		return
	}
	zr.cur = rc
	zr.curRemain = zp.len
}

func (zr *wholeFromZips) Close() error {
	if zr.closed {
		return nil
	}
	zr.closed = true
	var err error
	if zr.cur != nil {
		err = zr.cur.Close()
		zr.cur = nil
	}
	return err
}
