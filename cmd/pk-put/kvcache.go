/*
Copyright 2013 The Perkeep Authors.

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
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

var errCacheMiss = errors.New("not in cache")

// KvHaveCache is a HaveCache on top of a single
// mutable database file on disk using github.com/syndtr/goleveldb.
// It stores the blobref in binary as the key, and
// the blobsize in binary as the value.
// Access to the cache is restricted to one process
// at a time with a lock file. Close should be called
// to remove the lock.
type KvHaveCache struct {
	filename string
	db       *leveldb.DB
}

func NewKvHaveCache(gen string) *KvHaveCache {
	cleanCacheDir()
	fullPath := filepath.Join(osutil.CacheDir(), "pk-put.havecache."+escapeGen(gen)+".leveldb")
	db, err := leveldb.OpenFile(fullPath, nil)
	if err != nil {
		log.Fatalf("Could not create/open new have cache at %v, %v", fullPath, err)
	}

	if err := maybeRunCompaction("HaveCache", db); err != nil {
		log.Fatal(err)
	}

	return &KvHaveCache{
		filename: fullPath,
		db:       db,
	}
}

// maybeRunCompaction forces compaction of db, if the number of
// tables in level 0 is >= 4. dbname should be provided for error messages.
func maybeRunCompaction(dbname string, db *leveldb.DB) error {
	val, err := db.GetProperty("leveldb.num-files-at-level0")
	if err != nil {
		return fmt.Errorf("could not get number of level-0 files of %v's LevelDB: %v", dbname, err)
	}
	nbFiles, err := strconv.Atoi(val)
	if err != nil {
		return fmt.Errorf("could not convert number of level-0 files to int: %v", err)
	}
	// Only force compaction if we're at the default trigger (4), see
	// github.com/syndtr/goleveldb/leveldb/opt.DefaultCompactionL0Trigger
	if nbFiles < 4 {
		return nil
	}
	if err := db.CompactRange(util.Range{Start: nil, Limit: nil}); err != nil {
		return fmt.Errorf("could not run compaction on %v's LevelDB: %v", dbname, err)
	}
	return nil
}

// Close should be called to commit all the writes
// to the db and to unlock the file.
func (c *KvHaveCache) Close() error {
	return c.db.Close()
}

func (c *KvHaveCache) StatBlobCache(br blob.Ref) (size uint32, ok bool) {
	if !br.Valid() {
		return
	}
	binBr, _ := br.MarshalBinary()
	binVal, err := c.db.Get(binBr, nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			cachelog.Printf("have cache MISS on %v", br)
			return
		}
		log.Fatalf("Could not query have cache %v for %v: %v", c.filename, br, err)
	}
	val, err := strconv.ParseUint(string(binVal), 10, 32)
	if err != nil {
		log.Fatalf("Could not decode have cache binary value for %v: %v", br, err)
	}
	cachelog.Printf("have cache HIT on %v", br)
	return uint32(val), true
}

func (c *KvHaveCache) NoteBlobExists(br blob.Ref, size uint32) {
	if !br.Valid() {
		return
	}
	binBr, _ := br.MarshalBinary()
	binVal := fmt.Appendf(nil, "%d", size)
	cachelog.Printf("Adding to have cache %v: %q", br, binVal)
	if err := c.db.Put(binBr, binVal, nil); err != nil {
		log.Fatalf("Could not write %v in have cache: %v", br, err)
	}
}

// KvStatCache is an UploadCache on top of a single
// mutable database file on disk using github.com/syndtr/goleveldb.
// It stores a binary combination of an os.FileInfo fingerprint and
// a client.Putresult as the key, and the blobsize in binary as
// the value.
// Access to the cache is restricted to one process
// at a time with a lock file. Close should be called
// to remove the lock.
type KvStatCache struct {
	filename string
	db       *leveldb.DB
}

func NewKvStatCache(gen string) *KvStatCache {
	fullPath := filepath.Join(osutil.CacheDir(), "pk-put.statcache."+escapeGen(gen)+".leveldb")
	db, err := leveldb.OpenFile(fullPath, nil)
	if err != nil {
		log.Fatalf("Could not create/open new stat cache at %v, %v", fullPath, err)
	}

	if err := maybeRunCompaction("StatCache", db); err != nil {
		log.Fatal(err)
	}

	return &KvStatCache{
		filename: fullPath,
		db:       db,
	}
}

// Close should be called to commit all the writes
// to the db and to unlock the file.
func (c *KvStatCache) Close() error {
	return c.db.Close()
}

func (c *KvStatCache) CachedPutResult(pwd, filename string, fi os.FileInfo, withPermanode bool) (*client.PutResult, error) {
	fullPath := fullpath(pwd, filename)
	cacheKey := &statCacheKey{
		Filepath:  fullPath,
		Permanode: withPermanode,
	}
	binKey, err := cacheKey.marshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshaling cache key: %w", err)
	}
	binVal, err := c.db.Get(binKey, nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			cachelog.Printf("stat cache MISS on %q", binKey)
			return nil, errCacheMiss
		}
		log.Fatalf("Could not query stat cache %q for %v: %v", binKey, fullPath, err)
	}
	val := &statCacheValue{}
	if err = val.unmarshalBinary(binVal); err != nil {
		return nil, fmt.Errorf("Bogus stat cached value for %q: %v", binKey, err)
	}
	fp := fileInfoToFingerprint(fi)
	if val.Fingerprint != fp {
		cachelog.Printf("cache MISS on %q: stats not equal:\n%#v\n%#v", binKey, val.Fingerprint, fp)
		return nil, errCacheMiss
	}
	cachelog.Printf("stat cache HIT on %q", binKey)
	return &val.Result, nil
}

func (c *KvStatCache) AddCachedPutResult(pwd, filename string, fi os.FileInfo, pr *client.PutResult, withPermanode bool) {
	fullPath := fullpath(pwd, filename)
	cacheKey := &statCacheKey{
		Filepath:  fullPath,
		Permanode: withPermanode,
	}
	val := &statCacheValue{fileInfoToFingerprint(fi), *pr}

	binKey, err := cacheKey.marshalBinary()
	if err != nil {
		log.Fatalf("Could not add %q to stat cache: %v", binKey, err)
	}
	binVal, err := val.marshalBinary()
	if err != nil {
		log.Fatalf("Could not add %q to stat cache: %v", binKey, err)
	}
	cachelog.Printf("Adding to stat cache %q: %q", binKey, binVal)
	if err := c.db.Put(binKey, binVal, nil); err != nil {
		log.Fatalf("Could not add %q to stat cache: %v", binKey, err)
	}
}

type statCacheKey struct {
	Filepath  string
	Permanode bool // whether -filenodes is being used.
}

// marshalBinary returns a more compact binary
// representation of the contents of sk.
func (sk *statCacheKey) marshalBinary() ([]byte, error) {
	if sk == nil {
		return nil, errors.New("Can not marshal from a nil stat cache key")
	}
	data := make([]byte, 0, len(sk.Filepath)+3)
	data = append(data, 1) // version number
	data = append(data, sk.Filepath...)
	data = append(data, '|')
	if sk.Permanode {
		data = append(data, 1)
	}
	return data, nil
}

type statFingerprint string

type statCacheValue struct {
	Fingerprint statFingerprint
	Result      client.PutResult
}

// marshalBinary returns a more compact binary
// representation of the contents of scv.
func (scv *statCacheValue) marshalBinary() ([]byte, error) {
	if scv == nil {
		return nil, errors.New("Can not marshal from a nil stat cache value")
	}
	binBr, _ := scv.Result.BlobRef.MarshalBinary()
	// Blob size fits on 4 bytes when binary encoded
	data := make([]byte, 0, len(scv.Fingerprint)+1+4+1+len(binBr))
	buf := bytes.NewBuffer(data)
	_, err := buf.WriteString(string(scv.Fingerprint))
	if err != nil {
		return nil, fmt.Errorf("Could not write fingerprint %v: %v", scv.Fingerprint, err)
	}
	err = buf.WriteByte('|')
	if err != nil {
		return nil, fmt.Errorf("Could not write '|': %v", err)
	}
	err = binary.Write(buf, binary.BigEndian, int32(scv.Result.Size))
	if err != nil {
		return nil, fmt.Errorf("Could not write blob size %d: %v", scv.Result.Size, err)
	}
	err = buf.WriteByte('|')
	if err != nil {
		return nil, fmt.Errorf("Could not write '|': %v", err)
	}
	_, err = buf.Write(binBr)
	if err != nil {
		return nil, fmt.Errorf("Could not write binary blobref %q: %v", binBr, err)
	}
	return buf.Bytes(), nil
}

var pipe = []byte("|")

func (scv *statCacheValue) unmarshalBinary(data []byte) error {
	if scv == nil {
		return errors.New("Can't unmarshalBinary into a nil stat cache value")
	}
	if scv.Fingerprint != "" {
		return errors.New("Can't unmarshalBinary into a non empty stat cache value")
	}

	parts := bytes.SplitN(data, pipe, 3)
	if len(parts) != 3 {
		return fmt.Errorf("Bogus stat cache value; was expecting fingerprint|blobSize|blobRef, got %q", data)
	}
	fingerprint := string(parts[0])
	buf := bytes.NewReader(parts[1])
	var size int32
	err := binary.Read(buf, binary.BigEndian, &size)
	if err != nil {
		return fmt.Errorf("Could not decode blob size from stat cache value part %q: %v", parts[1], err)
	}
	br := new(blob.Ref)
	if err := br.UnmarshalBinary(parts[2]); err != nil {
		return fmt.Errorf("Could not unmarshalBinary for %q: %v", parts[2], err)
	}

	scv.Fingerprint = statFingerprint(fingerprint)
	scv.Result = client.PutResult{
		BlobRef: *br,
		Size:    uint32(size),
		Skipped: true,
	}
	return nil
}

func fullpath(pwd, filename string) string {
	var fullPath string
	if filepath.IsAbs(filename) {
		fullPath = filepath.Clean(filename)
	} else {
		fullPath = filepath.Join(pwd, filename)
	}
	return fullPath
}

func escapeGen(gen string) string {
	// Good enough:
	return url.QueryEscape(gen)
}

var cleanSysStat func(v any) any

func fileInfoToFingerprint(fi os.FileInfo) statFingerprint {
	// We calculate the CRC32 of the underlying system stat structure to get
	// ctime, owner, group, etc.  This is overkill (e.g. we don't care about
	// the inode or device number probably), but works.
	sysHash := uint32(0)
	if sys := fi.Sys(); sys != nil {
		if clean := cleanSysStat; clean != nil {
			// TODO: don't clean bad fields, but provide a
			// portable way to extract all good fields.
			sys = clean(sys)
		}
		c32 := crc32.NewIEEE()
		fmt.Fprintf(c32, "%#v", sys)
		sysHash = c32.Sum32()
	}
	return statFingerprint(fmt.Sprintf("%dB/%dMOD/sys-%d", fi.Size(), fi.ModTime().UnixNano(), sysHash))
}

// Delete all but the oldest 5 havecache/statcache dirs, unless they're newer
// than 30 days.
func cleanCacheDir() {
	dir := osutil.CacheDir()
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	defer f.Close()
	fis, err := f.Readdir(-1)
	if err != nil {
		return
	}
	var haveCache, statCache []os.FileInfo
	seen := make(map[string]bool)
	for _, fi := range fis {
		seen[fi.Name()] = true
	}

	for _, fi := range fis {
		if strings.HasPrefix(fi.Name(), "pk-put.havecache.") {
			haveCache = append(haveCache, fi)
			continue
		}
		if strings.HasPrefix(fi.Name(), "pk-put.statcache.") {
			statCache = append(statCache, fi)
			continue
		}
	}
	for _, list := range [][]os.FileInfo{haveCache, statCache} {
		if len(list) <= 5 {
			continue
		}
		sort.Sort(byModtime(list))
		list = list[:len(list)-5]
		for _, fi := range list {
			if fi.ModTime().Before(time.Now().Add(-30 * 24 * time.Hour)) {
				os.RemoveAll(filepath.Join(dir, fi.Name()))
			}
		}
	}
}

type byModtime []os.FileInfo

func (s byModtime) Len() int           { return len(s) }
func (s byModtime) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s byModtime) Less(i, j int) bool { return s[i].ModTime().Before(s[j].ModTime()) }
