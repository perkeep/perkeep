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
	"camli/blobref"
	"camli/blobserver"
	"exec"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
)

var flagOpenImages = flag.Bool("showimages", false, "Show images on receiving them with eog.")

type diskStorage struct {
	root string

	hubLock sync.Mutex
	hubMap  map[blobserver.Partition]blobserver.BlobHub
}

func New(root string) (storage blobserver.Storage, err os.Error) {
	// Local disk.
	fi, staterr := os.Stat(root)
	if staterr != nil || !fi.IsDirectory() {
		err = os.NewError(fmt.Sprintf("Storage root %q doesn't exist or is not a directory.", root))
		return
	}
	storage = &diskStorage{
		root:   root,
		hubMap: make(map[blobserver.Partition]blobserver.BlobHub),
	}
	return
}

func (ds *diskStorage) GetBlobHub(partition blobserver.Partition) blobserver.BlobHub {
	ds.hubLock.Lock()
	defer ds.hubLock.Unlock()
	if hub, ok := ds.hubMap[partition]; ok {
		return hub
	}
	hub := new(blobserver.SimpleBlobHub)
	ds.hubMap[partition] = hub
	return hub
}

func (ds *diskStorage) Fetch(blob *blobref.BlobRef) (blobref.ReadSeekCloser, int64, os.Error) {
	fileName := ds.blobFileName(blob)
	stat, err := os.Stat(fileName)
	if errorIsNoEnt(err) {
		return nil, 0, err
	}
	file, err := os.Open(fileName, os.O_RDONLY, 0)
	if err != nil {
		return nil, 0, err
	}
	return file, stat.Size, nil
}

func (ds *diskStorage) Remove(partition blobserver.Partition, blobs []*blobref.BlobRef) os.Error {
	for _, blob := range blobs {
		fileName := ds.partitionBlobFileName(partition, blob)
		err := os.Remove(fileName)
		switch {
		case err == nil:
			continue
		case errorIsNoEnt(err):
			log.Printf("Deleting already-deleted file; harmless.")
			continue
		default:
			return err
		}
	}
	return nil
}

type readBlobRequest struct {
	ch      chan *blobref.SizedBlobRef
	after   string
	remain  *uint // limit countdown
	dirRoot string

	// Not used on initial request, only on recursion
	blobPrefix, pathInto string
}

type enumerateError struct {
	msg string
	err os.Error
}

func (ee *enumerateError) String() string {
	return fmt.Sprintf("Enumerate error: %s: %v", ee.msg, ee.err)
}

func readBlobs(opts readBlobRequest) os.Error {
	dirFullPath := opts.dirRoot + "/" + opts.pathInto
	dir, err := os.Open(dirFullPath, os.O_RDONLY, 0)
	if err != nil {
		return &enumerateError{"opening directory " + dirFullPath, err}
	}
	defer dir.Close()
	names, err := dir.Readdirnames(32768)
	if err != nil {
		return &enumerateError{"readdirnames of " + dirFullPath, err}
	}
	sort.SortStrings(names)
	for _, name := range names {
		if *opts.remain == 0 {
			return nil
		}

		fullPath := dirFullPath + "/" + name
		fi, err := os.Stat(fullPath)
		if err != nil {
			return &enumerateError{"stat of file " + fullPath, err}
		}

		if fi.IsDirectory() {
			var newBlobPrefix string
			if opts.blobPrefix == "" {
				newBlobPrefix = name + "-"
			} else {
				newBlobPrefix = opts.blobPrefix + name
			}
			if len(opts.after) > 0 {
				compareLen := len(newBlobPrefix)
				if len(opts.after) < compareLen {
					compareLen = len(opts.after)
				}
				if newBlobPrefix[0:compareLen] < opts.after[0:compareLen] {
					continue
				}
			}
			ropts := opts
			ropts.blobPrefix = newBlobPrefix
			ropts.pathInto = opts.pathInto + "/" + name
			readBlobs(ropts)
			continue
		}

		if fi.IsRegular() && strings.HasSuffix(name, ".dat") {
			blobName := name[0 : len(name)-4]
			if blobName <= opts.after {
				continue
			}
			blobRef := blobref.Parse(blobName)
			if blobRef != nil {
				opts.ch <- &blobref.SizedBlobRef{BlobRef: blobRef, Size: fi.Size}
				(*opts.remain)--
			}
			continue
		}
	}

	if opts.pathInto == "" {
		opts.ch <- nil
	}
	return nil
}

func (ds *diskStorage) EnumerateBlobs(dest chan *blobref.SizedBlobRef, partition blobserver.Partition, after string, limit uint) os.Error {
	dirRoot := ds.root
	if partition != "" {
		dirRoot += "/partition/" + string(partition) + "/"
	}
	limitMutable := limit
	return readBlobs(readBlobRequest{
		ch:      dest,
		dirRoot: dirRoot,
		after:   after,
		remain:  &limitMutable,
	})
}

