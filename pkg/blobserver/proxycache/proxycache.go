/*
Copyright 2014 The Camlistore Authors

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

/*
Package proxycache registers the "proxycache" blobserver storage type,
which uses a provided blobserver as a cache for a second origin
blobserver.

The proxycache blobserver type also takes a sorted.KeyValue reference
which it uses as the LRU for which old items to evict from the cache.

Example config:

      "/cache/": {
          "handler": "storage-proxycache",
          "handlerArgs": {
		  "origin": "",
		  "cache": "",
		  "meta": {},
		  "maxCacheBytes": 536870912
          }
      },
*/
package proxycache // import "camlistore.org/pkg/blobserver/proxycache"

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/stats"
	"camlistore.org/pkg/lru"
	"go4.org/jsonconfig"
	"go4.org/syncutil"
)

type sto struct {
	origin     blobserver.Storage
	cache      blobserver.Storage
	statsCache *stats.Receiver

	lru           *lru.Cache
	maxCacheBytes int64
	strictStats   bool // if true, only check with origin for Stats

	mu                sync.Mutex // guards cacheBytes, isCleaning, and lastCleanFinished mutations
	isCleaning        bool
	lastCleanFinished time.Time
	cacheBytes        int64
	debug             bool
}

func NewCache(maxBytes int64, cache, origin blobserver.Storage) blobserver.Storage {
	sto := &sto{
		origin:        origin,
		cache:         cache,
		lru:           lru.New(0),
		statsCache:    &stats.Receiver{},
		maxCacheBytes: maxBytes,
		debug:         false,
	}
	sto.verifyCache(nil) // sets cacheBytes
	return sto
}

func init() {
	blobserver.RegisterStorageConstructor("proxycache", blobserver.StorageConstructor(newFromConfig))
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	var (
		origin        = config.RequiredString("origin")
		cache         = config.RequiredString("cache")
		maxCacheBytes = config.OptionalInt64("maxCacheBytes", 512<<20)
	)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	cacheSto, err := ld.GetStorage(cache)
	if err != nil {
		return nil, err
	}
	originSto, err := ld.GetStorage(origin)
	if err != nil {
		return nil, err
	}

	s := &sto{
		origin:        originSto,
		cache:         cacheSto,
		maxCacheBytes: maxCacheBytes,
		lru:           lru.New(0),
		statsCache:    &stats.Receiver{},
		debug:         false,
	}

	s.verifyCache(nil)
	return s, nil
}

func (sto *sto) cleanCache() {
	go func() {
		sto.mu.Lock()
		isCleaning := sto.isCleaning
		sto.mu.Unlock()

		if isCleaning {
			// if we're already cleaning, don't start again
			return
		}

		defer func() {
			sto.mu.Lock()
			sto.isCleaning = false
			sto.lastCleanFinished = time.Now()
			sto.mu.Unlock()
		}()

		sto.mu.Lock()
		sto.isCleaning = true
		sto.mu.Unlock()

		for sto.needsRemoval() {
			sto.removeOldest()
		}
	}()
}

func (sto *sto) needsRemoval() bool {
	sto.mu.Lock()
	defer sto.mu.Unlock()

	if sto.debug {
		log.Printf("cache size is %d/%d (%f%%)", sto.cacheBytes, sto.maxCacheBytes, 100*float64(sto.cacheBytes)/float64(sto.maxCacheBytes))
	}

	return sto.cacheBytes > sto.maxCacheBytes
}

func (sto *sto) removeOldest() {
	sto.mu.Lock()
	defer sto.mu.Unlock()

	k, v := sto.lru.RemoveOldest()
	entry := v.(*cacheEntry)
	err := sto.cache.RemoveBlobs([]blob.Ref{entry.sb.Ref})
	if err != nil {
		log.Printf("proxycache: could not remove oldest blob %v: %v", k, err)
		sto.lru.Add(k, entry)
		return
	}

	err = sto.statsCache.RemoveBlobs([]blob.Ref{entry.sb.Ref})
	if err != nil {
		log.Println("proxycache: error removing blob:", err)
		return
	}

	if sto.debug {
		log.Println("proxycache: removed blob:", k)
	}
	sto.cacheBytes -= int64(entry.sb.Size)
}

