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

package main

import (
	"bytes"
	"fmt"
	"log"
	"io"
	"json"
	"os"
	"path/filepath"
	"sync"

	"camli/blobref"
	"camli/schema"
	"camli/third_party/github.com/hanwen/go-fuse/fuse"
)

var _ = fmt.Println
var _ = log.Println

type CamliFileSystem struct {
	fuse.DefaultPathFilesystem

	fetcher blobref.Fetcher
	root    *blobref.BlobRef

	lk         sync.Mutex
	nameToBlob map[string]*blobref.BlobRef
}

func NewCamliFileSystem(fetcher blobref.Fetcher, root *blobref.BlobRef) *CamliFileSystem {
	return &CamliFileSystem{
		fetcher:    fetcher,
		root:       root,
		nameToBlob: make(map[string]*blobref.BlobRef),
	}
}

// Where name == "" for root,
// Returns nil on failure
func (fs *CamliFileSystem) blobRefFromNameCached(name string) *blobref.BlobRef {
	fs.lk.Lock()
	defer fs.lk.Unlock()
	return fs.nameToBlob[name]
}

// Errors returned are:
//    os.ENOENT -- blob not found
//    os.EINVAL -- not JSON or a camli schema blob

func (fs *CamliFileSystem) fetchSchemaSuperset(br *blobref.BlobRef) (*schema.Superset, os.Error) {
	// TODO: LRU caching here?  fs.fetcher will also be a caching
	// Fetcher, but we want to avoid re-de-JSONing the blobs on
	// each call.
	rsc, _, err := fs.fetcher.Fetch(br)
	if err != nil {
		return nil, err
	}
	defer rsc.Close()
	jd := json.NewDecoder(rsc)
	ss := new(schema.Superset)
	err = jd.Decode(ss)
	if err != nil {
		log.Printf("Error parsing %s as schema blob: %v", br, err)
		return nil, os.EINVAL
	}
	if ss.Type == "" {
		log.Printf("blob %s is JSON but lacks camliType", br)
		return nil, os.EINVAL
	}
	ss.BlobRef = br
	return ss, nil
}

// Where name == "" for root,
// Returns fuse.Status == fuse.OK on success or anything else on failure.
func (fs *CamliFileSystem) blobRefFromName(name string) (retbr *blobref.BlobRef, retstatus fuse.Status) {
	if name == "" {
		return fs.root, fuse.OK
	}
	if br := fs.blobRefFromNameCached(name); br != nil {
		return br, fuse.OK
	}

	log.Printf("blobRefFromName(%q) = ...", name)
	defer func() {
		log.Printf("blobRefFromName(%q) = %s, %v", name, retbr, retstatus)
	}()

	dir, fileName := filepath.Split(name)
	if len(dir) > 0 {
		dir = dir[:len(dir)-1] // remove trailing "/" or whatever
	}
	dirBlob, fuseStatus := fs.blobRefFromName(dir)
	if fuseStatus != fuse.OK {
		return nil, fuseStatus
	}

	dirss, err := fs.fetchSchemaSuperset(dirBlob)
	switch {
	case err == os.ENOENT:
		log.Printf("Failed to find directory %s", dirBlob)
		return nil, fuse.ENOENT
	case err == os.EINVAL:
		log.Printf("Failed to parse directory %s", dirBlob)
		return nil, fuse.ENOTDIR
	case err != nil:
		panic(fmt.Sprintf("Invalid fetcher error: %v", err))
	case dirss == nil:
		panic("nil dirss")
	case dirss.Type != "directory":
		log.Printf("Expected %s to be a directory; actually a %s",
			dirBlob, dirss.Type)
		return nil, fuse.ENOTDIR
	}

	if dirss.Entries == "" {
		log.Printf("Expected %s to have 'entries'", dirBlob)
		return nil, fuse.ENOTDIR
	}
	entriesBlob := blobref.Parse(dirss.Entries)
	if entriesBlob == nil {
		log.Printf("Blob %s had invalid blobref %q for its 'entries'", dirBlob, dirss.Entries)
		return nil, fuse.ENOTDIR
	}

	entss, err := fs.fetchSchemaSuperset(entriesBlob)
	switch {
	case err == os.ENOENT:
		log.Printf("Failed to find entries %s via directory %s", entriesBlob, dirBlob)
		return nil, fuse.ENOENT
	case err == os.EINVAL:
		log.Printf("Failed to parse entries %s via directory %s", entriesBlob, dirBlob)
		return nil, fuse.ENOTDIR
	case err != nil:
		panic(fmt.Sprintf("Invalid fetcher error: %v", err))
	case entss == nil:
		panic("nil entss")
	case entss.Type != "static-set":
		log.Printf("Expected %s to be a directory; actually a %s",
			dirBlob, dirss.Type)
		return nil, fuse.ENOTDIR
	}

	wg := new(sync.WaitGroup)
	foundCh := make(chan *blobref.BlobRef) // important: unbuffered
	for _, m := range entss.Members {
		wg.Add(1)
		go func(memberBlobstr string) {
			defer wg.Done()
			memberBlob := blobref.Parse(memberBlobstr)
			if memberBlob == nil {
				log.Printf("invalid blobref of %q in static set %s", memberBlobstr, entss)
				return
			}
			childss, err := fs.fetchSchemaSuperset(memberBlob)
			if err == nil && childss.HasFilename(fileName) {
				foundCh <- memberBlob
			}
		}(m)
	}
	failCh := make(chan string)
	go func() {
		wg.Wait()
		failCh <- "ENOENT"
	}()
	select {
	case found := <-foundCh:
		fs.lk.Lock()
		defer fs.lk.Unlock()
		fs.nameToBlob[name] = found
		return found, fuse.OK
	case <-failCh:
	}
	// TODO: negative cache
	return nil, fuse.ENOENT
}