func (ds *diskStorage) Stat(dest chan *blobref.SizedBlobRef, partition blobserver.Partition, blobs []*blobref.BlobRef, waitSeconds int) os.Error {
	var missing []*blobref.BlobRef

	// TODO: stat in parallel; keep disks busy
	for _, ref := range blobs {
		fi, err := os.Stat(ds.blobFileName(ref))
		switch {
		case err == nil && fi.IsRegular():
			dest <- &blobref.SizedBlobRef{BlobRef: ref, Size: fi.Size}
		case err != nil && errorIsNoEnt(err) && waitSeconds > 0:
			missing = append(missing, ref)
		case err != nil && !errorIsNoEnt(err):
			return err
		}
	}

	if len(missing) > 0 {
		// TODO: use waitSeconds
		log.Printf("TODO: wait for %d blobs: %#v", len(missing), missing)
	}

	return nil
}

var CorruptBlobError = os.NewError("corrupt blob; digest doesn't match")

func (ds *diskStorage) ReceiveBlob(blobRef *blobref.BlobRef, source io.Reader, mirrorPartitions []blobserver.Partition) (blobGot *blobref.SizedBlobRef, err os.Error) {
	hashedDirectory := ds.blobDirectoryName(blobRef)
	err = os.MkdirAll(hashedDirectory, 0700)
	if err != nil {
		return
	}

	var tempFile *os.File
	tempFile, err = ioutil.TempFile(hashedDirectory, BlobFileBaseName(blobRef)+".tmp")
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
	var written int64
	written, err = io.Copy(io.MultiWriter(hash, tempFile), source)
	if err != nil {
		return
	}
	// TODO: fsync before close.
	if err = tempFile.Close(); err != nil {
		return
	}

	if !blobRef.HashMatches(hash) {
		err = CorruptBlobError
		return
	}

	fileName := ds.blobFileName(blobRef)
	if err = os.Rename(tempFile.Name(), fileName); err != nil {
		return
	}

	stat, err := os.Lstat(fileName)
	if err != nil {
		return
	}
	if !stat.IsRegular() || stat.Size != written {
		err = os.NewError("Written size didn't match.")
		return
	}

	for _, partition := range mirrorPartitions {
		partitionDir := ds.blobPartitionDirectoryName(partition, blobRef)
		if err = os.MkdirAll(partitionDir, 0700); err != nil {
			return
		}
		partitionFileName := ds.partitionBlobFileName(partition, blobRef)
		if err = os.Link(fileName, partitionFileName); err != nil {
			return
		}
		log.Printf("Mirrored to partition %q", partition)
	}

	blobGot = &blobref.SizedBlobRef{BlobRef: blobRef, Size: stat.Size}
	success = true

	if *flagOpenImages {
		exec.Run("/usr/bin/eog",
			[]string{"/usr/bin/eog", fileName},
			os.Environ(),
			"/",
			exec.DevNull,
			exec.DevNull,
			exec.MergeWithStdout)
	}

	hub := ds.GetBlobHub(blobserver.DefaultPartition)
	hub.NotifyBlobReceived(blobRef)
	for _, partition := range mirrorPartitions {
		hub = ds.GetBlobHub(partition)
		hub.NotifyBlobReceived(blobRef)
	}

	return
}

func BlobFileBaseName(b *blobref.BlobRef) string {
	return fmt.Sprintf("%s-%s.dat", b.HashName(), b.Digest())
}

func (ds *diskStorage) blobPartitionDirName(partitionDirSlash string, b *blobref.BlobRef) string {
	d := b.Digest()
	if len(d) < 6 {
		d = d + "______"
	}
	return fmt.Sprintf("%s/%s%s/%s/%s",
		ds.root, partitionDirSlash,
		b.HashName(), d[0:3], d[3:6])
}

func (ds *diskStorage) blobDirectoryName(b *blobref.BlobRef) string {
	return ds.blobPartitionDirName("", b)
}

func (ds *diskStorage) blobFileName(b *blobref.BlobRef) string {
	return fmt.Sprintf("%s/%s-%s.dat", ds.blobDirectoryName(b), b.HashName(), b.Digest())
}

func (ds *diskStorage) blobPartitionDirectoryName(partition blobserver.Partition, b *blobref.BlobRef) string {
	return ds.blobPartitionDirName("partition/"+string(partition)+"/", b)
}

func (ds *diskStorage) partitionBlobFileName(partition blobserver.Partition, b *blobref.BlobRef) string {
	return fmt.Sprintf("%s/%s-%s.dat", ds.blobPartitionDirectoryName(partition, b), b.HashName(), b.Digest())
}