func (sto *sto) touchStat(sb blob.SizedRef) {
	key := sb.Ref.String()
	_, old := sto.lru.Get(key)
	sto.lru.Add(key, &cacheEntry{sb, time.Now(), true})

	if !old {
		sto.mu.Lock()
		defer sto.mu.Unlock()
		_, err := sto.statsCache.ReceiveRef(sb.Ref, int64(sb.Size))
		if err != nil {
			log.Printf("error touching stat %v: %v", sb, err)
		}
	}

	if sto.debug {
		log.Println("proxycache: touched stat:", sb)
	}

	sto.cleanCache()
}

func (sto *sto) touchBlob(sb blob.SizedRef) {
	key := sb.Ref.String()

	_, old := sto.lru.Get(key)
	sto.lru.Add(key, &cacheEntry{sb, time.Now(), false})

	if !old {
		sto.mu.Lock()
		defer sto.mu.Unlock()
		sto.cacheBytes += int64(sb.Size)
	}

	if sto.debug {
		log.Println("proxycache: touched blob:", key)
	}

	go sto.touchStat(sb)

	sto.cleanCache()
}

type cacheEntry struct {
	sb       blob.SizedRef
	modtime  time.Time
	statOnly bool
}

func (sto *sto) Fetch(b blob.Ref) (rc io.ReadCloser, size uint32, err error) {
	rc, size, err = sto.cache.Fetch(b)
	if err == nil {
		sto.touchBlob(blob.SizedRef{Ref: b, Size: size})
		return
	}
	if err != os.ErrNotExist {
		log.Printf("warning: proxycache cache fetch error for %v: %v", b, err)
	}
	rc, size, err = sto.origin.Fetch(b)
	if err != nil {
		return
	}
	all, err := ioutil.ReadAll(rc)
	if err != nil {
		return
	}
	go func() {
		if _, err := blobserver.Receive(sto.cache, b, bytes.NewReader(all)); err != nil {
			log.Printf("populating proxycache cache for %v: %v", b, err)
			return
		}
		sto.touchBlob(blob.SizedRef{Ref: b, Size: size})
	}()
	return ioutil.NopCloser(bytes.NewReader(all)), size, nil
}

type CacheMissingRefError struct {
	Ref  blob.Ref
	Size uint32
}

func (e CacheMissingRefError) Error() string {
	return fmt.Sprintf("proxycache: cache is missing ref %v", e.Ref)
}

type CacheHasExtraError struct {
	Ref  blob.Ref
	Size uint32
}

func (e CacheHasExtraError) Error() string {
	return fmt.Sprintf("proxycache: cache has ref (%v) that does not exist in the origin", e.Ref)
}

type CacheHasWrongSizeError struct {
	Ref                   blob.Ref
	OriginSize, CacheSize uint32
}

func (e CacheHasWrongSizeError) Error() string {
	return fmt.Sprintf("proxycache: for ref %v cache has size %d, but origin has %d", e.Ref, e.CacheSize, e.OriginSize)
}

func (sto *sto) verifyCache(refs []blob.Ref) error {
	current := make(map[blob.Ref]uint32, 50)
	currentRefs := make([]blob.Ref, 0, 50)

	needRefsFromCache := map[blob.Ref]struct{}{}
	for _, ref := range refs {
		needRefsFromCache[ref] = struct{}{}
	}

	if len(refs) == 0 {
		var cacheBytes int64
		err := blobserver.EnumerateAll(context.Background(), sto.cache, func(sb blob.SizedRef) error {
			cacheBytes += int64(sb.Size)
			current[sb.Ref] = sb.Size
			currentRefs = append(currentRefs, sb.Ref)
			return nil
		})
		if err != nil {
			return fmt.Errorf("proxycache: error enumerating stats for verification: %v", err)
		}
		sto.mu.Lock()
		sto.cacheBytes = cacheBytes
		sto.mu.Unlock()
	} else {
		cacheDest := make(chan blob.SizedRef, len(refs))
		errCh := make(chan error, 1)

		go func() {
			errCh <- sto.cache.StatBlobs(cacheDest, refs)
			close(cacheDest)
		}()

		for sb := range cacheDest {
			current[sb.Ref] = sb.Size
			currentRefs = append(currentRefs, sb.Ref)
			delete(needRefsFromCache, sb.Ref)
		}

		err := <-errCh
		if err != nil {
			log.Println("proxycache: error getting stats from cache:", err)
		}
	}

	dest := make(chan blob.SizedRef, 50)
	errCh := make(chan error, 1)

	go func() {
		errCh <- sto.origin.StatBlobs(dest, currentRefs)
		close(dest)
	}()

	errs := errList{}

	for neededRef := range needRefsFromCache {
		errs = append(errs, CacheMissingRefError{
			Ref: neededRef,
		})
	}

	for sb := range dest {
		cacheSize, cacheHas := current[sb.Ref]
		if !cacheHas {
			errs = append(errs, CacheMissingRefError{
				Ref:  sb.Ref,
				Size: sb.Size,
			})
			continue
		}

		if cacheSize != sb.Size {
			errs = append(errs, CacheHasWrongSizeError{
				Ref:        sb.Ref,
				OriginSize: sb.Size,
				CacheSize:  cacheSize,
			})
			continue
		}

		delete(current, sb.Ref)
	}

	err := <-errCh
	if err != nil {
		errs = append(errs, err)
	}

	for ref, size := range current {
		errs = append(errs, CacheHasExtraError{
			Ref:  ref,
			Size: size,
		})
	}

	return errs.OrNil()
}

