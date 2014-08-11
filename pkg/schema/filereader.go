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
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/singleflight"
	"camlistore.org/pkg/types"
)

const closedIndex = -1

var errClosed = errors.New("filereader is closed")

// A FileReader reads the bytes of "file" and "bytes" schema blobrefs.
type FileReader struct {
	// Immutable stuff:
	*io.SectionReader             // provides Read, etc.
	parent            *FileReader // or nil for sub-region readers to find the ssm map in getSuperset
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

// NewFileReader returns a new FileReader reading the contents of fileBlobRef,
// fetching blobs from fetcher.  The fileBlobRef must be of a "bytes" or "file"
// schema blob.
//
// The caller should call Close on the FileReader when done reading.
func NewFileReader(fetcher blob.Fetcher, fileBlobRef blob.Ref) (*FileReader, error) {
	// TODO(bradfitz): make this take a blobref.FetcherAt instead?
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

// LoadAllChunks causes all chunks of the file to be loaded as quickly
// as possible.  The contents are immediately discarded, so it is
// assumed that the fetcher is a caching fetcher.
func (fr *FileReader) LoadAllChunks() {
	offsetc := make(chan int64, 16)
	go func() {
		for off := range offsetc {
			go func(off int64) {
				rc, err := fr.readerForOffset(off)
				if err == nil {
					defer rc.Close()
					var b [1]byte
					rc.Read(b[:]) // fault in the blob
				}
			}(off)
		}
	}()
	go fr.GetChunkOffsets(offsetc)
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
func (fr *FileReader) FileName() string { return fr.ss.FileName }

func (fr *FileReader) Close() error {
	// TODO: close cached blobs?
	return nil
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

// GetChunkOffsets sends c each of the file's chunk offsets.
// The offsets are not necessarily sent in order, and all ranges of the file
// are not necessarily represented if the file contains zero holes.
// The channel c is closed before the function returns, regardless of error.
func (fr *FileReader) GetChunkOffsets(c chan<- int64) error {
	defer close(c)
	firstErrc := make(chan error, 1)
	return fr.sendPartsChunks(c, firstErrc, 0, fr.ss.Parts)
}

// firstErrc is a communication mechanism amongst all outstanding
// superset-fetching goroutines to see if anybody else has failed.  If
// so (a non-blocking read returns something), then the recursive call
// to sendPartsChunks is skipped, hopefully preventing unnecessary
// work.  Whenever a caller receives on firstErrc, it should also send
// back to it.  It's buffered.
func (fr *FileReader) sendPartsChunks(c chan<- int64, firstErrc chan error, off int64, parts []*BytesPart) error {
	var errcs []chan error
	for _, p := range parts {
		switch {
		case p.BlobRef.Valid() && p.BytesRef.Valid():
			return fmt.Errorf("part illegally contained both a blobRef and bytesRef")
		case !p.BlobRef.Valid() && !p.BytesRef.Valid():
			// Don't send
		case p.BlobRef.Valid():
			c <- off
		case p.BytesRef.Valid():
			errc := make(chan error, 1)
			errcs = append(errcs, errc)
			br := p.BytesRef
			go func(off int64) (err error) {
				defer func() {
					errc <- err
					if err != nil {
						select {
						case firstErrc <- err: // pump
						default:
						}
					}
				}()
				select {
				case err = <-firstErrc:
					// There was already an error elsewhere in the file.
					// Avoid doing more work.
					return
				default:
					ss, err := fr.getSuperset(br)
					if err != nil {
						return err
					}
					return fr.sendPartsChunks(c, firstErrc, off, ss.Parts)
				}
			}(off)
		}
		off += int64(p.Size)
	}

	var retErr error
	for _, errc := range errcs {
		if err := <-errc; err != nil && retErr == nil {
			retErr = err
		}
	}
	return retErr
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

var debug = os.Getenv("CAMLI_DEBUG") != ""

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
		return eofReader, nil
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
		return eofReader, nil
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
