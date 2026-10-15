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

	"perkeep.org/pkg/blob"
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

	fileName := ds.blobPath(blobRef)
	if err := ds.fs.Rename(tempFile.Name(), fileName); err != nil {
		return blob.SizedRef{}, err
	}
	if syncer, ok := ds.fs.(DirSyncer); ok {
		// Make the new name durable, not just the contents.
		if err := syncer.SyncDir(hashedDirectory); err != nil {
			return blob.SizedRef{}, err
		}
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
