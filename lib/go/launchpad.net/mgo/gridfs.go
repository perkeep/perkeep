// mgo - MongoDB driver for Go
// 
// Copyright (c) 2010-2011 - Gustavo Niemeyer <gustavo@niemeyer.net>
// 
// All rights reserved.
// 
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
// 
//     * Redistributions of source code must retain the above copyright notice,
//       this list of conditions and the following disclaimer.
//     * Redistributions in binary form must reproduce the above copyright notice,
//       this list of conditions and the following disclaimer in the documentation
//       and/or other materials provided with the distribution.
//     * Neither the name of the copyright holder nor the names of its
//       contributors may be used to endorse or promote products derived from
//       this software without specific prior written permission.
// 
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR
// CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
// EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
// PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
// PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
// LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
// NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
// SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

package mgo

import (
	"crypto/md5"
	"encoding/hex"
	"hash"
	"launchpad.net/gobson/bson"
	"os"
	"sync"
)

type GridFS struct {
	Files  Collection
	Chunks Collection
}

type gfsFileMode int

const (
	gfsClosed  gfsFileMode = 0
	gfsReading gfsFileMode = 1
	gfsWriting gfsFileMode = 2
)

type GridFile struct {
	m    sync.Mutex
	c    sync.Cond
	gfs  GridFS
	mode gfsFileMode
	err  os.Error

	chunk  int
	offset int64

	wpending int
	wbuf     []byte
	wsum     hash.Hash

	rbuf   []byte
	rcache *gfsCachedChunk

	doc gfsFile
}

type gfsFile struct {
	Id          interface{}    "_id"
	ChunkSize   int            "chunkSize"
	UploadDate  bson.Timestamp "uploadDate"
	Length      int64          ",minsize"
	MD5         string
	Filename    string    ",omitempty"
	ContentType string    "contentType,omitempty"
	Metadata    *bson.Raw ",omitempty"
}

type gfsChunk struct {
	Id      interface{} "_id"
	FilesId interface{} "files_id"
	N       int
	Data    []byte
}

type gfsCachedChunk struct {
	wait sync.Mutex
	n    int
	data []byte
	err  os.Error
}

func newGridFS(db Database, prefix string) *GridFS {
	return &GridFS{db.C(prefix + ".files"), db.C(prefix + ".chunks")}
}

func (gfs GridFS) newFile() *GridFile {
	file := &GridFile{gfs: gfs}
	file.c.L = &file.m
	//runtime.SetFinalizer(file, finalizeFile)
	return file
}

func finalizeFile(file *GridFile) {
	file.Close()
}

// Create creates a new file with the provided name in the GridFS.  If the file
// name already exists, a new version will be inserted with an up-to-date
// uploadDate that will cause it to be atomically visible to the Open and
// OpenId methods.  If the file name is not important, an empty name may be
// provided and the file Id used instead.
//
// It's important to Close files whether they are being written to
// or read from, and to check the err result to ensure the operation
// completed successfully.
//
// A simple example inserting a new file:
//
//     func check(err os.Error) {
//         if err != nil {
//             panic(err.String())
//         }
//     }
//     file, err := db.GridFS("fs").Create("myfile.txt")
//     check(err)
//     n, err := file.Write([]byte("Hello world!")
//     check(err)
//     err = file.Close()
//     check(err)
//     fmt.Printf("%d bytes written\n", n)
//
// The io.Writer interface is implemented by *GridFile and may be used to
// help on the file creation.  For example:
//
//     file, err := db.GridFS("fs").Create("myfile.txt")
//     check(err)
//     messages, err := os.Open("/var/log/messages")
//     check(err)
//     defer messages.Close()
//     err = io.Copy(file, messages)
//     check(err)
//     err = file.Close()
//     check(err)
//
func (gfs GridFS) Create(name string) (file *GridFile, err os.Error) {
	file = gfs.newFile()
	file.mode = gfsWriting
	file.wsum = md5.New()
	file.doc = gfsFile{Id: bson.NewObjectId(), ChunkSize: 256 * 1024, Filename: name}
	return
}

// OpenId returns a file with the provided id in case it exists or an error
// instead.  If the file isn't found, err will be set to mgo.NotFound.
//
// It's important to Close files whether they are being written to
// or read from, and to check the err result to ensure the operation
// completed successfully.
//
// The following example will print the first 8192 bytes from the file:
// 
//     func check(err os.Error) {
//         if err != nil {
//             panic(err.String())
//         }
//     }
//     file, err := db.GridFS("fs").OpenId(objid)
//     check(err)
//     b := make([]byte, 8192)
//     n, err := file.Read(b)
//     check(err)
//     fmt.Println(string(b))
//     check(err)
//     err = file.Close()
//     check(err)
//     fmt.Printf("%d bytes read\n", n)
//
// The io.Reader interface is implemented by *GridFile and may be used to
// deal with it.  As an example, the following snippet will dump the whole
// file into the standard output:
//
//     file, err := db.GridFS("fs").OpenId(objid)
//     check(err)
//     err = io.Copy(os.Stdout, file)
//     check(err)
//     err = file.Close()
//     check(err)
//
func (gfs GridFS) OpenId(id interface{}) (file *GridFile, err os.Error) {
	var doc gfsFile
	err = gfs.Files.Find(bson.M{"_id": id}).One(&doc)
	if err != nil {
		return
	}
	file = gfs.newFile()
	file.mode = gfsReading
	file.doc = doc
	return
}

