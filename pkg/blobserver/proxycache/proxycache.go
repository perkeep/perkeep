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
... TODO
          }
      },
*/
package proxycache

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/sorted"
	"go4.org/jsonconfig"
	"golang.org/x/net/context"
)

const buffered = 8

type sto struct {
	origin        blobserver.Storage
	cache         blobserver.Storage
	kv            sorted.KeyValue
	maxCacheBytes int64

	mu         sync.Mutex // guards cacheBytes & kv mutations
	cacheBytes int64
}

func init() {
	blobserver.RegisterStorageConstructor("proxycache", blobserver.StorageConstructor(newFromConfig))
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	var (
		origin        = config.RequiredString("origin")
		cache         = config.RequiredString("cache")
		kvConf        = config.RequiredObject("meta")
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
	kv, err := sorted.NewKeyValue(kvConf)
	if err != nil {
		return nil, err
	}

	// TODO: enumerate through kv and calculate current size.
	// Maybe also even enumerate through cache to see if they match.
	// Or even: keep it only in memory and not in kv?

	s := &sto{
		origin:        originSto,
		cache:         cacheSto,
		maxCacheBytes: maxCacheBytes,
		kv:            kv,
	}
	return s, nil
}

func (sto *sto) touchBlob(sb blob.SizedRef) {
	key := sb.Ref.String()
	sto.mu.Lock()
	defer sto.mu.Unlock()
	val := fmt.Sprintf("%d:%d", sb.Size, time.Now().Unix())
	_, err := sto.kv.Get(key)
	new := err != nil
	if err == sorted.ErrNotFound {
		new = true
	} else if err != nil {
		log.Printf("proxycache: reading meta for key %q: %v", key, err)
	}
	if err := sto.kv.Set(key, val); err != nil {
		log.Printf("proxycache: updating meta for %v: %v", sb, err)
	}
	if new {
		sto.cacheBytes += int64(sb.Size)
	}
	if sto.cacheBytes > sto.maxCacheBytes {
		// TODO: clean some stuff.
	}
}

func (sto *sto) Fetch(b blob.Ref) (rc io.ReadCloser, size uint32, err error) {
	rc, size, err = sto.cache.Fetch(b)
	if err == nil {
		sto.touchBlob(blob.SizedRef{b, size})
		return
	}
	if err != os.ErrNotExist {
		log.Printf("warning: proxycache cache fetch error for %v: %v", b, err)
	}
	rc, size, err = sto.cache.Fetch(b)
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
		sto.touchBlob(blob.SizedRef{b, size})
	}()
	return ioutil.NopCloser(bytes.NewReader(all)), size, nil
}

func (sto *sto) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	// TODO: stat from cache if possible? then at least we have
	// to be sure we never have blobs in the cache that we don't have
	// in the origin. For now, be paranoid and just proxy to the origin:
	return sto.origin.StatBlobs(dest, blobs)
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
	sto.touchBlob(sb)
	return sto.origin.ReceiveBlob(br, bytes.NewReader(buf.Bytes()))
}

func (sto *sto) RemoveBlobs(blobs []blob.Ref) error {
	// Ignore result of cache removal
	go sto.cache.RemoveBlobs(blobs)
	return sto.origin.RemoveBlobs(blobs)
}

func (sto *sto) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	return sto.origin.EnumerateBlobs(ctx, dest, after, limit)
}

// TODO:
//var _ blobserver.Generationer = (*sto)(nil)

func (sto *sto) x_ResetStorageGeneration() error {
	panic("TODO")
}

func (sto *sto) x_StorageGeneration() (initTime time.Time, random string, err error) {
	panic("TODO")
}
