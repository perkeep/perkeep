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
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/osutil"
)

type statFingerprint string

func fileInfoToFingerprint(fi os.FileInfo) statFingerprint {
	// We calculate the CRC32 of the underlying system stat structure to get
	// ctime, owner, group, etc.  This is overkill (e.g. we don't care about
	// the inode or device number probably), but works.
	sysHash := uint32(0)
	if sys := fi.Sys(); sys != nil {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "%#v", sys)
		sysHash = crc32.ChecksumIEEE(buf.Bytes())
	}
	return statFingerprint(fmt.Sprintf("%dB/%dMOD/sys-%d", fi.Size(), fi.ModTime().UnixNano(), sysHash))
}

type fileInfoPutRes struct {
	Fingerprint statFingerprint
	Result      client.PutResult
}

// FlatStatCache is an ugly hack, until leveldb-go is ready
// (http://code.google.com/p/leveldb-go/)
type FlatStatCache struct {
	mu       sync.RWMutex
	filename string
	m        map[string]fileInfoPutRes
	af       *os.File // for appending
}

func escapeGen(gen string) string {
	// Good enough:
	return url.QueryEscape(gen)
}

func NewFlatStatCache(gen string) *FlatStatCache {
	filename := filepath.Join(osutil.CacheDir(), "camput.statcache." + escapeGen(gen))
	fc := &FlatStatCache{
		filename: filename,
		m:        make(map[string]fileInfoPutRes),
	}

	f, err := os.Open(filename)
	if os.IsNotExist(err) {
		return fc
	}
	if err != nil {
		log.Fatalf("opening camput stat cache: %v", filename, err)
	}
	defer f.Close()
	br := bufio.NewReader(f)
	for {
		ln, err := br.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Warning: (ignoring) reading stat cache: %v", err)
			break
		}
		ln = strings.TrimSpace(ln)
		f := strings.Split(ln, "\t")
		if len(f) < 3 {
			continue
		}
		filename, fp, putres := f[0], statFingerprint(f[1]), f[2]
		f = strings.Split(putres, "/")
		if len(f) != 2 {
			continue
		}
		blobrefStr := f[0]
		blobSize, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			continue
		}

		fc.m[filename] = fileInfoPutRes{
			Fingerprint: fp,
			Result: client.PutResult{
				BlobRef: blobref.Parse(blobrefStr),
				Size:    blobSize,
				Skipped: true, // is this used?
			},
		}
	}
	vlog.Printf("Flatcache read %d entries from %s", len(fc.m), filename)
	return fc
}

var _ UploadCache = (*FlatStatCache)(nil)

var errCacheMiss = errors.New("not in cache")

// cacheKey returns the cleaned absolute path of joining pwd and filename.
func cacheKey(pwd, filename string) string {
	if filepath.IsAbs(filename) {
		return filepath.Clean(filename)
	}
	return filepath.Join(pwd, filename)
}

func (c *FlatStatCache) CachedPutResult(pwd, filename string, fi os.FileInfo) (*client.PutResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	fp := fileInfoToFingerprint(fi)

	key := cacheKey(pwd, filename)
	val, ok := c.m[key]
	if !ok {
		cachelog.Printf("cache MISS on %q: not in cache", key)
		return nil, errCacheMiss
	}
	if val.Fingerprint != fp {
		cachelog.Printf("cache MISS on %q: stats not equal:\n%#v\n%#v", key, val.Fingerprint, fp)
		return nil, errCacheMiss
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

	c.m[key] = val
	if c.af == nil {
		var err error
		c.af, err = os.OpenFile(c.filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			log.Printf("opening stat cache for append: %v", err)
			return
		}
	}
	// TODO: flocking. see leveldb-go.
	c.af.Seek(0, os.SEEK_END)
	c.af.Write([]byte(fmt.Sprintf("%s\t%s\t%s/%d\n", key, val.Fingerprint, val.Result.BlobRef.String(), val.Result.Size)))
}

type FlatHaveCache struct {
	mu       sync.RWMutex
	filename string
	m        map[string]bool
	af       *os.File // appending file
}

func NewFlatHaveCache(gen string) *FlatHaveCache {
	filename := filepath.Join(osutil.CacheDir(), "camput.havecache." + escapeGen(gen))
	c := &FlatHaveCache{
		filename: filename,
		m:        make(map[string]bool),
	}
	f, err := os.Open(filename)
	if os.IsNotExist(err) {
		return c
	}
	if err != nil {
		log.Fatalf("opening camput have-cache: %v", filename, err)
	}
	br := bufio.NewReader(f)
	for {
		ln, err := br.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Warning: (ignoring) reading have-cache: %v", err)
			break
		}
		ln = strings.TrimSpace(ln)
		c.m[ln] = true
	}
	return c
}

func (c *FlatHaveCache) BlobExists(br *blobref.BlobRef) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.m[br.String()]
}

func (c *FlatHaveCache) NoteBlobExists(br *blobref.BlobRef) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := br.String()
	c.m[k] = true

	if c.af == nil {
		var err error
		c.af, err = os.OpenFile(c.filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			log.Printf("opening have-cache for append: %v", err)
			return
		}
	}
	// TODO: flocking. see leveldb-go.
	c.af.Seek(0, os.SEEK_END)
	c.af.Write([]byte(fmt.Sprintf("%s\n", k)))
}
