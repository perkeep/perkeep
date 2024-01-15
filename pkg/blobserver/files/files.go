/*
Copyright 2018 The Perkeep Authors

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

/*
Package files implements the blobserver interface by storing each blob
in its own file in nested directories. Users don't use the "files"
type directly; it's used by "localdisk" and in the future "sftp" and
"webdav".
*/
package files // import "perkeep.org/pkg/blobserver/files"

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"

	"go4.org/syncutil"
)

// VFS describes the virtual filesystem needed by this package.
//
// It is currently not possible to use this from other packages, but
// that will change.
type VFS interface {
	Remove(string) error // files, not directories
	RemoveDir(string) error
	Stat(string) (os.FileInfo, error)
	Lstat(string) (os.FileInfo, error)
	Open(string) (ReadableFile, error)
	MkdirAll(path string, perm os.FileMode) error

	// Rename is a POSIX-style rename, overwriting newname if it exists.
	Rename(oldname, newname string) error

	// TempFile should behave like os.CreateTemp
	TempFile(dir, prefix string) (WritableFile, error)

	ReadDirNames(dir string) ([]string, error)
}

// WritableFile is the interface required by files opened for Write
// from VFS.TempFile.
type WritableFile interface {
	io.Writer
	io.Closer
	// Name returns the full path to the file.
	// It should behave like (*os.File).Name.
	Name() string
	// Sync fsyncs the file, like (*os.File).Sync.
	Sync() error
}

// ReadableFile is the interface required by read-only files
// returned by VFS.Open.
type ReadableFile interface {
	io.Reader
	io.Seeker
	io.Closer
}

type Storage struct {
	fs   VFS
	root string

	// dirLockMu must be held for writing when deleting an empty directory
	// and for read when receiving blobs.
	dirLockMu *sync.RWMutex

	// statGate limits how many pending Stat calls we have in flight.
	statGate *syncutil.Gate

	// tmpFileGate limits the number of temporary files open at the same
	// time, so we don't run into the max set by ulimit. It is nil on
	// systems (Windows) where we don't know the maximum number of open
	// file descriptors.
	tmpFileGate *syncutil.Gate
}

// SetNewFileGate sets a gate (counting semaphore) on the number of new files
// that may be opened for writing at a time.
func (ds *Storage) SetNewFileGate(g *syncutil.Gate) { ds.tmpFileGate = g }

func NewStorage(fs VFS, root string) *Storage {
	return &Storage{
		fs:        fs,
		root:      root,
		dirLockMu: new(sync.RWMutex),
		statGate:  syncutil.NewGate(10), // arbitrary, but bounded; be more clever later?
	}
}

func (ds *Storage) tryRemoveDir(dir string) {
	ds.dirLockMu.Lock()
	defer ds.dirLockMu.Unlock()
	ds.fs.RemoveDir(dir) // ignore error
}

func (ds *Storage) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	return ds.fetch(ctx, br, 0, -1)
}

func (ds *Storage) SubFetch(ctx context.Context, br blob.Ref, offset, length int64) (io.ReadCloser, error) {
	if offset < 0 || length < 0 {
		return nil, blob.ErrNegativeSubFetch
	}
	rc, _, err := ds.fetch(ctx, br, offset, length)
	return rc, err
}

// u32 converts n to an uint32, or panics if n is out of range
func u32(n int64) uint32 {
	if n < 0 || n > math.MaxUint32 {
		panic("bad size " + fmt.Sprint(n))
	}
	return uint32(n)
}

// length -1 means entire file
func (ds *Storage) fetch(ctx context.Context, br blob.Ref, offset, length int64) (rc io.ReadCloser, size uint32, err error) {
	// TODO: use ctx, if the os package ever supports that.
	fileName := ds.blobPath(br)
	stat, err := ds.fs.Stat(fileName)
	if os.IsNotExist(err) {
		return nil, 0, os.ErrNotExist
	}
	size = u32(stat.Size())
	file, err := ds.fs.Open(fileName)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.ErrNotExist
		}
		return nil, 0, err
	}
	// normal Fetch
	if length < 0 && offset == 0 {
		return file, size, nil
	}
	// SubFetch:
	if offset < 0 || offset > stat.Size() {
		if offset < 0 {
			return nil, 0, blob.ErrNegativeSubFetch
		}
		return nil, 0, blob.ErrOutOfRangeOffsetSubFetch
	}
	if offset != 0 {
		if at, err := file.Seek(offset, io.SeekStart); err != nil || at != offset {
			file.Close()
			return nil, 0, fmt.Errorf("localdisk: error seeking to %d: got %v, %v", offset, at, err)
		}
	}
	return struct {
		io.Reader
		io.Closer
	}{
		Reader: io.LimitReader(file, length),
		Closer: file,
	}, 0 /* unused */, nil
}

func (ds *Storage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	for _, blob := range blobs {
		fileName := ds.blobPath(blob)
		err := ds.fs.Remove(fileName)
		switch {
		case err == nil:
			continue
		case os.IsNotExist(err):
			// deleting already-deleted file; harmless.
			continue
		default:
			return err
		}
	}
	return nil
}

func blobFileBaseName(b blob.Ref) string {
	return fmt.Sprintf("%s-%s.dat", b.HashName(), b.Digest())
}

func (ds *Storage) blobDirectory(b blob.Ref) string {
	d := b.Digest()
	if len(d) < 4 {
		d = d + "____"
	}
	return filepath.Join(ds.root, b.HashName(), d[0:2], d[2:4])
}

func (ds *Storage) blobPath(b blob.Ref) string {
	return filepath.Join(ds.blobDirectory(b), blobFileBaseName(b))
}

const maxParallelStats = 20

var statGate = syncutil.NewGate(maxParallelStats)

func (ds *Storage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	return blobserver.StatBlobsParallelHelper(ctx, blobs, fn, statGate, func(ref blob.Ref) (sb blob.SizedRef, err error) {
		fi, err := ds.fs.Stat(ds.blobPath(ref))
		switch {
		case err == nil && fi.Mode().IsRegular():
			return blob.SizedRef{Ref: ref, Size: u32(fi.Size())}, nil
		case err != nil && !os.IsNotExist(err):
			return sb, err
		}
		return sb, nil

	})
}
