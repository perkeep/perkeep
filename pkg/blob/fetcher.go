/*
Copyright 2011 The Perkeep Authors

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
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

var (
	ErrNegativeSubFetch         = errors.New("invalid negative subfetch parameters")
	ErrOutOfRangeOffsetSubFetch = errors.New("subfetch offset greater than blob size")
)

// Fetcher is the minimal interface for retrieving a blob from storage.
// The full storage interface is blobserver.Storage.
type Fetcher interface {
	// Fetch returns a blob. If the blob is not found then
	// os.ErrNotExist should be returned for the error (not a wrapped
	// error with a ErrNotExist inside)
	//
	// The contents are not guaranteed to match the digest of the
	// provided Ref (e.g. when streamed over HTTP). Paranoid
	// callers should verify them.
	//
	// The caller must close blob.
	//
	// The provided context is used until blob is closed and its
	// cancelation should but may not necessarily cause reads from
	// blob to fail with an error.
	Fetch(context.Context, Ref) (blob io.ReadCloser, size uint32, err error)
}

// ErrUnimplemented is returned by optional interfaces when their
// wrapped values don't implemented the optional interface.
var ErrUnimplemented = errors.New("optional method not implemented")

// A SubFetcher is a Fetcher that can retrieve part of a blob.
type SubFetcher interface {
	// SubFetch returns part of a blob.
	// The caller must close the returned io.ReadCloser.
	// The Reader may return fewer than 'length' bytes. Callers should
	// check. The returned error should be: ErrNegativeSubFetch if any of
	// offset or length is negative, or os.ErrNotExist if the blob
	// doesn't exist, or ErrOutOfRangeOffsetSubFetch if offset goes over
	// the size of the blob. If the error is ErrUnimplemented, the caller should
	// treat this Fetcher as if it doesn't implement SubFetcher.
	SubFetch(ctx context.Context, ref Ref, offset, length int64) (io.ReadCloser, error)
}

func NewSerialFetcher(fetchers ...Fetcher) Fetcher {
	return &serialFetcher{fetchers}
}

func NewSimpleDirectoryFetcher(dir string) *DirFetcher {
	return &DirFetcher{dir, "camli"}
}

type serialFetcher struct {
	fetchers []Fetcher
}

func (sf *serialFetcher) Fetch(ctx context.Context, r Ref) (file io.ReadCloser, size uint32, err error) {
	for _, fetcher := range sf.fetchers {
		file, size, err = fetcher.Fetch(ctx, r)
		if err == nil {
			return
		}
	}
	return
}

type DirFetcher struct {
	directory, extension string
}

func (df *DirFetcher) Fetch(ctx context.Context, r Ref) (file io.ReadCloser, size uint32, err error) {
	fileName := fmt.Sprintf("%s/%s.%s", df.directory, r.String(), df.extension)
	var stat os.FileInfo
	stat, err = os.Stat(fileName)
	if err != nil {
		return
	}
	if stat.Size() > math.MaxUint32 {
		err = errors.New("file size too big")
		return
	}
	file, err = os.Open(fileName)
	if err != nil {
		return
	}
	size = uint32(stat.Size())
	return
}

// ReaderAt returns an io.ReaderAt of br, fetching against sf.
// The context is stored in and used by the returned ReaderAt.
func ReaderAt(ctx context.Context, sf SubFetcher, br Ref) io.ReaderAt {
	return readerAt{ctx, sf, br}
}

type readerAt struct {
	ctx context.Context
	sf  SubFetcher
	br  Ref
}

func (ra readerAt) ReadAt(p []byte, off int64) (n int, err error) {
	rc, err := ra.sf.SubFetch(ra.ctx, ra.br, off, int64(len(p)))
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return io.ReadFull(rc, p)
}
