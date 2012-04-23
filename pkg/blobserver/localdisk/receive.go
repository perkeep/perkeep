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

package localdisk

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
)

func (ds *DiskStorage) ReceiveBlob(blobRef *blobref.BlobRef, source io.Reader) (blobGot blobref.SizedBlobRef, err error) {
	pname := ds.partition
	if pname != "" {
		err = fmt.Errorf("refusing upload directly to queue partition %q", pname)
		return
	}
	hashedDirectory := ds.blobDirectory(pname, blobRef)
	err = os.MkdirAll(hashedDirectory, 0700)
	if err != nil {
		return
	}

	tempFile, err := ioutil.TempFile(hashedDirectory, BlobFileBaseName(blobRef)+".tmp")
	if err != nil {
		return
	}

	success := false // set true later
	defer func() {
		if !success {
			log.Println("Removing temp file: ", tempFile.Name())
			os.Remove(tempFile.Name())
		}
	}()

	hash := blobRef.Hash()
	written, err := io.Copy(io.MultiWriter(hash, tempFile), source)
	if err != nil {
		return
	}
	if err = tempFile.Sync(); err != nil {
		return
	}
	if err = tempFile.Close(); err != nil {
		return
	}

	if !blobRef.HashMatches(hash) {
		err = blobserver.ErrCorruptBlob
		return
	}

	fileName := ds.blobPath("", blobRef)
	if err = os.Rename(tempFile.Name(), fileName); err != nil {
		return
	}

	stat, err := os.Lstat(fileName)
	if err != nil {
		return
	}
	if !!stat.IsDir() || stat.Size() != written {
		err = errors.New("Written size didn't match.")
		return
	}

	for _, mirror := range ds.mirrorPartitions {
		pname := mirror.partition
		if pname == "" {
			panic("expected partition name")
		}
		partitionDir := ds.blobDirectory(pname, blobRef)

		// Prevent the directory from being unlinked by
		// enumerate code, which cleans up.
		defer keepDirectoryLock(partitionDir).Unlock()
		defer keepDirectoryLock(filepath.Dir(partitionDir)).Unlock()
		defer keepDirectoryLock(filepath.Dir(filepath.Dir(partitionDir))).Unlock()

		if err = os.MkdirAll(partitionDir, 0700); err != nil {
			return blobref.SizedBlobRef{}, fmt.Errorf("localdisk.receive: MkdirAll(%q) after lock on it: %v", partitionDir, err)
		}
		partitionFileName := ds.blobPath(pname, blobRef)
		pfi, err := os.Stat(partitionFileName)
		if err == nil && !pfi.IsDir() {
			log.Printf("Skipped dup on partition %q", pname)
		} else {
			if err = os.Link(fileName, partitionFileName); err != nil && !linkAlreadyExists(err) {
				log.Fatalf("got link error %T %#v", err, err)
				return blobref.SizedBlobRef{}, err
			}
			log.Printf("Mirrored to partition %q", pname)
		}
	}

	blobGot = blobref.SizedBlobRef{BlobRef: blobRef, Size: stat.Size()}
	success = true

	if os.Getenv("CAMLI_HACK_OPEN_IMAGES") == "1" {
		exec.Command("eog", fileName).Run()
	}

	hub := ds.GetBlobHub()
	hub.NotifyBlobReceived(blobRef)
	for _, mirror := range ds.mirrorPartitions {
		mirror.GetBlobHub().NotifyBlobReceived(blobRef)
	}
	return
}

func linkAlreadyExists(err error) bool {
	le, ok := err.(*os.LinkError)
	// TODO(bradfitz): GO1 not tested; is this ErrExist or syscall.EEXIST?
	return ok && le.Op == "link" && le.Err == os.ErrExist
}