func (fs *CamliFileSystem) Mount(connector *fuse.PathFileSystemConnector) fuse.Status {
	log.Printf("cammount: Mount")
	return fuse.OK
}

func (fs *CamliFileSystem) Unmount() {
	log.Printf("cammount: Unmount.")
}

func (fs *CamliFileSystem) GetAttr(name string) (*fuse.Attr, fuse.Status) {
	blobref, errStatus := fs.blobRefFromName(name)
	log.Printf("cammount: GetAttr(%q) => (%s, %v)", name, blobref, errStatus)
	if errStatus != fuse.OK {
		return nil, errStatus
	}

	ss, err := fs.fetchSchemaSuperset(blobref)
	if err != nil {
		log.Printf("cammount: GetAttr(%q, %s): fetch schema error: %v", name, blobref, err)
		return nil, fuse.EIO
	}

	out := new(fuse.Attr)
	var fi os.FileInfo

	fi.Mode = ss.UnixMode()

	// TODO: have a mode to set permissions equal to mounting user?
	fi.Uid = ss.UnixOwnerId
	fi.Gid = ss.UnixGroupId

	// TODO: other types
	if ss.Type == "file" {
		fi.Size = int64(ss.Size)
	}

	// TODO: mtime and such

	fuse.CopyFileInfo(&fi, out)
	return out, fuse.OK
}

func (fs *CamliFileSystem) Access(name string, mode uint32) fuse.Status {
	log.Printf("cammount: Access(%q, %d)", name, mode)
	return fuse.OK
}

func (fs *CamliFileSystem) Open(name string, flags uint32) (file fuse.RawFuseFile, code fuse.Status) {
	if flags&uint32(os.O_CREAT|os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_TRUNC) != 0 {
		log.Printf("cammount: Open(%q, %d): denying write access", name, flags)
		return nil, fuse.EACCES
	}

	fileblob, errStatus := fs.blobRefFromName(name)
	log.Printf("cammount: Open(%q, %d) => (%s, %v)", name, flags, fileblob, errStatus)
	if errStatus != fuse.OK {
		return nil, errStatus
	}
	ss, err := fs.fetchSchemaSuperset(fileblob)
	if err != nil {
		log.Printf("cammount: Open(%q): %v", name, err)
		return nil, fuse.EIO
	}
	if ss.Type != "file" {
		log.Printf("cammount: Open(%q): %s is a %q, not file", name, fileblob, ss.Type)
		return nil, fuse.EINVAL
	}

	return &CamliFile{nil, fs, fileblob, ss}, fuse.OK
}

