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

package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"camlistore.org/pkg/blobref"
)

var _ = log.Printf

const closedIndex = -1

var errClosed = errors.New("filereader is closed")

// A DirReader reads the entries of a "directory" schema blob's
// referenced "static-set" blob.
type DirReader struct {
	fetcher blobref.SeekFetcher
	ss      *Superset

	staticSet []*blobref.BlobRef
	current   int
}

// NewDirReader creates a new directory reader and prepares to
// fetch the static-set entries
func NewDirReader(fetcher blobref.SeekFetcher, dirBlobRef *blobref.BlobRef) (*DirReader, error) {
	ss := new(Superset)
	err := ss.setFromBlobRef(fetcher, dirBlobRef)
	if err != nil {
		return nil, err
	}
	if ss.Type != "directory" {
		return nil, fmt.Errorf("schema/filereader: expected \"directory\" schema blob for %s, got %q", dirBlobRef, ss.Type)
	}
	dr, err := ss.NewDirReader(fetcher)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: creating DirReader for %s: %v", dirBlobRef, err)
	}
	dr.current = 0
	return dr, nil
}

func (ss *Superset) NewDirReader(fetcher blobref.SeekFetcher) (*DirReader, error) {
	if ss.Type != "directory" {
		return nil, fmt.Errorf("Superset not of type \"directory\"")
	}
	return &DirReader{fetcher: fetcher, ss: ss}, nil
}

func (ss *Superset) setFromBlobRef(fetcher blobref.SeekFetcher, blobRef *blobref.BlobRef) error {
	if blobRef == nil {
		return errors.New("schema/filereader: blobref was nil")
	}
	ss.BlobRef = blobRef
	rsc, _, err := fetcher.Fetch(blobRef)
	if err != nil {
		return fmt.Errorf("schema/filereader: fetching schema blob %s: %v", blobRef, err)
	}
	defer rsc.Close()
	if err = json.NewDecoder(rsc).Decode(ss); err != nil {
		return fmt.Errorf("schema/filereader: decoding schema blob %s: %v", blobRef, err)
	}
	return nil
}

// StaticSet returns the whole of the static set members of that directory
func (dr *DirReader) StaticSet() ([]*blobref.BlobRef, error) {
	if dr.staticSet != nil {
		return dr.staticSet, nil
	}
	staticSetBlobref := blobref.Parse(dr.ss.Entries)
	if staticSetBlobref == nil {
		return nil, fmt.Errorf("schema/filereader: Invalid blobref\n")
	}
	rsc, _, err := dr.fetcher.Fetch(staticSetBlobref)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: fetching schema blob %s: %v", staticSetBlobref, err)
	}
	ss, err := ParseSuperset(rsc)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: decoding schema blob %s: %v", staticSetBlobref, err)
	}
	if ss.Type != "static-set" {
		return nil, fmt.Errorf("schema/filereader: expected \"static-set\" schema blob for %s, got %q", staticSetBlobref, ss.Type)
	}
	for _, s := range ss.Members {
		member := blobref.Parse(s)
		if member == nil {
			return nil, fmt.Errorf("schema/filereader: invalid (static-set member) blobref\n")
		}
		dr.staticSet = append(dr.staticSet, member)
	}
	return dr.staticSet, nil
}

// Readdir implements the Directory interface.
func (dr *DirReader) Readdir(n int) (entries []DirectoryEntry, err error) {
	sts, err := dr.StaticSet()
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: can't get StaticSet: %v\n", err)
	}
	up := dr.current + n
	if n <= 0 {
		dr.current = 0
		up = len(sts)
	} else {
		if n > (len(sts) - dr.current) {
			err = io.EOF
			up = len(sts)
		}
	}
	for _, entryBref := range sts[dr.current:up] {
		entry, err := NewDirectoryEntryFromBlobRef(dr.fetcher, entryBref)
		if err != nil {
			return nil, fmt.Errorf("schema/filereader: can't create dirEntry: %v\n", err)
		}
		entries = append(entries, entry)
	}
	return entries, err
}

// A FileReader reads the bytes of "file" and "bytes" schema blobrefs.
type FileReader struct {
	*io.SectionReader

	fetcher blobref.SeekFetcher
	ss      *Superset

	size int64 // total number of bytes
}

// NewFileReader returns a new FileReader reading the contents of fileBlobRef,
// fetching blobs from fetcher.  The fileBlobRef must be of a "bytes" or "file"
// schema blob.
//
// The caller should call Close on the FileReader when done reading.
func NewFileReader(fetcher blobref.SeekFetcher, fileBlobRef *blobref.BlobRef) (*FileReader, error) {
	// TODO(bradfitz): make this take a blobref.FetcherAt instead?
	// TODO(bradfitz): rename this into bytes reader? but for now it's still
	//                 named FileReader, but can also read a "bytes" schema.
	if fileBlobRef == nil {
		return nil, errors.New("schema/filereader: NewFileReader blobref was nil")
	}
	rsc, _, err := fetcher.Fetch(fileBlobRef)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: fetching file schema blob: %v", err)
	}
	defer rsc.Close()
	ss, err := ParseSuperset(rsc)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: decoding file schema blob: %v", err)
	}
	if ss.Type != "file" && ss.Type != "bytes" {
		return nil, fmt.Errorf("schema/filereader: expected \"file\" or \"bytes\" schema blob, got %q", ss.Type)
	}
	fr, err := ss.NewFileReader(fetcher)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: creating FileReader for %s: %v", fileBlobRef, err)
	}
	return fr, nil
}