// Open returns the most recent uploaded file with the provided name, or an
// error instead.  If the file isn't found, err will be set to mgo.NotFound.
//
// It's important to Close files whether they are being written to
// or read from, and to check the err result to ensure the operation
// completed successfully.
//
// The following example will print the first 8192 bytes from the file:
// 
//     func check(err os.Error) {
//         if err != nil {
//             panic(err.String())
//         }
//     }
//     file, err := db.GridFS("fs").Open("myfile.txt")
//     check(err)
//     b := make([]byte, 8192)
//     n, err := file.Read(b)
//     check(err)
//     fmt.Println(string(b))
//     check(err)
//     err = file.Close()
//     check(err)
//     fmt.Printf("%d bytes read\n", n)
//
// The io.Reader interface is implemented by *GridFile and may be used to
// deal with it.  As an example, the following snippet will dump the whole
// file into the standard output:
//
//     file, err := db.GridFS("fs").Open("myfile.txt")
//     check(err)
//     err = io.Copy(os.Stdout, file)
//     check(err)
//     err = file.Close()
//     check(err)
//
func (gfs GridFS) Open(name string) (file *GridFile, err os.Error) {
	var doc gfsFile
	err = gfs.Files.Find(bson.M{"filename": name}).Sort(bson.M{"uploadDate": -1}).One(&doc)
	if err != nil {
		return
	}
	file = gfs.newFile()
	file.mode = gfsReading
	file.doc = doc
	return
}

// RemoveId deletes the file with the provided id from the GridFS.
func (gfs GridFS) RemoveId(id interface{}) os.Error {
	err := gfs.Files.Remove(bson.M{"_id": id})
	if err != nil {
		return err
	}
	return gfs.Chunks.RemoveAll(bson.M{"files_id": id})
}

type gfsDocId struct {
	Id interface{} "_id"
}

// Remove deletes all files with the provided name from the GridFS.
func (gfs GridFS) Remove(name string) (err os.Error) {
	iter := gfs.Files.Find(bson.M{"filename": name}).Select(bson.M{"_id": 1}).Iter()
	var doc gfsDocId
	for iter.Next(&doc) {
		if e := gfs.RemoveId(doc.Id); e != nil {
			err = e
		}
	}
	if err == nil {
		err = iter.Err()
	}
	return err
}

func (file *GridFile) assertMode(mode gfsFileMode) {
	switch file.mode {
	case mode:
		return
	case gfsWriting:
		panic("GridFile is open for writing")
	case gfsReading:
		panic("GridFile is open for reading")
	case gfsClosed:
		panic("GridFile is closed")
	default:
		panic("Internal error: missing GridFile mode")
	}
}

// SetChunkSize sets size of saved chunks.  Once the file is written to, it
// will be split in blocks of that size and each block saved into an
// independent chunk document.  The default chunk size is 256kb.
//
// It is a runtime error to call this function once the file has started
// being written to.
func (file *GridFile) SetChunkSize(bytes int) {
	file.assertMode(gfsWriting)
	debugf("GridFile %p: setting chunk size to %d", file, bytes)
	file.m.Lock()
	file.doc.ChunkSize = bytes
	file.m.Unlock()
}

// Id returns the current file Id.
func (file *GridFile) Id() interface{} {
	return file.doc.Id
}

// SetId changes the current file Id.  It is a runtime
//
// It is a runtime error to call this function once the file has started
// being written to, or when the file is not open for writing.
func (file *GridFile) SetId(id interface{}) {
	file.assertMode(gfsWriting)
	file.m.Lock()
	file.doc.Id = id
	file.m.Unlock()
}

// Name returns the optional file name.  An empty string will be returned
// in case it is unset.
func (file *GridFile) Name() string {
	return file.doc.Filename
}

// SetName changes the optional file name.  An empty string may be used to
// unset it.
//
// It is a runtime error to call this function when the file is not open
// for writing.
func (file *GridFile) SetName(name string) {
	file.assertMode(gfsWriting)
	file.m.Lock()
	file.doc.Filename = name
	file.m.Unlock()
}

// ContentType returns the optional file content type.  An empty string will be
// returned in case it is unset.
func (file *GridFile) ContentType() string {
	return file.doc.ContentType
}