func (fs *CamliFileSystem) OpenDir(name string) (stream chan fuse.DirEntry, code fuse.Status) {
	dirBlob, errStatus := fs.blobRefFromName(name)
	log.Printf("cammount: OpenDir(%q), dirBlob=%s err=%v", name, dirBlob, errStatus)
	if errStatus != fuse.OK {
		return nil, errStatus
	}

	dirss, err := fs.fetchSchemaSuperset(dirBlob)
	log.Printf("dirss blob: %v, err=%v", dirss, err)
	if err != nil {
		log.Printf("cammount: OpenDir(%q, %s): fetch schema error: %v", name, dirBlob, err)
		return nil, fuse.EIO
	}

	if dirss.Entries == "" {
		log.Printf("Expected %s to have 'entries'", dirBlob)
		return nil, fuse.ENOTDIR
	}
	entriesBlob := blobref.Parse(dirss.Entries)
	if entriesBlob == nil {
		log.Printf("Blob %s had invalid blobref %q for its 'entries'", dirBlob, dirss.Entries)
		return nil, fuse.ENOTDIR
	}

	entss, err := fs.fetchSchemaSuperset(entriesBlob)
	log.Printf("entries blob: %v, err=%v", entss, err)
	switch {
	case err == os.ENOENT:
		log.Printf("Failed to find entries %s via directory %s", entriesBlob, dirBlob)
		return nil, fuse.ENOENT
	case err == os.EINVAL:
		log.Printf("Failed to parse entries %s via directory %s", entriesBlob, dirBlob)
		return nil, fuse.ENOTDIR
	case err != nil:
		panic(fmt.Sprintf("Invalid fetcher error: %v", err))
	case entss == nil:
		panic("nil entss")
	case entss.Type != "static-set":
		log.Printf("Expected %s to be a directory; actually a %s",
			dirBlob, entss.Type)
		return nil, fuse.ENOTDIR
	}

	retch := make(chan fuse.DirEntry, 20)
	wg := new(sync.WaitGroup)
	for _, m := range entss.Members {
		wg.Add(1)
		go func(memberBlobstr string) {
			defer wg.Done()
			memberBlob := blobref.Parse(memberBlobstr)
			if memberBlob == nil {
				log.Printf("invalid blobref of %q in static set %s", memberBlobstr, entss)
				return
			}
			childss, err := fs.fetchSchemaSuperset(memberBlob)
			if err == nil {
				if childss.FileName != "" {
					mode := childss.UnixMode()
					//log.Printf("adding to dir %s: file=%q, mode=%d", dirBlob, childss.FileName, mode)
					retch <- fuse.DirEntry{Name: childss.FileName, Mode: mode}
				} else {
					log.Printf("Blob %s had no filename", childss.FileName)
				}
			} else {
				log.Printf("Error fetching %s: %v", memberBlobstr, err)
			}
		}(m)
	}
	go func() {
		wg.Wait()
		close(retch)
	}()
	return retch, fuse.OK
}

func (fs *CamliFileSystem) Readlink(name string) (string, fuse.Status) {
	log.Printf("cammount: Readlink(%q)", name)
	// TODO
	return "", fuse.EACCES
}

type CamliFile struct {
	*fuse.DefaultRawFuseFile

	fs   *CamliFileSystem
	blob *blobref.BlobRef
	ss   *schema.Superset
}

func (file *CamliFile) Read(ri *fuse.ReadIn, bp *fuse.BufferPool) (retbuf []byte, retst fuse.Status) {
	offset := ri.Offset
	if offset >= file.ss.Size {
		return []byte(""), fuse.OK // TODO: correct status?
	}
	size := ri.Size // size of read to do (uint32)
	endOffset := offset + uint64(size)
	if endOffset > file.ss.Size {
		size -= uint32(endOffset - file.ss.Size)
		endOffset = file.ss.Size
	}

	buf := bytes.NewBuffer(make([]byte, 0, int(size)))
	fr := file.ss.NewFileReader(file.fs.fetcher)
	fr.Skip(offset)
	lr := io.LimitReader(fr, int64(size))
	_, err := io.Copy(buf, lr) // TODO: care about n bytes copied?
	if err == nil {
		return buf.Bytes(), fuse.OK
	}
	log.Printf("cammount Read error: %v", err)
	retst = fuse.EIO
	return
}