func (ss *Superset) NewFileReader(fetcher blobref.SeekFetcher) (*FileReader, error) {
	if ss.Type != "file" && ss.Type != "bytes" {
		return nil, fmt.Errorf("schema/filereader: Superset not of type \"file\" or \"bytes\"")
	}
	size := int64(ss.SumPartsSize())
	fr := &FileReader{
		fetcher: fetcher,
		ss:      ss,
		size:    size,
	}
	fr.SectionReader = io.NewSectionReader(fr, 0, size)
	return fr, nil
}

// FileSchema returns the reader's schema superset. Don't mutate it.
func (fr *FileReader) FileSchema() *Superset {
	return fr.ss
}

func (fr *FileReader) Close() error {
	// TODO: close cached blobs?
	return nil
}

// Skip skips past skipBytes of the file.
// It is equivalent to but more efficient than:
//
//   io.CopyN(ioutil.Discard, fr, skipBytes)
//
// It returns the number of bytes skipped.
//
// TODO(bradfitz): delete this. Legacy interface; callers should just Seek now.
func (fr *FileReader) Skip(skipBytes uint64) uint64 {
	oldOff, err := fr.Seek(0, os.SEEK_CUR)
	if err != nil {
		panic("Failed to seek")
	}
	remain := fr.size - oldOff
	if int64(skipBytes) > remain {
		skipBytes = uint64(remain)
	}
	newOff, err := fr.Seek(int64(skipBytes), os.SEEK_CUR)
	if err != nil {
		panic("Failed to seek")
	}
	skipped := newOff - oldOff
	if skipped < 0 {
		panic("")
	}
	return uint64(skipped)
}

var _ interface {
	io.ReaderAt
	io.Reader
	io.Closer
	Size() int64
} = (*FileReader)(nil)

func (fr *FileReader) ReadAt(p []byte, offset int64) (n int, err error) {
	if offset < 0 {
		return 0, errors.New("schema/filereader: negative offset")
	}
	if offset >= fr.Size() {
		return 0, io.EOF
	}
	want := len(p)
	for len(p) > 0 && err == nil {
		var rc io.ReadCloser
		rc, err = fr.readerForOffset(offset)
		if err != nil {
			return
		}
		var n1 int64 // never bigger than an int
		n1, err = io.CopyN(&sliceWriter{p}, rc, int64(len(p)))
		rc.Close()
		if err == io.EOF {
			err = nil
		}
		if n1 == 0 {
			break
		}
		p = p[n1:]
		offset += int64(n1)
		n += int(n1)
	}
	if n < want && err == nil {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

type sliceWriter struct {
	dst []byte
}

func (sw *sliceWriter) Write(p []byte) (n int, err error) {
	n = copy(sw.dst, p)
	sw.dst = sw.dst[n:]
	return n, nil
}

var eofReader io.ReadCloser = ioutil.NopCloser(strings.NewReader(""))

// readerForOffset returns a ReadCloser that reads some number of bytes and then EOF
// from the provided offset.  Seeing EOF doesn't mean the end of the whole file; just the
// chunk at that offset.  The caller must close the ReadCloser when done reading.
func (fr *FileReader) readerForOffset(off int64) (io.ReadCloser, error) {
	if off < 0 {
		panic("negative offset")
	}
	if off >= fr.size {
		return eofReader, nil
	}
	offRemain := off
	parts := fr.ss.Parts
	for len(parts) > 0 && parts[0].Size <= uint64(offRemain) {
		offRemain -= int64(parts[0].Size)
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return eofReader, nil
	}
	p0 := parts[0]
	var rsc blobref.ReadSeekCloser
	var err error
	switch {
	case p0.BlobRef != nil && p0.BytesRef != nil:
		return nil, fmt.Errorf("part illegally contained both a blobRef and bytesRef")
	case p0.BlobRef == nil && p0.BytesRef == nil:
		return &nZeros{int(p0.Size - uint64(offRemain))}, nil
	case p0.BlobRef != nil:
		rsc, _, err = fr.fetcher.Fetch(p0.BlobRef)
	case p0.BytesRef != nil:
		rsc, err = NewFileReader(fr.fetcher, p0.BytesRef)
	}
	if err != nil {
		return nil, err
	}
	offRemain += int64(p0.Offset)
	if offRemain > 0 {
		newPos, err := rsc.Seek(offRemain, os.SEEK_SET)
		if err != nil {
			return nil, err
		}
		if newPos != offRemain {
			panic("Seek didn't work")
		}
	}
	return struct {
		io.Reader
		io.Closer
	}{
		io.LimitReader(rsc, int64(p0.Size)),
		rsc,
	}, nil
}

// nZeros is a ReadCloser that reads remain zeros before EOF.
type nZeros struct {
	remain int
}

func (z *nZeros) Read(p []byte) (n int, err error) {
	for len(p) > 0 && z.remain > 0 {
		p[0] = 0
		n++
		z.remain--
	}
	if n == 0 && z.remain == 0 {
		err = io.EOF
	}
	return
}

func (*nZeros) Close() error { return nil }
