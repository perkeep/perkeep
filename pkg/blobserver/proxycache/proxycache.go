/*
Copyright 2014 The Perkeep Authors

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

If the provided maxCacheBytes is unspecified, the default is 512MB.

Example config:

	      "/cache/": {
	          "handler": "storage-proxycache",
	          "handlerArgs": {
			  "origin": "/cloud-blobs/",
			  "cache": "/local-ssd/",
			  "maxCacheBytes": 536870912
	          }
	      },
*/
package proxycache // import "perkeep.org/pkg/blobserver/proxycache"

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"sync"

	"go4.org/jsonconfig"
	"go4.org/syncutil"

	"perkeep.org/internal/lru"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

// Storage implements the "proxycache" blob storage.
type Storage struct {
	origin blobserver.Storage
	cache  blobserver.Storage

	debug         bool
	maxCacheBytes int64

	mu         sync.Mutex // guards following
	lru        *lru.Cache
	cacheBytes int64
}

var (
	_ blobserver.Storage = (*Storage)(nil)
	_ blob.SubFetcher    = (*Storage)(nil)
	// TODO:
	// _ blobserver.Generationer = (*Storage)(nil)
)

// New returns a proxycache blob storage that reads from cache,
// then origin, populating cache as needed, up to a total of maxBytes.
func New(maxBytes int64, cache, origin blobserver.Storage) *Storage {
	sto := &Storage{
		origin:        origin,
		cache:         cache,
		lru:           lru.NewUnlocked(0),
		maxCacheBytes: maxBytes,
	}
	return sto
}

func init() {
	blobserver.RegisterStorageConstructor("proxycache", blobserver.StorageConstructor(newFromConfig))
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
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
	return New(maxCacheBytes, cacheSto, originSto), nil
}

// must hold sto.mu.
// Reports whether an item was removed.
func (sto *Storage) removeOldest() bool {
	ctx := context.TODO()
	k, v := sto.lru.RemoveOldest()
	if v == nil {
		return false
	}
	sb := v.(blob.SizedRef)
	// TODO: run these without sto.mu held in background
	// goroutine? at least pass a context?
	err := sto.cache.RemoveBlobs(ctx, []blob.Ref{sb.Ref})
	if err != nil {
		log.Printf("proxycache: could not remove oldest blob %v (%d bytes): %v", sb.Ref, sb.Size, err)
		sto.lru.Add(k, v)
		return false
	}
	if sto.debug {
		log.Printf("proxycache: removed blob %v (%d bytes)", sb.Ref, sb.Size)
	}
	sto.cacheBytes -= int64(sb.Size)
	return true
}

func (sto *Storage) touch(sb blob.SizedRef) {
	key := sb.Ref.String()

	sto.mu.Lock()
	defer sto.mu.Unlock()

	_, old := sto.lru.Get(key)
	if !old {
		sto.lru.Add(key, sb)
		sto.cacheBytes += int64(sb.Size)

		// Clean while needed.
		for sto.cacheBytes > sto.maxCacheBytes {
			if !sto.removeOldest() {
				break
			}
		}
	}
}

func (sto *Storage) Fetch(ctx context.Context, b blob.Ref) (rc io.ReadCloser, size uint32, err error) {
	rc, size, err = sto.cache.Fetch(ctx, b)
	if err == nil {
		sto.touch(blob.SizedRef{Ref: b, Size: size})
		return
	}
	if !errors.Is(err, os.ErrNotExist) {
		log.Printf("warning: proxycache cache fetch error for %v: %v", b, err)
	}
	rc, size, err = sto.origin.Fetch(ctx, b)
	if err != nil {
		return
	}
	all, err := io.ReadAll(rc)
	if err != nil {
		return
	}
	if _, err := blobserver.Receive(ctx, sto.cache, b, bytes.NewReader(all)); err != nil {
		log.Printf("populating proxycache cache for %v: %v", b, err)
	} else {
		sto.touch(blob.SizedRef{Ref: b, Size: size})
	}
	return io.NopCloser(bytes.NewReader(all)), size, nil
}

func (sto *Storage) SubFetch(ctx context.Context, ref blob.Ref, offset, length int64) (io.ReadCloser, error) {
	if sf, ok := sto.cache.(blob.SubFetcher); ok {
		rc, err := sf.SubFetch(ctx, ref, offset, length)
		if err == nil {
			return rc, nil
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, blob.ErrUnimplemented) {
			log.Printf("proxycache: error fetching from cache %T: %v", sto.cache, err)
		}
	}
	if sf, ok := sto.origin.(blob.SubFetcher); ok {
		return sf.SubFetch(ctx, ref, offset, length)
	}
	return nil, blob.ErrUnimplemented
}

func (sto *Storage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	need := map[blob.Ref]bool{}
	for _, br := range blobs {
		need[br] = true
	}
	err := sto.cache.StatBlobs(ctx, blobs, func(sb blob.SizedRef) error {
		sto.touch(sb)
		delete(need, sb.Ref)
		return fn(sb)
	})
	if err != nil {
		return err
	}
	if len(need) == 0 {
		// Cache had them all.
		return nil
	}
	// And now any missing ones:
	blobs = make([]blob.Ref, 0, len(need))
	for br := range need {
		blobs = append(blobs, br)
	}
	return sto.origin.StatBlobs(ctx, blobs, func(sb blob.SizedRef) error {
		sto.touch(sb)
		return fn(sb)
	})
}

func (sto *Storage) ReceiveBlob(ctx context.Context, br blob.Ref, src io.Reader) (blob.SizedRef, error) {
	// Slurp the whole blob before replicating. Bounded by 16 MB anyway.
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, src); err != nil {
		return blob.SizedRef{}, err
	}

	sb, err := sto.origin.ReceiveBlob(ctx, br, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return sb, err
	}

	if _, err := sto.cache.ReceiveBlob(ctx, br, bytes.NewReader(buf.Bytes())); err != nil {
		log.Printf("proxycache: ignoring error populating blob %v in cache: %v", br, err)
	} else {
		sto.touch(sb)
	}
	return sb, err
}

func (sto *Storage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	var gr syncutil.Group
	gr.Go(func() error {
		return sto.cache.RemoveBlobs(ctx, blobs)
	})
	gr.Go(func() error {
		return sto.origin.RemoveBlobs(ctx, blobs)
	})
	gr.Wait()
	return gr.Err()
}

func (sto *Storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	return sto.origin.EnumerateBlobs(ctx, dest, after, limit)
}