// ContentType changes the optional file content type.  An empty string may be
// used to unset it.
//
// It is a runtime error to call this function when the file is not open
// for writing.
func (file *GridFile) SetContentType(ctype string) {
	file.assertMode(gfsWriting)
	file.m.Lock()
	file.doc.ContentType = ctype
	file.m.Unlock()
}

// GetInfo unmarshals the optional metadata associated with the file
// into the result parameter.  For example:
//
//     result := struct{ INode int }{}
//     err = file.GetInfo(&result)
//     if err != nil {
//         panic(err.String())
//     }
//     fmt.Printf("inode: %d\n", result.INode)
//
func (file *GridFile) GetInfo(result interface{}) (err os.Error) {
	file.m.Lock()
	if file.doc.Metadata != nil {
		err = bson.Unmarshal(file.doc.Metadata.Data, result)
	}
	file.m.Unlock()
	return
}

// SetInfo changes the optional metadata associated with the file.
// For example:
//
//     file.SetInfo(bson.M{"inode": inode})
//
// It is a runtime error to call this function when the file is not open
// for writing.
func (file *GridFile) SetInfo(metadata interface{}) {
	file.assertMode(gfsWriting)
	data, err := bson.Marshal(metadata)
	file.m.Lock()
	if err != nil && file.err == nil {
		file.err = err
	} else {
		file.doc.Metadata = &bson.Raw{Data: data}
	}
	file.m.Unlock()
}

// Size returns the file size in bytes.
func (file *GridFile) Size() (bytes int64) {
	file.m.Lock()
	bytes = file.doc.Length
	file.m.Unlock()
	return
}

// MD5 returns the file MD5 as a hex-encoded string.
func (file *GridFile) MD5() (md5 string) {
	return file.doc.MD5
}

// UploadDate returns the file upload time in nanoseconds.
func (file *GridFile) UploadDate() int64 {
	return int64(file.doc.UploadDate)
}

// Close flushes any pending changes in case the file is being written
// to, waits for any background operations to finish, and closes the file.
//
// It's important to Close files whether they are being written to
// or read from, and to check the err result to ensure the operation
// completed successfully.
func (file *GridFile) Close() (err os.Error) {
	file.m.Lock()
	defer file.m.Unlock()
	if file.mode == gfsWriting {
		if len(file.wbuf) > 0 {
			file.insertChunk(file.wbuf)
			file.wbuf = file.wbuf[0:0]
		}
		file.insertFile()
	} else if file.mode == gfsReading && file.rcache != nil {
		file.rcache.wait.Lock()
		file.rcache = nil
	}
	file.mode = gfsClosed
	debugf("GridFile %p: closed", file)
	return file.err
}

// Write writes the provided data to the file and returns the
// number of bytes written and an error in case something
// wrong happened.
//
// The file will internally cache the data so that all but the last
// chunk sent to the database have the size defined by SetChunkSize.
//
// The parameters and behavior of this function turn the file
// into an io.Writer.
func (file *GridFile) Write(data []byte) (n int, err os.Error) {
	file.assertMode(gfsWriting)
	file.m.Lock()
	debugf("GridFile %p: writing %d bytes", file, len(data))
	defer file.m.Unlock()

	if file.err != nil {
		return 0, file.err
	}

	n = len(data)
	file.doc.Length += int64(n)
	chunkSize := file.doc.ChunkSize

	if len(file.wbuf)+len(data) < chunkSize {
		file.wbuf = append(file.wbuf, data...)
		return
	}

	// First, flush file.wbuf complementing with data.
	if len(file.wbuf) > 0 {
		missing := chunkSize - len(file.wbuf)
		if missing > len(data) {
			missing = len(data)
		}
		file.wbuf = append(file.wbuf, data[:missing]...)
		data = data[missing:]
		file.insertChunk(file.wbuf)
		file.wbuf = file.wbuf[0:0]
	}

	// Then, flush all chunks from data without copying.
	for len(data) > chunkSize {
		size := chunkSize
		if size > len(data) {
			size = len(data)
		}
		file.insertChunk(data[:size])
		data = data[size:]
	}

	// And append the rest for a future call.
	file.wbuf = append(file.wbuf, data...)

	return n, file.err
}

