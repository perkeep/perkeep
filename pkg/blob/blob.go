/*
Copyright 2014 The Perkeep Authors

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

package blob

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"sync/atomic"
	"unicode/utf8"

	"perkeep.org/pkg/constants"
)

// Blob represents a blob. Use the methods Size, SizedRef and
// ReadAll to query and get data from Blob.
type Blob struct {
	ref     Ref
	size    uint32
	readAll func(context.Context) ([]byte, error)
	mem     atomic.Value // of []byte, if in memory
}

// NewBlob constructs a Blob from its Ref, size and a function that
// returns the contents of the blob. Any error in the function newReader when
// constructing the io.ReadCloser should be returned upon the first call to Read or
// Close.
func NewBlob(ref Ref, size uint32, readAll func(ctx context.Context) ([]byte, error)) *Blob {
	return &Blob{
		ref:     ref,
		size:    size,
		readAll: readAll,
	}
}

// Size returns the size of the blob (in bytes).
func (b *Blob) Size() uint32 {
	return b.size
}

// SizedRef returns the SizedRef corresponding to the blob.
func (b *Blob) SizedRef() SizedRef {
	return SizedRef{b.ref, b.size}
}

// Ref returns the blob's reference.
func (b *Blob) Ref() Ref { return b.ref }

// ReadAll reads the blob completely to memory, using the provided
// context, and then returns a reader over it. The Reader will not
// have errors except EOF.
//
// The provided is only ctx is used until ReadAll returns.
func (b *Blob) ReadAll(ctx context.Context) (*bytes.Reader, error) {
	mem, err := b.getMem(ctx)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(mem), nil
}

func (b *Blob) getMem(ctx context.Context) ([]byte, error) {
	mem, ok := b.mem.Load().([]byte)
	if ok {
		return mem, nil
	}
	mem, err := b.readAll(ctx)
	if err != nil {
		return nil, err
	}
	if uint32(len(mem)) != b.size {
		return nil, fmt.Errorf("Blob.ReadAll read %d bytes of %v; expected %d", len(mem), b.ref, b.size)
	}
	b.mem.Store(mem)
	return mem, nil
}

// ValidContents reports whether the hash of blob's content matches
// its reference.
func (b *Blob) ValidContents(ctx context.Context) error {
	mem, err := b.getMem(ctx)
	if err != nil {
		return err
	}
	h := b.ref.Hash()
	h.Write(mem)
	if !b.ref.HashMatches(h) {
		return fmt.Errorf("blob contents don't match digest for ref %v", b.ref)
	}
	return nil
}

// IsUTF8 reports whether the blob is entirely UTF-8.
func (b *Blob) IsUTF8(ctx context.Context) (bool, error) {
	mem, err := b.getMem(ctx)
	if err != nil {
		return false, err
	}
	return utf8.Valid(mem), nil
}

// A reader reads a blob's contents.
// It adds a no-op Close method to a *bytes.Reader.
type reader struct {
	*bytes.Reader
}

func (reader) Close() error { return nil }

// FromFetcher fetches br from fetcher and slurps its contents to
// memory. It does not validate the blob's digest.  Use the
// Blob.ValidContents method for that.
func FromFetcher(ctx context.Context, fetcher Fetcher, br Ref) (*Blob, error) {
	rc, size, err := fetcher.Fetch(ctx, br)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return FromReader(ctx, br, rc, size)
}

// FromReader slurps the given blob from r to memory.
// It does not validate the blob's digest.  Use the
// Blob.ValidContents method for that.
func FromReader(ctx context.Context, br Ref, r io.Reader, size uint32) (*Blob, error) {
	if size > constants.MaxBlobSize {
		return nil, fmt.Errorf("blob: %v with reported size %d is over max size of %d", br, size, constants.MaxBlobSize)
	}
	buf := make([]byte, size)
	// TODO: use ctx here during ReadFull? add context-checking Reader wrapper?
	if n, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("blob: after reading %d bytes of %v: %v", n, br, err)
	}
	n, _ := io.CopyN(ioutil.Discard, r, 1)
	if n > 0 {
		return nil, fmt.Errorf("blob: %v had more than reported %d bytes", br, size)
	}
	b := NewBlob(br, uint32(size), func(context.Context) ([]byte, error) {
		return buf, nil
	})
	b.mem.Store(buf)
	return b, nil
}
