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

// Package cacher provides various blobref fetching caching mechanisms.
package cacher // import "perkeep.org/pkg/cacher"

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/localdisk"

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
	// cacheHitHook, if set, is called right after a cache hit. It is meant to add
	// potential side-effects from calling the Fetcher that would have happened
	// if we had had a cache miss. It is the responsibility of cacheHitHook to return
	// a ReadCloser equivalent to the state that rc was given in.
	cacheHitHook func(br blob.Ref, rc io.ReadCloser) (io.ReadCloser, error)

	g singleflight.Group
}

// SetCacheHitHook sets a function that will modify the return values from Fetch
// in the case of a cache hit.
// Its purpose is to add potential side-effects from calling the Fetcher that would
// have happened if we had had a cache miss. It is the responsibility of fn to
// return a ReadCloser equivalent to the state that rc was given in.
func (cf *CachingFetcher) SetCacheHitHook(fn func(br blob.Ref, rc io.ReadCloser) (io.ReadCloser, error)) {
	cf.cacheHitHook = fn
}

func (cf *CachingFetcher) Fetch(ctx context.Context, br blob.Ref) (content io.ReadCloser, size uint32, err error) {
	content, size, err = cf.c.Fetch(ctx, br)
	if err == nil {
		if cf.cacheHitHook != nil {
			rc, err := cf.cacheHitHook(br, content)
			if err != nil {
				content.Close()
				return nil, 0, err
			}
			content = rc
		}
		return
	}
	if err = cf.faultIn(ctx, br); err != nil {
		return
	}
	return cf.c.Fetch(ctx, br)
}

func (cf *CachingFetcher) faultIn(ctx context.Context, br blob.Ref) error {
	_, err := cf.g.Do(br.String(), func() (any, error) {
		sblob, _, err := cf.sf.Fetch(ctx, br)
		if err != nil {
			return nil, err
		}
		defer sblob.Close()
		_, err = blobserver.Receive(ctx, cf.c, br, sblob)
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
			cacheDir, err = os.MkdirTemp("", "camlicache")
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
