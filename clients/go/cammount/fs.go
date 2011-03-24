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
	"fmt"
	"log"
	"json"
	"os"
	"path/filepath"
	"sync"

	"camli/blobref"
	"camli/client"
	"camli/schema"
	"camli/third_party/github.com/hanwen/go-fuse/fuse"
)

var _ = fmt.Println
var _ = log.Println

type CamliFileSystem struct {
	fuse.DefaultPathFilesystem

	fetcher blobref.Fetcher
	root   *blobref.BlobRef

	lk         sync.Mutex
	nameToBlob map[string]*blobref.BlobRef
}

func NewCamliFileSystem(client *client.Client, root *blobref.BlobRef) *CamliFileSystem {
	return &CamliFileSystem{
	fetcher: client,
	root: root,
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

func (fs *CamliFileSystem) fetchSchemaSuperset(br *blobref.BlobRef) (*schema.Superset, os.Error) {
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
	return ss, nil
}

// Where name == "" for root,
// Returns fuse.Status == fuse.OK on success or anything else on failure.
func (fs *CamliFileSystem) blobRefFromName(name string) (*blobref.BlobRef, fuse.Status) {
	if name == "" {
		return fs.root, fuse.OK
	}
	if br := fs.blobRefFromNameCached(name); br != nil {
		return br, fuse.OK
	}
	dir, fileName := filepath.Split(name)
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
	foundCh := make(chan *blobref.BlobRef)
	for _, m := range entss.Members {
		wg.Add(1)
		go func(memberBlobstr string) {
			childss, err := fs.fetchSchemaSuperset(entriesBlob)
			if err == nil && childss.HasFilename(fileName) {
				foundCh <- entriesBlob
			}
			wg.Done()
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
	log.Printf("cammount: GetAttr(%q)", name)
	blobref, errStatus := fs.blobRefFromName(name)
	log.Printf("cammount: GetAttr(%q), blobRefFromName err=%v", name, errStatus)
	if errStatus != fuse.OK {
		return nil, errStatus
	}
	log.Printf("cammount: got blob %s", blobref)

	// TODO: this is redundant with what blobRefFromName already
	// did.  we should at least keep this in RAM (pre-de-JSON'd)
	// so we don't have to fetch + unmarshal it again.
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
		fi.Size = ss.Size
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
	log.Printf("cammount: Open(%q, %d)", name, flags)
	// TODO
	return nil, fuse.EACCES
}

func (fs *CamliFileSystem) OpenDir(name string) (stream chan fuse.DirEntry, code fuse.Status) {
	log.Printf("cammount: OpenDir(%q)", name)

	dirBlob, errStatus := fs.blobRefFromName(name)
	log.Printf("cammount: OpenDir(%q), dirBlobFromName err=%v", name, errStatus)
	if errStatus != fuse.OK {
		return nil, errStatus
	}
	log.Printf("cammount: got blob %s", dirBlob)

	// TODO: this is redundant with what blobRefFromName already
	// did.  we should at least keep this in RAM (pre-de-JSON'd)
	// so we don't have to fetch + unmarshal it again.
	ss, err := fs.fetchSchemaSuperset(dirBlob)
	if err != nil {
		log.Printf("cammount: OpenDir(%q, %s): fetch schema error: %v", name, dirBlob, err)
		return nil, fuse.EIO
	}

	if ss.Entries == "" {
		log.Printf("Expected %s to have 'entries'", dirBlob)
		return nil, fuse.ENOTDIR
	}
	entriesBlob := blobref.Parse(ss.Entries)
	if entriesBlob == nil {
		log.Printf("Blob %s had invalid blobref %q for its 'entries'", dirBlob, ss.Entries)
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
			dirBlob, ss.Type)
		return nil, fuse.ENOTDIR
	}

	retch := make(chan fuse.DirEntry, 20)
	wg := new(sync.WaitGroup)
	for _, m := range entss.Members {
		wg.Add(1)
		go func(memberBlobstr string) {
			childss, err := fs.fetchSchemaSuperset(entriesBlob)
			if err == nil {
				retch <- fuse.DirEntry{Name: childss.FileName, Mode: ss.UnixMode()}
			} else {
				log.Printf("Error fetching %s: %v", entriesBlob, err)
			}
			wg.Done()
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
