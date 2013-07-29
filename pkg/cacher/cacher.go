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
	"os"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/localdisk"
	"camlistore.org/pkg/singleflight"
	"camlistore.org/pkg/types"
)

// NewCachingFetcher returns a CachingFetcher that fetches from
// fetcher and writes to and serves from cache.
func NewCachingFetcher(cache blobserver.Cache, fetcher blobref.StreamingFetcher) *CachingFetcher {
	return &CachingFetcher{c: cache, sf: fetcher}
}

// A CachingFetcher is a blobref.StreamingFetcher and a blobref.SeekFetcher.
type CachingFetcher struct {
	c  blobserver.Cache
	sf blobref.StreamingFetcher

	g singleflight.Group
}

func (cf *CachingFetcher) FetchStreaming(br *blobref.BlobRef) (file io.ReadCloser, size int64, err error) {
	file, size, err = cf.c.Fetch(br)
	if err == nil {
		return
	}
	if err = cf.faultIn(br); err != nil {
		return
	}
	return cf.c.Fetch(br)
}

func (cf *CachingFetcher) Fetch(br *blobref.BlobRef) (file types.ReadSeekCloser, size int64, err error) {
	file, size, err = cf.c.Fetch(br)
	if err == nil {
		return
	}
	if err = cf.faultIn(br); err != nil {
		return
	}
	return cf.c.Fetch(br)
}

func (cf *CachingFetcher) faultIn(br *blobref.BlobRef) error {
	_, err := cf.g.Do(br.String(), func() (interface{}, error) {
		sblob, _, err := cf.sf.FetchStreaming(br)
		if err != nil {
			return nil, err
		}
		defer sblob.Close()
		_, err = cf.c.ReceiveBlob(br, sblob)
		return nil, err
	})
	return err
}

// A DiskCache is a blobref.StreamingFetcher and blobref.SeekFetcher
// that serves from a local temp directory and is backed by a another
// blobref.StreamingFetcher (usually the pkg/client HTTP client).
type DiskCache struct {
	*CachingFetcher

	// Root is the temp directory being used to store files.
	// It is available mostly for debug printing.
	Root string
}

// NewDiskCache returns a new DiskCache from a StreamingFetcher, which
// is usually the pkg/client HTTP client (which typically has much
// higher latency and lower bandwidth than local disk).
func NewDiskCache(fetcher blobref.StreamingFetcher) (*DiskCache, error) {
	// TODO: max disk size, keep LRU of access, smarter cleaning,
	// persistent directory per-user, etc.

	cacheDir, err := ioutil.TempDir("", "camlicache")
	if err != nil {
		return nil, err
	}
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
	// TODO: something less aggressive?
	os.RemoveAll(dc.Root)
}

var (
	_ blobref.StreamingFetcher = (*CachingFetcher)(nil)
	_ blobref.SeekFetcher      = (*CachingFetcher)(nil)
	_ blobref.StreamingFetcher = (*DiskCache)(nil)
	_ blobref.SeekFetcher      = (*DiskCache)(nil)
)
