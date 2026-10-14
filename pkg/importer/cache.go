/*
Copyright 2026 The Perkeep Authors

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

package importer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/sorted"
	_ "perkeep.org/pkg/sorted/defaultkv"
)

var errCacheClosed = errors.New("importer: caches are closed")

func (im *importer) cache() (sorted.KeyValue, error) {
	h := im.host
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if h.cacheClosed {
		return nil, errCacheClosed
	}
	im.cacheOnce.Do(func() {
		im.cacheStore, im.cacheErr = h.openCacheScope(im.name, blob.Ref{})
	})
	return im.cacheStore, im.cacheErr
}

func (ia *importerAcct) cache() (sorted.KeyValue, error) {
	h := ia.im.host
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if h.cacheClosed {
		return nil, errCacheClosed
	}
	ia.cacheOnce.Do(func() {
		if ia.acct == nil || !ia.acct.PermanodeRef().Valid() {
			ia.cacheErr = errors.New("importer: account cache requires a valid account permanode")
			return
		}
		ia.cacheStore, ia.cacheErr = h.openCacheScope(ia.im.name, ia.acct.PermanodeRef())
	})
	return ia.cacheStore, ia.cacheErr
}

// openCacheScope runs with cacheMu held. Cache opening is allowed after run
// cancellation: a managed run may need to save state before it returns.
// Only closeCaches, called after those runs return, gates cache access.
func (h *Host) openCacheScope(name string, account blob.Ref) (sorted.KeyValue, error) {
	if h.cache != nil {
		// Only an explicitly injected backing store is shared. Default
		// memory and disk caches each have their own KV per scope.
		// Register prohibits colons in names, keeping boundaries unambiguous.
		prefix := fmt.Sprintf("importer:%s:", name)
		if account.Valid() {
			prefix = fmt.Sprintf("account:%s:%s:", name, account)
		}
		return sorted.WithPrefix(h.cache, prefix), nil
	}
	var store sorted.KeyValue
	if h.cacheRoot == "" {
		store = sorted.NewMemoryKeyValue()
	} else {
		dir, err := cacheImporterDirName(name)
		if err != nil {
			return nil, err
		}
		if account.Valid() {
			dir += "-" + account.String()
		}
		store, err = openCacheStore(filepath.Join(h.cacheRoot, dir), h.cacheType)
		if err != nil {
			return nil, err
		}
	}
	h.cacheStores = append(h.cacheStores, store)
	return sorted.WithPrefix(store, ""), nil
}

func openCacheStore(dir, typ string) (sorted.KeyValue, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("importer cache directory: %w", err)
	}
	store, err := sorted.NewKeyValue(jsonconfig.Obj{
		"type": typ,
		"file": filepath.Join(dir, "cache."+typ),
	})
	if err != nil {
		// Some constructors return an open store along with a NeedWipeError.
		// A disposable cache must not leave that store locked on failure.
		if store != nil {
			store.Close()
		}
		return nil, fmt.Errorf("importer cache: %w", err)
	}
	if store == nil {
		return nil, fmt.Errorf("importer cache: backend %q returned no store", typ)
	}
	return store, nil
}

func (h *Host) closeCaches() error {
	h.cacheMu.Lock()
	h.cacheClosed = true
	stores := h.cacheStores
	h.cacheStores = nil
	h.cacheMu.Unlock()
	var firstErr error
	for _, store := range stores {
		if err := store.Close(); firstErr == nil && err != nil {
			firstErr = err
		}
	}
	return firstErr
}

// cacheImporterDirName keeps usual lowercase names readable while encoding
// everything else byte-wise. Uppercase is escaped too, avoiding collisions on
// case-insensitive filesystems. In particular, '-' is escaped so importer-only
// directories cannot collide with name-accountref directories. The result has
// no path separators, trailing dots/spaces, or Windows device names.
func cacheImporterDirName(name string) (string, error) {
	if name == "" {
		return "", errors.New("importer: persistent cache requires a nonempty importer name")
	}
	var b strings.Builder
	for i := range len(name) {
		c := name[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	dir := b.String()
	switch dir {
	case "con", "prn", "aux", "nul",
		"com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
		"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9":
		dir = fmt.Sprintf("%%%02X%s", dir[0], dir[1:])
	}
	return dir, nil
}
