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

// Package cacher provides various blobref fetching caching mechanisms.
package cacher

import (
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/localdisk"
	"camlistore.org/pkg/osutil"

	"go4.org/syncutil/singleflight"
)

// NewCachingFetcher returns a CachingFetcher that fetches from
// fetcher and writes to and serves from cache.
func NewCachingFetcher(cache blobserver.Cache, fetcher blob.Fetcher) *CachingFetcher {
	return &CachingFetcher{c: cache, sf: fetcher}
}

// A CachingFetcher is a blob.Fetcher and a blob.SeekFetcher.
type CachingFetcher struct {
	c  blobserver.Cache
	sf blob.Fetcher

	g singleflight.Group
}

func (cf *CachingFetcher) Fetch(br blob.Ref) (file io.ReadCloser, size uint32, err error) {
	file, size, err = cf.c.Fetch(br)
	if err == nil {
		return
	}
	if err = cf.faultIn(br); err != nil {
		return
	}
	return cf.c.Fetch(br)
}

func (cf *CachingFetcher) faultIn(br blob.Ref) error {
	_, err := cf.g.Do(br.String(), func() (interface{}, error) {
		sblob, _, err := cf.sf.Fetch(br)
		if err != nil {
			return nil, err
		}
		defer sblob.Close()
		_, err = blobserver.Receive(cf.c, br, sblob)
		return nil, err
	})
	return err
}

// A DiskCache is a blob.Fetcher that serves from a local temp
// directory and is backed by a another blob.Fetcher (usually the
// pkg/client HTTP client).
type DiskCache struct {
	*CachingFetcher

	// Root is the temp directory being used to store files.
	// It is available mostly for debug printing.
	Root string

	cleanAll bool // cleaning policy. TODO: something better.
}

// NewDiskCache returns a new DiskCache from a Fetcher, which
// is usually the pkg/client HTTP client (which typically has much
// higher latency and lower bandwidth than local disk).
func NewDiskCache(fetcher blob.Fetcher) (*DiskCache, error) {
	cacheDir := filepath.Join(osutil.CacheDir(), "blobs")
	if !osutil.DirExists(cacheDir) {
		if err := os.Mkdir(cacheDir, 0700); err != nil {
			log.Printf("Warning: failed to make %s: %v; using tempdir instead", cacheDir, err)
			cacheDir, err = ioutil.TempDir("", "camlicache")
			if err != nil {
				return nil, err
			}
		}
	}
	// TODO: max disk size, keep LRU of access, smarter cleaning, etc
	// TODO: use diskpacked instead? harder to clean, though.
	diskcache, err := localdisk.New(cacheDir)
	if err != nil {
		return nil, err
	}
	dc := &DiskCache{
		CachingFetcher: NewCachingFetcher(diskcache, fetcher),
		Root:           cacheDir,
	}
	return dc, nil
}

// Clean cleans some or all of the DiskCache.
func (dc *DiskCache) Clean() {
	// TODO: something between nothing and deleting everything.
	if dc.cleanAll {
		os.RemoveAll(dc.Root)
	}
}

var (
	_ blob.Fetcher = (*CachingFetcher)(nil)
	_ blob.Fetcher = (*DiskCache)(nil)
)
