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

	"camlistore.org/pkg/atomics"
	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/singleflight"
)

var _ = log.Printf

const closedIndex = -1

var errClosed = errors.New("filereader is closed")

// A FileReader reads the bytes of "file" and "bytes" schema blobrefs.
type FileReader struct {
	*io.SectionReader

	ssmmu sync.Mutex           // guards ssm
	ssm   map[string]*Superset // blobref -> superset

	sfg singleflight.Group // for loading blobrefs for ssm

	fetcher blobref.SeekFetcher
	ss      *Superset

	size int64 // total number of bytes

	readAll atomics.Bool // whether to preload aggressively
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

func (fr *FileReader) WriteTo(w io.Writer) (n int64, err error) {
	// WriteTo is called by io.Copy. Use this as a signal that we're going to want
	// to read the rest of the file and go into an aggressive read-ahead mode.
	fr.readAll.Set(true)

	// TODO: actually use readAll somehow.

	// Now just do a normal copy, but hide fr's WriteTo method from io.Copy:
	return io.Copy(w, struct{ io.Reader }{fr})
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

func (fr *FileReader) getSuperset(br *blobref.BlobRef) (*Superset, error) {
	brStr := br.String()

	// Check cache first.
	fr.ssmmu.Lock()
	ss, ok := fr.ssm[brStr]
	fr.ssmmu.Unlock()
	if ok {
		return ss, nil
	}

	ssi, err := fr.sfg.Do(brStr, func() (interface{}, error) {
		rsc, _, err := fr.fetcher.Fetch(br)
		if err != nil {
			return nil, fmt.Errorf("schema/filereader: fetching file schema blob: %v", err)
		}
		defer rsc.Close()
		return ParseSuperset(rsc)
	})
	if err != nil {
		return nil, err
	}
	return ssi.(*Superset), nil
}

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
		var ss *Superset
		ss, err = fr.getSuperset(p0.BytesRef)
		if err != nil {
			return nil, err
		}
		rsc, err = ss.NewFileReader(fr.fetcher)
		if err == nil && fr.readAll.Get() {
			rsc.(*FileReader).readAll.Set(true)
			// TODO: tell it to start faulting everything in
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
