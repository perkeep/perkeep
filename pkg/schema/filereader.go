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

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/singleflight"
)

var _ = log.Printf

const closedIndex = -1

var errClosed = errors.New("filereader is closed")

// A FileReader reads the bytes of "file" and "bytes" schema blobrefs.
type FileReader struct {
	// Immutable stuff:
	*io.SectionReader             // provides Read, etc.
	parent            *FileReader // or nil for sub-region readers to find the ssm map in getSuperset
	rootOff           int64       // this FileReader's offset from the root
	fetcher           blobref.SeekFetcher
	ss                *Superset
	size              int64 // total number of bytes

	sfg singleflight.Group // for loading blobrefs for ssm

	ssmmu sync.Mutex           // guards ssm
	ssm   map[string]*Superset // blobref -> superset
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

// NewFileReader returns a new FileReader, reading bytes and blobs
// from the provided fetcher.
//
// NewFileReader does no fetch operation on the fetcher itself.  The
// fetcher is only used in subsequent read operations.
//
// An error is only returned if the type of the Superset is not either
// "file" or "bytes".
func (ss *Superset) NewFileReader(fetcher blobref.SeekFetcher) (*FileReader, error) {
	if ss.Type != "file" && ss.Type != "bytes" {
		return nil, fmt.Errorf("schema/filereader: Superset not of type \"file\" or \"bytes\"")
	}
	size := int64(ss.SumPartsSize())
	fr := &FileReader{
		fetcher: fetcher,
		ss:      ss,
		size:    size,
		ssm:     make(map[string]*Superset),
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

// FileSchema returns the reader's schema superset. Don't mutate it.
func (fr *FileReader) FileSchema() *Superset {
	return fr.ss
}

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
	return fr.sendPartsChunks(c, 0, fr.ss.Parts)
}

func (fr *FileReader) sendPartsChunks(c chan<- int64, off int64, parts []*BytesPart) error {
	var errcs []chan error
	for _, p := range parts {
		switch {
		case p.BlobRef != nil && p.BytesRef != nil:
			return fmt.Errorf("part illegally contained both a blobRef and bytesRef")
		case p.BlobRef == nil && p.BytesRef == nil:
			// Don't send
		case p.BlobRef != nil:
			c <- off
		case p.BytesRef != nil:
			errc := make(chan error, 1)
			errcs = append(errcs, errc)
			br := p.BytesRef
			offNow := off
			go func() {
				ss, err := fr.getSuperset(br)
				if err != nil {
					errc <- err
					return
				}
				errc <- fr.sendPartsChunks(c, offNow, ss.Parts)
			}()
		}
		off += int64(p.Size)
	}
	for _, errc := range errcs {
		if err := <-errc; err != nil {
			return err
		}
	}
	return nil
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

func (fr *FileReader) getSuperset(br *blobref.BlobRef) (*Superset, error) {
	if root := fr.rootReader(); root != fr {
		return root.getSuperset(br)
	}
	brStr := br.String()
	ssi, err := fr.sfg.Do(brStr, func() (interface{}, error) {
		fr.ssmmu.Lock()
		ss, ok := fr.ssm[brStr]
		fr.ssmmu.Unlock()
		if ok {
			return ss, nil
		}
		rsc, _, err := fr.fetcher.Fetch(br)
		if err != nil {
			return nil, fmt.Errorf("schema/filereader: fetching file schema blob: %v", err)
		}
		defer rsc.Close()
		ss, err = ParseSuperset(rsc)
		if err != nil {
			return nil, err
		}
		fr.ssmmu.Lock()
		defer fr.ssmmu.Unlock()
		fr.ssm[brStr] = ss
		return ss, nil
	})
	if err != nil {
		return nil, err
	}
	return ssi.(*Superset), nil
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
		var ss *Superset
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