func (file *GridFile) insertChunk(data []byte) {
	n := file.chunk
	file.chunk++
	debugf("GridFile %p: adding to checksum: %q", file, string(data))
	file.wsum.Write(data)

	for file.doc.ChunkSize*file.wpending >= 1024*1024 {
		// Hold on.. we got a MB pending.
		file.c.Wait()
		if file.err != nil {
			return
		}
	}

	file.wpending++

	debugf("GridFile %p: inserting chunk %d with %d bytes", file, n, len(data))

	// We may not own the memory of data, so rather than
	// simply copying it, we'll marshal the document ahead of time.
	data, err := bson.Marshal(gfsChunk{bson.NewObjectId(), file.doc.Id, n, data})
	if err != nil {
		file.err = err
		return
	}

	go func() {
		err := file.gfs.Chunks.Insert(bson.Raw{Data: data})
		file.m.Lock()
		file.wpending--
		if err != nil && file.err == nil {
			file.err = err
		}
		file.c.Broadcast()
		file.m.Unlock()
	}()
}

func (file *GridFile) insertFile() {
	hexsum := hex.EncodeToString(file.wsum.Sum())
	for file.wpending > 0 {
		debugf("GridFile %p: waiting for %d pending chunks to insert file", file, file.wpending)
		file.c.Wait()
	}
	if file.err == nil {
		file.doc.UploadDate = bson.Now()
		file.doc.MD5 = hexsum
		file.err = file.gfs.Files.Insert(file.doc)
		file.gfs.Chunks.EnsureIndexKey([]string{"files_id", "n"})
	}
}

// Seek sets the offset for the next Read or Write on file to
// offset, interpreted according to whence: 0 means relative to
// the origin of the file, 1 means relative to the current offset,
// and 2 means relative to the end. It returns the new offset and
// an Error, if any.
func (file *GridFile) Seek(offset int64, whence int) (pos int64, err os.Error) {
	file.m.Lock()
	debugf("GridFile %p: seeking for %s (whence=%d)", file, offset, whence)
	defer file.m.Unlock()
	switch whence {
	case os.SEEK_SET:
	case os.SEEK_CUR:
		offset += file.offset
	case os.SEEK_END:
		offset += file.doc.Length
	default:
		panic("Unsupported whence value")
	}
	if offset > file.doc.Length {
		return file.offset, os.NewError("Seek past end of file")
	}
	chunk := int(offset / int64(file.doc.ChunkSize))
	if chunk+1 == file.chunk && offset >= file.offset {
		file.rbuf = file.rbuf[int(offset-file.offset):]
		file.offset = offset
		return file.offset, nil
	}
	file.offset = offset
	file.chunk = chunk
	file.rbuf = nil
	file.rbuf, err = file.getChunk()
	if err == nil {
		file.rbuf = file.rbuf[int(file.offset-int64(chunk)*int64(file.doc.ChunkSize)):]
	}
	return file.offset, err
}

// Read reads into b the next available data from the file and
// returns the number of bytes written and an error in case
// something wrong happened.  At the end of the file, n will
// be zero and err will be set to os.EOF.
//
// The parameters and behavior of this function turn the file
// into an io.Reader.
func (file *GridFile) Read(b []byte) (n int, err os.Error) {
	file.assertMode(gfsReading)
	file.m.Lock()
	debugf("GridFile %p: reading at offset %d into buffer of length %d", file, file.offset, len(b))
	defer file.m.Unlock()
	if file.offset == file.doc.Length {
		return 0, os.EOF
	}
	for err == nil {
		i := copy(b, file.rbuf)
		n += i
		file.offset += int64(i)
		file.rbuf = file.rbuf[i:]
		if i == len(b) || file.offset == file.doc.Length {
			break
		}
		b = b[i:]
		file.rbuf, err = file.getChunk()
	}
	return n, err
}

func (file *GridFile) getChunk() (data []byte, err os.Error) {
	cache := file.rcache
	file.rcache = nil
	if cache != nil && cache.n == file.chunk {
		debugf("GridFile %p: Getting chunk %d from cache", file, file.chunk)
		cache.wait.Lock()
		data, err = cache.data, cache.err
	} else {
		debugf("GridFile %p: Fetching chunk %d", file, file.chunk)
		var doc gfsChunk
		err = file.gfs.Chunks.Find(bson.D{{"files_id", file.doc.Id}, {"n", file.chunk}}).One(&doc)
		data = doc.Data
	}
	file.chunk++
	if int64(file.chunk)*int64(file.doc.ChunkSize) < file.doc.Length {
		// Read the next one in background.
		cache = &gfsCachedChunk{n: file.chunk}
		cache.wait.Lock()
		debugf("GridFile %p: Scheduling chunk %d for background caching", file, file.chunk)
		chunks := file.gfs.Chunks
		// Clone the session to avoid having it closed in between.
		chunks.DB.Session = chunks.DB.Session.Clone()
		go func(id interface{}, n int) {
			defer chunks.DB.Session.Close()
			var doc gfsChunk
			cache.err = chunks.Find(bson.D{{"files_id", id}, {"n", n}}).One(&doc)
			cache.data = doc.Data
			cache.wait.Unlock()
		}(file.doc.Id, file.chunk)
		file.rcache = cache
	}
	debugf("Returning err: %#v", err)
	return
}
