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
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/env"
	"camlistore.org/pkg/types"

	"go4.org/syncutil"
	"go4.org/syncutil/singleflight"
)

const closedIndex = -1

var errClosed = errors.New("filereader is closed")

// A FileReader reads the bytes of "file" and "bytes" schema blobrefs.
type FileReader struct {
	// Immutable stuff:
	*io.SectionReader             // provides Read, Seek, and Size.
	parent            *FileReader // or nil. for sub-region readers to find the top.
	rootOff           int64       // this FileReader's offset from the root
	fetcher           blob.Fetcher
	ss                *superset
	size              int64 // total number of bytes

	sfg singleflight.Group // for loading blobrefs for ssm

	blobmu   sync.Mutex // guards lastBlob
	lastBlob *blob.Blob // most recently fetched blob; cuts dup reads up to 85x

	ssmmu sync.Mutex             // guards ssm
	ssm   map[blob.Ref]*superset // blobref -> superset
}

var _ interface {
	io.Seeker
	io.ReaderAt
	io.Reader
	io.Closer
	Size() int64
} = (*FileReader)(nil)

// NewFileReader returns a new FileReader reading the contents of fileBlobRef,
// fetching blobs from fetcher.  The fileBlobRef must be of a "bytes" or "file"
// schema blob.
//
// The caller should call Close on the FileReader when done reading.
func NewFileReader(fetcher blob.Fetcher, fileBlobRef blob.Ref) (*FileReader, error) {
	// TODO(bradfitz): rename this into bytes reader? but for now it's still
	//                 named FileReader, but can also read a "bytes" schema.
	if !fileBlobRef.Valid() {
		return nil, errors.New("schema/filereader: NewFileReader blobref invalid")
	}
	rc, _, err := fetcher.Fetch(fileBlobRef)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: fetching file schema blob: %v", err)
	}
	defer rc.Close()
	ss, err := parseSuperset(rc)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: decoding file schema blob: %v", err)
	}
	ss.BlobRef = fileBlobRef
	if ss.Type != "file" && ss.Type != "bytes" {
		return nil, fmt.Errorf("schema/filereader: expected \"file\" or \"bytes\" schema blob, got %q", ss.Type)
	}
	fr, err := ss.NewFileReader(fetcher)
	if err != nil {
		return nil, fmt.Errorf("schema/filereader: creating FileReader for %s: %v", fileBlobRef, err)
	}
	return fr, nil
}

func (b *Blob) NewFileReader(fetcher blob.Fetcher) (*FileReader, error) {
	return b.ss.NewFileReader(fetcher)
}

// NewFileReader returns a new FileReader, reading bytes and blobs
// from the provided fetcher.
//
// NewFileReader does no fetch operation on the fetcher itself.  The
// fetcher is only used in subsequent read operations.
//
// An error is only returned if the type of the superset is not either
// "file" or "bytes".
func (ss *superset) NewFileReader(fetcher blob.Fetcher) (*FileReader, error) {
	if ss.Type != "file" && ss.Type != "bytes" {
		return nil, fmt.Errorf("schema/filereader: Superset not of type \"file\" or \"bytes\"")
	}
	size := int64(ss.SumPartsSize())
	fr := &FileReader{
		fetcher: fetcher,
		ss:      ss,
		size:    size,
		ssm:     make(map[blob.Ref]*superset),
	}
	fr.SectionReader = io.NewSectionReader(fr, 0, size)
	return fr, nil
}

// LoadAllChunks starts a process of loading all chunks of this file
// as quickly as possible. The contents are immediately discarded, so
// it is assumed that the fetcher is a caching fetcher.
func (fr *FileReader) LoadAllChunks() {
	// TODO: ask the underlying blobserver to do this if it would
	// prefer.  Some blobservers (like blobpacked) might not want
	// to do this at all.
	go fr.loadAllChunksSync()
}

func (fr *FileReader) loadAllChunksSync() {
	gate := syncutil.NewGate(20) // num readahead chunk loads at a time
	fr.ForeachChunk(func(_ []blob.Ref, p BytesPart) error {
		if !p.BlobRef.Valid() {
			return nil
		}
		gate.Start()
		go func(br blob.Ref) {
			defer gate.Done()
			rc, _, err := fr.fetcher.Fetch(br)
			if err == nil {
				defer rc.Close()
				var b [1]byte
				rc.Read(b[:]) // fault in the blob
			}
		}(p.BlobRef)
		return nil
	})
}

// UnixMtime returns the file schema's UnixMtime field, or the zero value.
func (fr *FileReader) UnixMtime() time.Time {
	t, err := time.Parse(time.RFC3339, fr.ss.UnixMtime)
	if err != nil {
		return time.Time{}
	}
	return t
}

// FileName returns the file schema's filename, if any.
func (fr *FileReader) FileName() string { return fr.ss.FileNameString() }

func (fr *FileReader) ModTime() time.Time { return fr.ss.ModTime() }

func (fr *FileReader) SchemaBlobRef() blob.Ref { return fr.ss.BlobRef }

// Close currently does nothing.
func (fr *FileReader) Close() error { return nil }

