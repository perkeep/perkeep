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
	"camli/blobref"
	"camli/blobserver"

	"os"
)

func NewCachingFetcher(cacheTarget blobserver.Cache, sfetcher blobref.StreamingFetcher) blobref.Fetcher {
	return &CachingFetcher{cacheTarget, sfetcher}
}

type CachingFetcher struct {
	c  blobserver.Cache
	sf blobref.StreamingFetcher
}

func (cf *CachingFetcher) Fetch(br *blobref.BlobRef) (file blobref.ReadSeekCloser, size int64, err os.Error) {
	file, size, err = cf.c.Fetch(br)
	if err == nil {
		return
	}

	// TODO: let fetches some in real-time with stream in the
	// common case, only blocking if a Seek-forward is encountered
	// mid-download.  But for now we're lazy and first copy the
	// whole thing to cache.
	sblob, size, err := cf.sf.FetchStreaming(br)
	if err != nil {
		return nil, 0, err
	}

	_, err = cf.c.ReceiveBlob(br, sblob, nil)
	if err != nil {
		return nil, 0, err
	}

	return cf.c.Fetch(br)
}
