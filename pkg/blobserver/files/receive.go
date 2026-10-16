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

package files

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sync"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

func (ds *Storage) startGate() {
	if ds.tmpFileGate == nil {
		return
	}
	ds.tmpFileGate.Start()
}

func (ds *Storage) doneGate() {
	if ds.tmpFileGate == nil {
		return
	}
	ds.tmpFileGate.Done()
}

func (ds *Storage) ReceiveBlob(ctx context.Context, blobRef blob.Ref, source io.Reader) (blob.SizedRef, error) {
	ds.dirLockMu.RLock()
	defer ds.dirLockMu.RUnlock()

	sb, err := ds.writeBlobLocked(blobRef, source)
	if err != nil {
		return blob.SizedRef{}, err
	}
	// Make the new name durable, not just the contents.
	if err := ds.syncDir(ds.blobDirectory(blobRef)); err != nil {
		return blob.SizedRef{}, err
	}
	return sb, nil
}

// receiveBlobsConcurrency is how many blobs ReceiveBlobs writes at once,
// and how many directories it syncs at once. It's high because the
// more syncs are in flight together, the more the filesystem can combine:
// on ext4, receiving 64KB blobs in groups of 256, this many at once was
// about 3x as fast as 16 at once. The new file gate (SetNewFileGate)
// still limits how many files are open.
const receiveBlobsConcurrency = 256

// ReceiveBlobs implements blobserver.BlobsReceiver. It writes and syncs the
// blobs' files concurrently, and then syncs each of their directories
// once, also concurrently. Each blob still needs its own sync, and blobs
// rarely share a directory (there are 65536 for each hash), but
// filesystems such as ext4 and XFS can combine concurrent syncs into one
// journal commit.
func (ds *Storage) ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error {
	ds.dirLockMu.RLock()
	defer ds.dirLockMu.RUnlock()

	errs := make([]error, len(blobs))
	forEach(len(blobs), func(i int) {
		if errs[i] = ctx.Err(); errs[i] != nil {
			return
		}
		b := blobs[i]
		r, err := b.ReadAll(ctx)
		if err == nil {
			_, err = ds.writeBlobLocked(b.Ref(), r)
		}
		errs[i] = err
	})

	be := &blobserver.BlobsError{Op: "receive"}
	var dirs []string
	dirBlobs := map[string][]blob.Ref{} // the written blobs in each dir
	for i, b := range blobs {
		if errs[i] != nil {
			be.Add(b.Ref(), errs[i])
			continue
		}
		dir := ds.blobDirectory(b.Ref())
		if _, ok := dirBlobs[dir]; !ok {
			dirs = append(dirs, dir)
		}
		dirBlobs[dir] = append(dirBlobs[dir], b.Ref())
	}
	dirErrs := make([]error, len(dirs))
	forEach(len(dirs), func(i int) {
		dirErrs[i] = ds.syncDir(dirs[i])
	})
	for i, dir := range dirs {
		for _, br := range dirBlobs[dir] {
			be.Add(br, dirErrs[i])
		}
	}
	return be.OrNil()
}

// forEach calls fn(i) for each i in [0, n), with up to
// receiveBlobsConcurrency calls at once.
func forEach(n int, fn func(int)) {
	workers := min(n, receiveBlobsConcurrency)
	if workers <= 1 {
		for i := range n {
			fn(i)
		}
		return
	}
	next := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range next {
				fn(i)
			}
		})
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
}

// syncDir syncs dir, if ds.fs is a DirSyncer.
func (ds *Storage) syncDir(dir string) error {
	if syncer, ok := ds.fs.(DirSyncer); ok {
		return syncer.SyncDir(dir)
	}
	return nil
}