func (fr *FileReader) ReadAt(p []byte, offset int64) (n int, err error) {
	if offset < 0 {
		return 0, errors.New("schema/filereader: negative offset")
	}
	if offset >= fr.Size() {
		return 0, io.EOF
	}
	want := len(p)
	for len(p) > 0 && err == nil {
		rc, err := fr.readerForOffset(offset)
		if err != nil {
			return n, err
		}
		var n1 int
		n1, err = io.ReadFull(rc, p)
		rc.Close()
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			err = nil
		}
		if n1 == 0 {
			break
		}
		p = p[n1:]
		offset += int64(n1)
		n += n1
	}
	if n < want && err == nil {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

// ForeachChunk calls fn for each chunk of fr, in order.
//
// The schemaPath argument will be the path from the "file" or "bytes"
// schema blob down to possibly other "bytes" schema blobs, the final
// one of which references the given BytesPart. The BytesPart will be
// the actual chunk. The fn function will not be called with
// BytesParts referencing a "BytesRef"; those are followed recursively
// instead. The fn function must not retain or mutate schemaPath.
//
// If fn returns an error, iteration stops and that error is returned
// from ForeachChunk. Other errors may be returned from ForeachChunk
// if schema blob fetches fail.
func (fr *FileReader) ForeachChunk(fn func(schemaPath []blob.Ref, p BytesPart) error) error {
	return fr.foreachChunk(fn, nil)
}

func (fr *FileReader) foreachChunk(fn func([]blob.Ref, BytesPart) error, path []blob.Ref) error {
	path = append(path, fr.ss.BlobRef)
	for _, bp := range fr.ss.Parts {
		if bp.BytesRef.Valid() && bp.BlobRef.Valid() {
			return fmt.Errorf("part in %v illegally contained both a blobRef and bytesRef", fr.ss.BlobRef)
		}
		if bp.BytesRef.Valid() {
			ss, err := fr.getSuperset(bp.BytesRef)
			if err != nil {
				return err
			}
			subfr, err := ss.NewFileReader(fr.fetcher)
			if err != nil {
				return err
			}
			subfr.parent = fr
			if err := subfr.foreachChunk(fn, path); err != nil {
				return err
			}
		} else {
			if err := fn(path, *bp); err != nil {
				return err
			}
		}
	}
	return nil
}

func (fr *FileReader) rootReader() *FileReader {
	if fr.parent != nil {
		return fr.parent.rootReader()
	}
	return fr
}

func (fr *FileReader) getBlob(br blob.Ref) (*blob.Blob, error) {
	if root := fr.rootReader(); root != fr {
		return root.getBlob(br)
	}
	fr.blobmu.Lock()
	last := fr.lastBlob
	fr.blobmu.Unlock()
	if last != nil && last.Ref() == br {
		return last, nil
	}
	blob, err := blob.FromFetcher(fr.fetcher, br)
	if err != nil {
		return nil, err
	}

	fr.blobmu.Lock()
	fr.lastBlob = blob
	fr.blobmu.Unlock()
	return blob, nil
}

func (fr *FileReader) getSuperset(br blob.Ref) (*superset, error) {
	if root := fr.rootReader(); root != fr {
		return root.getSuperset(br)
	}
	brStr := br.String()
	ssi, err := fr.sfg.Do(brStr, func() (interface{}, error) {
		fr.ssmmu.Lock()
		ss, ok := fr.ssm[br]
		fr.ssmmu.Unlock()
		if ok {
			return ss, nil
		}
		rc, _, err := fr.fetcher.Fetch(br)
		if err != nil {
			return nil, fmt.Errorf("schema/filereader: fetching file schema blob: %v", err)
		}
		defer rc.Close()
		ss, err = parseSuperset(rc)
		if err != nil {
			return nil, err
		}
		ss.BlobRef = br
		fr.ssmmu.Lock()
		defer fr.ssmmu.Unlock()
		fr.ssm[br] = ss
		return ss, nil
	})
	if err != nil {
		return nil, err
	}
	return ssi.(*superset), nil
}

var debug = env.IsDebug()

// readerForOffset returns a ReadCloser that reads some number of bytes and then EOF
// from the provided offset.  Seeing EOF doesn't mean the end of the whole file; just the
// chunk at that offset.  The caller must close the ReadCloser when done reading.
func (fr *FileReader) readerForOffset(off int64) (io.ReadCloser, error) {
	if debug {
		log.Printf("(%p) readerForOffset %d + %d = %d", fr, fr.rootOff, off, fr.rootOff+off)
	}
	if off < 0 {
		panic("negative offset")
	}
	if off >= fr.size {
		return types.EmptyBody, nil
	}
	offRemain := off
	var skipped int64
	parts := fr.ss.Parts
	for len(parts) > 0 && parts[0].Size <= uint64(offRemain) {
		offRemain -= int64(parts[0].Size)
		skipped += int64(parts[0].Size)
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return types.EmptyBody, nil
	}
	p0 := parts[0]
	var rsc types.ReadSeekCloser
	var err error
	switch {
	case p0.BlobRef.Valid() && p0.BytesRef.Valid():
		return nil, fmt.Errorf("part illegally contained both a blobRef and bytesRef")
	case !p0.BlobRef.Valid() && !p0.BytesRef.Valid():
		return ioutil.NopCloser(
			io.LimitReader(zeroReader{},
				int64(p0.Size-uint64(offRemain)))), nil
	case p0.BlobRef.Valid():
		blob, err := fr.getBlob(p0.BlobRef)
		if err != nil {
			return nil, err
		}
		rsc = blob.Open()
	case p0.BytesRef.Valid():
		var ss *superset
		ss, err = fr.getSuperset(p0.BytesRef)
		if err != nil {
			return nil, err
		}
		rsc, err = ss.NewFileReader(fr.fetcher)
		if err == nil {
			subFR := rsc.(*FileReader)
			subFR.parent = fr.rootReader()
			subFR.rootOff = fr.rootOff + skipped
		}
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

type zeroReader struct{}

func (zeroReader) Read(p []byte) (n int, err error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