type errList []error

func (e errList) OrNil() error {
	switch len(e) {
	case 0:
		return nil
	case 1:
		return e[0]
	default:
		return e
	}
}

func (e errList) Error() string {
	switch len(e) {
	case 0:
		return ""
	case 1:
		return e[0].Error()
	default:
		strs := make([]string, len(e))
		for i, err := range e {
			strs[i] = err.Error()
		}
		return fmt.Sprintf("%d errors: %s", len(strs), strings.Join(strs, ", "))
	}
}

func (sto *sto) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	gr := &syncutil.Group{}

	cacheHits := make(chan blob.SizedRef, 0)
	cacheMisses := make(chan blob.SizedRef, 0)

	var timer *time.Timer

	if sto.strictStats {
		gr.Go(func() error {
			defer close(cacheMisses)
			return sto.origin.StatBlobs(cacheMisses, blobs)
		})
	} else {

		gr.Go(func() error {
			defer close(cacheHits)
			return sto.statsCache.StatBlobs(cacheHits, blobs)
		})

		timer = time.AfterFunc(50*time.Millisecond, func() {
			gr.Go(func() error {
				defer close(cacheMisses)
				return sto.origin.StatBlobs(cacheMisses, blobs)
			})

		})
	}

	gr.Go(func() error {
		seenBlobs := map[blob.Ref]struct{}{}

		defer func() {
			timer.Stop()
		}()

		for {
			var sb blob.SizedRef
			var moreHits, moreMisses bool

			select {
			case sb, moreHits = <-cacheHits:
				if moreHits {
					if sto.debug {
						log.Println("cache hit:", sb)
					}
				} else {
					cacheHits = nil
					continue
				}
			case sb, moreMisses = <-cacheMisses:
				if moreMisses {
					if sto.debug {
						log.Println("cache miss:", sb)
					}
				} else {
					cacheMisses = nil
					continue
				}
			}

			if sb.Valid() {
				sto.touchStat(sb)

				_, old := seenBlobs[sb.Ref]
				if !old {
					seenBlobs[sb.Ref] = struct{}{}
					dest <- sb
				}
			}

			if len(seenBlobs) == len(blobs) {
				return nil
			}

			if !moreHits && !moreMisses {
				break
			}
		}

		return errors.New("unexpected end of blob stats: couldn't find all the stats")
	})

	gr.Wait()
	return gr.Err()
}

func (sto *sto) ReceiveBlob(br blob.Ref, src io.Reader) (sb blob.SizedRef, err error) {
	// Slurp the whole blob before replicating. Bounded by 16 MB anyway.
	var buf bytes.Buffer
	if _, err = io.Copy(&buf, src); err != nil {
		return
	}

	if _, err = sto.cache.ReceiveBlob(br, bytes.NewReader(buf.Bytes())); err != nil {
		return
	}
	sb, err = sto.origin.ReceiveBlob(br, bytes.NewReader(buf.Bytes()))
	if err == nil {
		sto.touchBlob(sb)
	}
	return sb, err
}

func (sto *sto) RemoveBlobs(blobs []blob.Ref) error {
	var gr syncutil.Group
	gr.Go(func() error {
		return sto.cache.RemoveBlobs(blobs)
	})
	gr.Go(func() error {
		return sto.origin.RemoveBlobs(blobs)
	})
	gr.Wait()
	return gr.Err()
}

func (sto *sto) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	return sto.origin.EnumerateBlobs(ctx, dest, after, limit)
}

// TODO:
//var _ blobserver.Generationer = (*sto)(nil)