// writeBlobLocked writes blobRef's contents from source to a temporary
// file, syncs it, and renames it into place, but doesn't sync its
// directory. ds.dirLockMu must be held for reading.
func (ds *Storage) writeBlobLocked(blobRef blob.Ref, source io.Reader) (blob.SizedRef, error) {
	hashedDirectory := ds.blobDirectory(blobRef)
	if err := ds.mkdirDurable(hashedDirectory); err != nil {
		return blob.SizedRef{}, err
	}

	// TODO(mpl): warn when we hit the gate, and at a limited rate, like maximum once a minute.
	// Deferring to another CL, since it requires modifications to syncutil.Gate first.
	ds.startGate()
	tempFile, err := ds.fs.TempFile(hashedDirectory, blobFileBaseName(blobRef)+".tmp")
	if err != nil {
		ds.doneGate()
		return blob.SizedRef{}, err
	}

	success := false // set true later
	defer func() {
		if !success {
			log.Println("Removing temp file: ", tempFile.Name())
			ds.fs.Remove(tempFile.Name())
		}
		ds.doneGate()
	}()

	written, err := io.Copy(tempFile, source)
	if err != nil {
		return blob.SizedRef{}, err
	}
	fileName := ds.blobPath(blobRef)
	if fi, err := ds.fs.Lstat(fileName); err == nil && fi.Size() == written {
		// The blob is already stored (blobs are content-addressed, and
		// source matched its ref), so keep that copy rather than
		// replacing it, which would cost a sync, and which fails on
		// Windows while anything has the file open. Another receive of
		// it may not have made its name durable yet, but the caller
		// syncs the directory anyway.
		tempFile.Close()
		ds.fs.Remove(tempFile.Name())
		success = true // the temp file is already removed
		return blob.SizedRef{Ref: blobRef, Size: uint32(written)}, nil
	}
	if err = tempFile.Sync(); err != nil {
		return blob.SizedRef{}, err
	}
	if err = tempFile.Close(); err != nil {
		return blob.SizedRef{}, err
	}
	stat, err := ds.fs.Lstat(tempFile.Name())
	if err != nil {
		return blob.SizedRef{}, err
	}
	if stat.Size() != written {
		return blob.SizedRef{}, fmt.Errorf("temp file %q size %d didn't match written size %d", tempFile.Name(), stat.Size(), written)
	}

	if err := ds.fs.Rename(tempFile.Name(), fileName); err != nil {
		return blob.SizedRef{}, err
	}

	stat, err = ds.fs.Lstat(fileName)
	if err != nil {
		return blob.SizedRef{}, err
	}
	if stat.Size() != written {
		return blob.SizedRef{}, fmt.Errorf("files: wrote %d bytes but stat after said %d bytes", written, stat.Size())
	}

	success = true // used in defer above
	return blob.SizedRef{Ref: blobRef, Size: uint32(stat.Size())}, nil
}

// mkdirDurable is like MkdirAll for dir, a directory under ds.root, but if
// ds.fs is a DirSyncer, it also makes the entries of dir and its parents,
// up to ds.root, durable. ds.root itself must already be durable.
// ds.dirLockMu must be held for reading.
func (ds *Storage) mkdirDurable(dir string) error {
	if _, ok := ds.durableDirs.Load(dir); ok {
		return nil
	}
	if err := ds.fs.MkdirAll(dir, 0700); err != nil {
		return err
	}
	syncer, ok := ds.fs.(DirSyncer)
	if !ok {
		return nil
	}
	// Find the directories not yet known to be durable, then sync their
	// parents from the top down, so a directory is only recorded as
	// durable once its parents are.
	root := filepath.Clean(ds.root)
	var pending []string
	for d := dir; d != root && filepath.Dir(d) != d; d = filepath.Dir(d) {
		if _, ok := ds.durableDirs.Load(d); ok {
			break
		}
		pending = append(pending, d)
	}
	for i := len(pending) - 1; i >= 0; i-- {
		d := pending[i]
		if err := syncer.SyncDir(filepath.Dir(d)); err != nil {
			return err
		}
		ds.durableDirs.Store(d, true)
	}
	return nil
}
