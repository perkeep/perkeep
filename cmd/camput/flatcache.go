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
	"encoding/gob"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/osutil"
)

type statFingerprint struct {
	Size    int64
	ModTime time.Time
	// TODO: add ctime, etc
}

func fileInfoToFingerprint(fi os.FileInfo) (f statFingerprint) {
	f.Size = fi.Size()
	f.ModTime = fi.ModTime()
	return
}

type fileInfoPutRes struct {
	Fingerprint statFingerprint
	Result      client.PutResult
}

// FlatStatCache is an ugly hack, until leveldb-go is ready
// (http://code.google.com/p/leveldb-go/)
type FlatStatCache struct {
	mu       sync.Mutex
	filename string
	m        map[string]fileInfoPutRes
	dirty    map[string]fileInfoPutRes
}

func NewFlatStatCache() *FlatStatCache {
	filename := filepath.Join(osutil.CacheDir(), "camput.statcache")
	fc := &FlatStatCache{
		filename: filename,
		m:        make(map[string]fileInfoPutRes),
		dirty:    make(map[string]fileInfoPutRes),
	}

	if f, err := os.Open(filename); err == nil {
		defer f.Close()
		d := gob.NewDecoder(f)
		for {
			var (
				key string
				val fileInfoPutRes
				err error
			)
			err = d.Decode(&key)
			if err == io.EOF {
				break
			}
			if err != nil {
				log.Fatalf("stat cache key decode error: %v", err)
			}
			err = d.Decode(&val)
			if err != nil {
				log.Fatalf("stat cache value decode error: %v", err)
			}
			val.Result.Skipped = true
			fc.m[key] = val
		}
		vlog.Printf("Flatcache read %d entries from %s", len(fc.m), filename)
	}
	return fc
}

var _ UploadCache = (*FlatStatCache)(nil)

var ErrCacheMiss = errors.New("not in cache")

// filename may be relative.
// returns ErrCacheMiss on miss
func cacheKey(pwd, filename string) string {
	if filepath.IsAbs(filename) {
		return filepath.Clean(filename)
	}
	return filepath.Join(pwd, filename)
}

func (c *FlatStatCache) CachedPutResult(pwd, filename string, fi os.FileInfo) (*client.PutResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	fp := fileInfoToFingerprint(fi)

	key := cacheKey(pwd, filename)
	val, ok := c.m[key]
	if !ok {
		cachelog.Printf("cache MISS on %q: not in cache", key)
		return nil, ErrCacheMiss
	}
	if !reflect.DeepEqual(val.Fingerprint, fp) {
		cachelog.Printf("cache MISS on %q: stats not equal:\n%#v\n%#v", key, val.Fingerprint, fp)
		return nil, ErrCacheMiss
	}
	pr := val.Result
	return &pr, nil
}

func (c *FlatStatCache) AddCachedPutResult(pwd, filename string, fi os.FileInfo, pr *client.PutResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := cacheKey(pwd, filename)
	val := fileInfoPutRes{fileInfoToFingerprint(fi), *pr}

	cachelog.Printf("Adding to stat cache %q: %v", key, val)

	c.dirty[key] = val
	c.m[key] = val
}

func (c *FlatStatCache) Save() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.dirty) == 0 {
		cachelog.Printf("FlatStatCache: Save, but nothing dirty")
		return
	}

	f, err := os.OpenFile(c.filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		log.Fatalf("FlatStatCache OpenFile: %v", err)
	}
	defer f.Close()
	e := gob.NewEncoder(f)
	write := func(v interface{}) {
		if err := e.Encode(v); err != nil {
			panic("Encode: " + err.Error())
		}
	}
	for k, v := range c.dirty {
		write(k)
		write(v)
	}
	c.dirty = make(map[string]fileInfoPutRes)
	cachelog.Printf("FlatStatCache: saved")
}

type FlatHaveCache struct {
	mu       sync.Mutex
	filename string
	m        map[string]bool
	dirty    map[string]bool
}

func NewFlatHaveCache() *FlatHaveCache {
	filename := filepath.Join(osutil.CacheDir(), "camput.havecache")
	c := &FlatHaveCache{
		filename: filename,
		m:        make(map[string]bool),
		dirty:    make(map[string]bool),
	}
	if f, err := os.Open(filename); err == nil {
		defer f.Close()
		d := gob.NewDecoder(f)
		for {
			var key string
			if d.Decode(&key) != nil {
				break
			}
			c.m[key] = true
		}
	}
	return c
}

func (c *FlatHaveCache) BlobExists(br *blobref.BlobRef) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[br.String()]
}

func (c *FlatHaveCache) NoteBlobExists(br *blobref.BlobRef) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := br.String()
	c.m[k] = true
	c.dirty[k] = true
}

func (c *FlatHaveCache) Save() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.dirty) == 0 {
		cachelog.Printf("FlatHaveCache: Save, but nothing dirty")
		return
	}
	os.Mkdir(osutil.CacheDir(), 0700)
	f, err := os.OpenFile(c.filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		log.Fatalf("FlatHaveCache OpenFile: %v", err)
	}
	defer f.Close()
	e := gob.NewEncoder(f)
	write := func(v interface{}) {
		if err := e.Encode(v); err != nil {
			panic("Encode: " + err.Error())
		}
	}
	for k, _ := range c.dirty {
		write(k)
	}
	c.dirty = make(map[string]bool)
	cachelog.Printf("FlatHaveCache: saved")
}
