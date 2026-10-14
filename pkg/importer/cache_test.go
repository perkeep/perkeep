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
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/sorted"
	_ "perkeep.org/pkg/sorted/kvfile"
	_ "perkeep.org/pkg/sorted/sqlite"
	"perkeep.org/pkg/test"
)

func TestHostMemoryCache(t *testing.T) {
	first, err := NewHost(HostConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewHost(HostConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.cache != nil || second.cache != nil || len(first.cacheStores) != 0 || len(second.cacheStores) != 0 {
		t.Fatal("NewHost eagerly allocated a shared memory cache")
	}
	account, setup := cacheTestContexts(first, "dummy1", "first")
	sibling, _ := cacheTestContexts(first, "dummy1", "second")
	other, _ := cacheTestContexts(first, "dummy2", "first")
	views := []sorted.KeyValue{
		requireCache(t, account.ImporterCache),
		requireCache(t, account.AccountCache),
		requireCache(t, sibling.AccountCache),
		requireCache(t, other.ImporterCache),
		requireCache(t, other.AccountCache),
	}
	if len(first.cacheStores) != len(views) {
		t.Fatalf("allocated %d stores for %d scopes", len(first.cacheStores), len(views))
	}
	for i, view := range views {
		if err := view.Set("ratelimit", fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
		if err := view.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for i, store := range first.cacheStores {
		// The plain key must be in this scope's own backing KV, not in a
		// shared KV behind a hidden namespace prefix.
		if got, err := store.Get("ratelimit"); err != nil || got != fmt.Sprint(i) {
			t.Errorf("backing store %d Get = %q, %v; want %d", i, got, err, i)
		}
		for j := 0; j < i; j++ {
			if store == first.cacheStores[j] {
				t.Errorf("scopes %d and %d share a backing KV", i, j)
			}
		}
	}
	if views[0] != requireCache(t, sibling.ImporterCache) || views[1] != requireCache(t, setup.AccountCache) {
		t.Fatal("repeated setup/run access did not reuse the scope's view")
	}
	if len(first.cacheStores) != len(views) {
		t.Fatal("repeated access allocated another memory store")
	}
	maxKey := strings.Repeat("k", sorted.MaxKeySize)
	if err := views[0].Set(maxKey, "no prefix overhead"); err != nil {
		t.Fatal(err)
	}
	if got, err := first.cacheStores[0].Get(maxKey); err != nil || got != "no prefix overhead" {
		t.Errorf("maximum-size plain key = %q, %v", got, err)
	}
	secondAccount, _ := cacheTestContexts(second, "dummy1", "first")
	if _, err := requireCache(t, secondAccount.AccountCache).Get("ratelimit"); !errors.Is(err, sorted.ErrNotFound) {
		t.Errorf("another host's cache Get = %v; want ErrNotFound", err)
	}
}

type closeTrackingCache struct {
	sorted.KeyValue
	mu       sync.Mutex
	closes   int
	closeErr error
	closed   chan struct{}
}

func (c *closeTrackingCache) Set(key, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closes != 0 {
		return errors.New("write to closed cache")
	}
	return c.KeyValue.Set(key, value)
}

func (c *closeTrackingCache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
	if c.closes == 1 && c.closed != nil {
		close(c.closed)
	}
	return c.closeErr
}

func (c *closeTrackingCache) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closes
}

func TestHostInjectedCacheOwnership(t *testing.T) {
	cache := &closeTrackingCache{KeyValue: sorted.NewMemoryKeyValue()}
	h, err := NewHost(HostConfig{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if h.cache != cache {
		t.Fatal("Host did not use the supplied cache")
	}
	for range 2 {
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if got := cache.closeCount(); got != 0 {
		t.Errorf("Host closed caller-owned cache %d times", got)
	}
	if err := cache.Set("test:state", "still open"); err != nil {
		t.Error(err)
	}
	_, err = NewHost(HostConfig{
		Cache: cache, ClientSecret: map[string]string{"dummy1": "secret without ID"},
	})
	if err == nil {
		t.Fatal("NewHost accepted a client secret without an ID")
	}
	if got := cache.closeCount(); got != 0 {
		t.Errorf("failed NewHost closed caller-owned cache %d times", got)
	}
}

func configuredCacheHost(t *testing.T, ld blobserver.Loader, cfg jsonconfig.Obj) *Host {
	t.Helper()
	handler, err := newFromConfig(ld, cfg)
	if err != nil {
		t.Fatal(err)
	}
	host := handler.(*Host)
	t.Cleanup(func() {
		if err := host.Close(); err != nil {
			t.Error(err)
		}
	})
	return host
}

func TestHostCacheConfigErrors(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	for _, cfg := range []jsonconfig.Obj{
		{"cache": "not an object"},
		{"cache": map[string]any{}},
		{"cacheRoot": true},
		{"cacheType": 10},
		{"cacheRoot": root, "cacheType": "mysql"},
		{"cacheRoot": root, "cacheType": "memory"},
		{"cacheRoot": root, "cacheType": "no-such-importer-cache"},
		{
			"cacheRoot": root,
			"dummy1":    map[string]any{"clientSecret": "secret-without-client-id"},
		},
	} {
		if _, err := newFromConfig(test.NewLoader(), cfg); err == nil {
			t.Errorf("newFromConfig(%v) succeeded; want error", cfg)
		}
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("invalid configuration created cache files: %v", err)
	}
	cache := &closeTrackingCache{KeyValue: sorted.NewMemoryKeyValue()}
	for _, hc := range []HostConfig{
		{Cache: cache, CacheRoot: root},
		{Cache: cache, CacheType: sorted.DefaultKVFileType},
		{Cache: cache, CacheRoot: root, CacheType: "sqlite"},
	} {
		if _, err := NewHost(hc); err == nil {
			t.Error("NewHost accepted an ambiguous injected cache configuration")
		}
	}
	if got := cache.closeCount(); got != 0 {
		t.Errorf("configuration errors closed caller-owned cache %d times", got)
	}
}

var cacheTestConstructorID atomic.Uint64

func TestHostCacheConstructorErrorClosesStore(t *testing.T) {
	for _, needWipe := range []bool{false, true} {
		cache := &closeTrackingCache{KeyValue: sorted.NewMemoryKeyValue()}
		typ := fmt.Sprintf("importer-cache-test-%d", cacheTestConstructorID.Add(1))
		sorted.RegisterKeyValue(typ, func(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
			if needWipe {
				cfg.RequiredString("file")
				return cache, sorted.NeedWipeError{Msg: "test cache needs repair"}
			}
			// Return an open store and leave the unknown key for the
			// registry's final configuration validation to reject.
			return cache, nil
		})
		_, err := openCacheStore(t.TempDir(), typ)
		if err == nil {
			t.Fatal("openCacheStore succeeded; want configuration error")
		}
		if got := cache.closeCount(); got != 1 {
			t.Errorf("openCacheStore closed failed store %d times; want 1", got)
		}
	}
}

func TestHostCacheConstructorReturningNil(t *testing.T) {
	typ := fmt.Sprintf("importer-cache-test-%d", cacheTestConstructorID.Add(1))
	sorted.RegisterKeyValue(typ, func(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
		cfg.RequiredString("file")
		return nil, nil
	})
	if store, err := openCacheStore(t.TempDir(), typ); store != nil || err == nil {
		t.Errorf("nil constructor result = %v, %v; want nil, error", store, err)
	}
}

type cacheBlockingImporter struct {
	Importer
	entered   chan struct{}
	cancelled chan error
	release   chan struct{}
}

func (im *cacheBlockingImporter) Run(rc *RunContext) error {
	close(im.entered)
	<-rc.Context().Done()
	cache, err := rc.ImporterCache()
	if err == nil {
		err = cache.Set("test:final", "saved before close")
	}
	im.cancelled <- err
	<-im.release
	return rc.Context().Err()
}

func (im *cacheBlockingImporter) LongPoll(rc *RunContext) error { return im.Run(rc) }
func (*cacheBlockingImporter) SummarizeAccount(*Object) string  { return "cache shutdown test" }

type cacheAutoDescriber struct{ search.QueryDescriber }

func (cacheAutoDescriber) Describe(_ context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error) {
	return &search.DescribeResponse{Meta: search.MetaMap{req.BlobRef.String(): {
		Permanode: &search.DescribedPermanode{Attr: url.Values{attrImportAuto: {"1h"}}},
	}}}, nil
}

func TestHostCloseWaitsForManagedCacheUsers(t *testing.T) {
	for _, mode := range []string{"start", "run", "long-poll"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "long-poll" && disableImporters {
				t.Skip("periodic importers disabled by environment")
			}
			impl := &cacheBlockingImporter{
				entered: make(chan struct{}), cancelled: make(chan error, 1), release: make(chan struct{}),
			}
			release := sync.OnceFunc(func() { close(impl.release) })
			defer release()
			closeErr := errors.New("test cache close error")
			cache := &closeTrackingCache{KeyValue: sorted.NewMemoryKeyValue(), closeErr: closeErr}
			h, err := NewHost(HostConfig{Cache: cache, Search: cacheAutoDescriber{}})
			if err != nil {
				t.Fatal(err)
			}
			// Model a cache opened by newFromConfig, without touching disk.
			h.cacheStores = append(h.cacheStores, h.cache)
			t.Cleanup(func() { h.Close() })
			ia := &importerAcct{
				im:   &importer{host: h, name: "cache-test", impl: impl},
				acct: &Object{pn: blob.RefFromString("cache shutdown account")},
			}
			switch mode {
			case "start":
				ia.start()
			case "run":
				go ia.run()
			case "long-poll":
				ia.lastRunDone = time.Now()
				ia.maybeStart()
			}
			select {
			case <-impl.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("importer did not start")
			}
			closed := make(chan error, 1)
			go func() { closed <- h.Close() }()
			select {
			case err := <-impl.cancelled:
				if err != nil {
					t.Fatalf("cache closed before importer returned: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Close did not cancel importer")
			}
			if got := cache.closeCount(); got != 0 {
				t.Errorf("cache closed %d times while importer still running", got)
			}
			select {
			case <-closed:
				t.Fatal("Close returned before importer returned")
			default:
			}
			release()
			select {
			case err := <-closed:
				if !errors.Is(err, closeErr) {
					t.Errorf("Close = %v; want %v", err, closeErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Close did not finish")
			}
			if err := h.Close(); !errors.Is(err, closeErr) {
				t.Errorf("repeated Close = %v; want %v", err, closeErr)
			}
			if got := cache.closeCount(); got != 1 {
				t.Errorf("cache closed %d times; want 1", got)
			}
			// All entry points must reject new managed work after Close.
			ia.start()
			ia.maybeStart()
			if err := ia.run(); err == nil {
				t.Error("run succeeded on closed Host")
			}
		})
	}
}

type cacheUncancellableImporter struct {
	Importer
	entered  chan struct{}
	release  chan struct{}
	finished chan error
}

func (im *cacheUncancellableImporter) Run(rc *RunContext) error {
	close(im.entered)
	// Model a legacy importer blocked in IO that does not use rc.Context.
	<-im.release
	cache, err := rc.ImporterCache()
	if err == nil {
		err = cache.Set("test:final", "saved after shutdown timeout")
	}
	im.finished <- err
	return err
}

func (*cacheUncancellableImporter) SummarizeAccount(*Object) string {
	return "uncancellable cache shutdown test"
}

func closeCacheHostWithShortTimeout(t *testing.T, h *Host) error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- h.closeWithTimeout(10 * time.Millisecond) }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Close succeeded while shutdown was blocked; want timeout")
		}
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not respect its timeout")
		return nil
	}
}

func TestHostCloseTimeoutLeavesCacheOpenUntilImporterReturns(t *testing.T) {
	impl := &cacheUncancellableImporter{
		entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan error, 1),
	}
	release := sync.OnceFunc(func() { close(impl.release) })
	defer release()
	cache := &closeTrackingCache{KeyValue: sorted.NewMemoryKeyValue(), closed: make(chan struct{})}
	h, err := NewHost(HostConfig{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	h.cacheStores = append(h.cacheStores, h.cache)
	t.Cleanup(func() { h.Close() })
	ia := &importerAcct{
		im:   &importer{host: h, name: "cache-test", impl: impl},
		acct: &Object{pn: blob.RefFromString("uncancellable cache shutdown account")},
	}
	ia.start()
	select {
	case <-impl.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("importer did not start")
	}
	timeoutErr := closeCacheHostWithShortTimeout(t, h)
	if err := h.Close(); err != timeoutErr {
		t.Errorf("repeated Close = %v; want original timeout %v", err, timeoutErr)
	}
	if got := cache.closeCount(); got != 0 {
		t.Errorf("cache closed %d times before importer returned", got)
	}
	if err := cache.Set("test:during-shutdown", "still open"); err != nil {
		t.Fatal(err)
	}
	if h.beginWork() {
		h.work.Done()
		t.Fatal("Host accepted new work after shutdown timed out")
	}
	release()
	select {
	case err := <-impl.finished:
		if err != nil {
			t.Errorf("importer's final cache write failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("importer did not return")
	}
	select {
	case <-cache.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("cache did not close after importer returned")
	}
	if got := cache.closeCount(); got != 1 {
		t.Errorf("cache closed %d times; want 1", got)
	}
	if err := h.Close(); err != timeoutErr {
		t.Errorf("Close after cleanup = %v; want original timeout %v", err, timeoutErr)
	}
}

type cacheBlockingClose struct {
	*closeTrackingCache
	entered chan struct{}
	release chan struct{}
}

func (c *cacheBlockingClose) Close() error {
	close(c.entered)
	<-c.release
	return c.closeTrackingCache.Close()
}

func TestHostCloseTimeoutIncludesBackendClose(t *testing.T) {
	cache := &cacheBlockingClose{
		closeTrackingCache: &closeTrackingCache{
			KeyValue: sorted.NewMemoryKeyValue(), closed: make(chan struct{}),
		},
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(cache.release) })
	defer release()
	h, err := NewHost(HostConfig{Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	h.cacheStores = append(h.cacheStores, h.cache)
	t.Cleanup(func() { h.Close() })
	timeoutErr := closeCacheHostWithShortTimeout(t, h)
	select {
	case <-cache.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("cache Close did not start")
	}
	release()
	select {
	case <-cache.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("cache Close did not finish")
	}
	if got := cache.closeCount(); got != 1 {
		t.Errorf("cache closed %d times; want 1", got)
	}
	if err := h.Close(); err != timeoutErr {
		t.Errorf("Close after cleanup = %v; want original timeout %v", err, timeoutErr)
	}
}

func cacheTestContexts(h *Host, name, account string) (*RunContext, *SetupContext) {
	ia := &importerAcct{
		im: h.imp[name], acct: &Object{pn: blob.RefFromString(account)},
	}
	return &RunContext{Host: h, ia: ia}, &SetupContext{Host: h, ia: ia}
}

func requireCache(t *testing.T, get func() (sorted.KeyValue, error)) sorted.KeyValue {
	t.Helper()
	cache, err := get()
	if err != nil {
		t.Fatal(err)
	}
	if cache == nil {
		t.Fatal("cache getter returned a nil store without an error")
	}
	return cache
}

func TestContextCachesAreScopedAndNonClosing(t *testing.T) {
	backing := &closeTrackingCache{KeyValue: sorted.NewMemoryKeyValue()}
	h, err := NewHost(HostConfig{Cache: backing})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	first, setup := cacheTestContexts(h, "dummy1", "first")
	sibling, _ := cacheTestContexts(h, "dummy1", "second")
	other, _ := cacheTestContexts(h, "dummy2", "first")
	if requireCache(t, first.ImporterCache) != requireCache(t, sibling.ImporterCache) || requireCache(t, first.ImporterCache) != requireCache(t, setup.ImporterCache) {
		t.Fatal("importer cache is not shared across accounts and setup/run contexts")
	}
	if requireCache(t, first.AccountCache) != requireCache(t, first.AccountCache) || requireCache(t, first.AccountCache) != requireCache(t, setup.AccountCache) {
		t.Fatal("account cache view is not stable across setup/run calls")
	}
	views := []sorted.KeyValue{
		requireCache(t, first.ImporterCache), requireCache(t, first.AccountCache), requireCache(t, sibling.AccountCache),
		requireCache(t, other.ImporterCache), requireCache(t, other.AccountCache),
	}
	for i, cache := range views {
		if err := cache.Set("ratelimit", fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	for i, cache := range views {
		if got, err := cache.Get("ratelimit"); err != nil || got != fmt.Sprint(i) {
			t.Errorf("view %d Get = %q, %v; want %d", i, got, err, i)
		}
		it := cache.Find("", "")
		if !it.Next() || it.Key() != "ratelimit" || it.Value() != fmt.Sprint(i) || it.Next() {
			t.Errorf("view %d Find leaked other importer/account entries", i)
		}
		if err := it.Close(); err != nil {
			t.Fatal(err)
		}
		if err := cache.Close(); err != nil {
			t.Fatal(err)
		}
		if err := cache.Set("ratelimit", fmt.Sprint(i)); err != nil {
			t.Fatalf("closing view %d disabled cache: %v", i, err)
		}
	}
	if got := backing.closeCount(); got != 0 {
		t.Fatalf("closing scoped views closed the backing store %d times", got)
	}
	if got, err := requireCache(t, sibling.ImporterCache).Get("ratelimit"); err != nil || got != "0" {
		t.Errorf("sibling importer cache Get = %q, %v; want shared 0", got, err)
	}
}

func TestContextCacheNamespacesSurviveRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "importer-cache")
	h := configuredCacheHost(t, test.NewLoader(), jsonconfig.Obj{"cacheRoot": root})
	first, _ := cacheTestContexts(h, "dummy1", "first")
	if err := requireCache(t, first.ImporterCache).Set("ratelimit", "shared"); err != nil {
		t.Fatal(err)
	}
	if err := requireCache(t, first.AccountCache).Set("ratelimit", "private"); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := configuredCacheHost(t, test.NewLoader(), jsonconfig.Obj{"cacheRoot": root})
	same, _ := cacheTestContexts(reopened, "dummy1", "first")
	sibling, _ := cacheTestContexts(reopened, "dummy1", "second")
	other, _ := cacheTestContexts(reopened, "dummy2", "first")
	for _, cache := range []sorted.KeyValue{requireCache(t, same.ImporterCache), requireCache(t, sibling.ImporterCache)} {
		if got, err := cache.Get("ratelimit"); err != nil || got != "shared" {
			t.Errorf("restored importer cache Get = %q, %v; want shared", got, err)
		}
	}
	if got, err := requireCache(t, same.AccountCache).Get("ratelimit"); err != nil || got != "private" {
		t.Errorf("restored account cache Get = %q, %v; want private", got, err)
	}
	for _, cache := range []sorted.KeyValue{requireCache(t, sibling.AccountCache), requireCache(t, other.ImporterCache), requireCache(t, other.AccountCache)} {
		if _, err := cache.Get("ratelimit"); !errors.Is(err, sorted.ErrNotFound) {
			t.Errorf("different importer/account restored another scope's entry: %v", err)
		}
	}
}

func TestCacheNamespacesCannotOverlapViaNamesOrKeys(t *testing.T) {
	backing := sorted.NewMemoryKeyValue()
	defer backing.Close()
	h, err := NewHost(HostConfig{Cache: backing})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	im := &importer{host: h, name: "a"}
	other := &importer{host: h, name: "ab"}
	ia := &importerAcct{im: im, acct: &Object{pn: blob.RefFromString("namespace account")}}
	for _, key := range []string{"", "b:ratelimit", "\x00ratelimit", "\xff", "account:a:" + ia.acct.PermanodeRef().String() + ":ratelimit"} {
		if err := requireCache(t, im.cache).Set(key, "importer-private"); err != nil {
			t.Fatal(err)
		}
	}
	for _, cache := range []sorted.KeyValue{requireCache(t, other.cache), requireCache(t, ia.cache)} {
		it := cache.Find("", "")
		if it.Next() {
			t.Errorf("namespace leaked entry %q = %q", it.Key(), it.Value())
		}
		if err := it.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHostWithoutCacheRootUsesMemory(t *testing.T) {
	// No root must not fall back to any home/cache environment setting.
	home := filepath.Join(t.TempDir(), "not-created")
	for _, key := range []string{"HOME", "PERKEEP_CACHE_DIR", "CAMLI_CACHE_DIR", "XDG_CACHE_HOME"} {
		t.Setenv(key, home)
	}
	for _, typ := range []string{"", "leveldb", "sqlite", "kv"} {
		h := configuredCacheHost(t, test.NewLoader(), jsonconfig.Obj{"cacheType": typ})
		rc, _ := cacheTestContexts(h, "dummy1", "memory account")
		if err := requireCache(t, rc.AccountCache).Set("state", "memory"); err != nil {
			t.Fatal(err)
		}
		if h.cache != nil || h.cacheRoot != "" || len(h.cacheStores) != 1 {
			t.Fatal("Host without a root did not allocate a private memory store")
		}
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("memory-only caches accessed home/cache directories: %v", err)
	}
}

func TestHostDiskCacheLayoutPersistenceAndDeletion(t *testing.T) {
	for _, typ := range []string{"", "leveldb", "sqlite", "kv"} {
		t.Run("type="+typ, func(t *testing.T) {
			backend := typ
			if backend == "" {
				backend = sorted.DefaultKVFileType
			}
			root := filepath.Join(t.TempDir(), "importer-cache")
			newHost := func() *Host {
				return configuredCacheHost(t, test.NewLoader(), jsonconfig.Obj{"cacheRoot": root, "cacheType": typ})
			}
			h := newHost()
			if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unused host created cache directories: %v", err)
			}
			first, setup := cacheTestContexts(h, "dummy1", "first")
			sibling, _ := cacheTestContexts(h, "dummy1", "second")
			other, _ := cacheTestContexts(h, "dummy2", "first")
			shared := requireCache(t, first.ImporterCache)
			accountDir := filepath.Join(root, "dummy1-"+first.AccountNode().PermanodeRef().String())
			if _, err := os.Stat(accountDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("importer cache access created account cache: %v", err)
			}
			private := requireCache(t, first.AccountCache)
			if private != requireCache(t, setup.AccountCache) || shared != requireCache(t, sibling.ImporterCache) {
				t.Fatal("disk scope views are not stable across setup/run contexts")
			}
			views := []sorted.KeyValue{
				shared, private, requireCache(t, sibling.AccountCache), requireCache(t, other.ImporterCache),
			}
			for i, cache := range views {
				if err := cache.Set("state", fmt.Sprint(i)); err != nil {
					t.Fatal(err)
				}
				if err := cache.Close(); err != nil {
					t.Fatal(err)
				}
				if got, err := cache.Get("state"); err != nil || got != fmt.Sprint(i) {
					t.Errorf("view %d Get = %q, %v; want %d", i, got, err, i)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 4 {
				t.Fatalf("got %d scope directories; want 4", len(entries))
			}
			for _, dir := range []string{
				"dummy1", "dummy2",
				"dummy1-" + first.AccountNode().PermanodeRef().String(),
				"dummy1-" + sibling.AccountNode().PermanodeRef().String(),
			} {
				if _, err := os.Stat(filepath.Join(root, dir, "cache."+backend)); err != nil {
					t.Errorf("expected readable scope directory %s: %v", dir, err)
				}
			}
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := newHost()
			same, _ := cacheTestContexts(reopened, "dummy1", "first")
			next, _ := cacheTestContexts(reopened, "dummy1", "second")
			other, _ = cacheTestContexts(reopened, "dummy2", "first")
			for i, cache := range []sorted.KeyValue{
				requireCache(t, same.ImporterCache), requireCache(t, same.AccountCache),
				requireCache(t, next.AccountCache), requireCache(t, other.ImporterCache),
			} {
				if got, err := cache.Get("state"); err != nil || got != fmt.Sprint(i) {
					t.Errorf("reopened scope %d Get = %q, %v; want %d", i, got, err, i)
				}
			}
			if err := reopened.Close(); err != nil {
				t.Fatal(err)
			}
			// Only this account's disposable cache is removed, with all
			// stores closed first; importer-wide and sibling data survives.
			if err := os.RemoveAll(accountDir); err != nil {
				t.Fatal(err)
			}
			afterDeletion := newHost()
			same, _ = cacheTestContexts(afterDeletion, "dummy1", "first")
			next, _ = cacheTestContexts(afterDeletion, "dummy1", "second")
			if _, err := requireCache(t, same.AccountCache).Get("state"); !errors.Is(err, sorted.ErrNotFound) {
				t.Errorf("deleted account cache Get = %v; want ErrNotFound", err)
			}
			if got, err := requireCache(t, same.ImporterCache).Get("state"); err != nil || got != "0" {
				t.Errorf("account deletion changed importer cache = %q, %v", got, err)
			}
			if got, err := requireCache(t, next.AccountCache).Get("state"); err != nil || got != "2" {
				t.Errorf("account deletion changed sibling cache = %q, %v", got, err)
			}
		})
	}
}

func TestHostCacheOpenErrorsAreStable(t *testing.T) {
	for _, blockBackend := range []bool{false, true} {
		t.Run(fmt.Sprintf("backend=%v", blockBackend), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "cache-root")
			block := root
			if blockBackend {
				block = filepath.Join(root, "dummy1", "cache."+sorted.DefaultKVFileType)
				if err := os.MkdirAll(filepath.Dir(block), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(block, []byte("obstruct cache opening"), 0600); err != nil {
				t.Fatal(err)
			}
			h := configuredCacheHost(t, test.NewLoader(), jsonconfig.Obj{"cacheRoot": root})
			rc, setup := cacheTestContexts(h, "dummy1", "first")
			cache, err := rc.ImporterCache()
			if cache != nil || err == nil {
				t.Fatalf("open obstructed cache = %v, %v; want nil, error", cache, err)
			}
			if err := os.Remove(block); err != nil {
				t.Fatal(err)
			}
			if cache, again := setup.ImporterCache(); cache != nil || again != err {
				t.Errorf("same scope did not retain open error: %v, %v", cache, again)
			}
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := configuredCacheHost(t, test.NewLoader(), jsonconfig.Obj{"cacheRoot": root})
			rc, _ = cacheTestContexts(reopened, "dummy1", "first")
			requireCache(t, rc.ImporterCache)
		})
	}
}

func TestHostCacheAccessAfterClose(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(fmt.Sprintf("disk=%v", disk), func(t *testing.T) {
			hc := HostConfig{}
			if disk {
				hc.CacheRoot = filepath.Join(t.TempDir(), "importer-cache")
			}
			h, err := NewHost(hc)
			if err != nil {
				t.Fatal(err)
			}
			rc, setup := cacheTestContexts(h, "dummy1", "first")
			other, _ := cacheTestContexts(h, "dummy2", "unopened")
			requireCache(t, rc.ImporterCache)
			requireCache(t, rc.AccountCache)
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			for _, get := range []func() (sorted.KeyValue, error){
				rc.ImporterCache, rc.AccountCache, setup.ImporterCache, setup.AccountCache,
				other.ImporterCache, other.AccountCache,
			} {
				if cache, err := get(); cache != nil || !errors.Is(err, errCacheClosed) {
					t.Errorf("cache after Close = %v, %v; want nil, closed error", cache, err)
				}
			}
			if disk {
				if _, err := os.Stat(filepath.Join(hc.CacheRoot, "dummy2")); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("closed host created a new scope: %v", err)
				}
			}
		})
	}
}

func TestCacheDirectoryNamesAreInjectiveAndSafe(t *testing.T) {
	seen := make(map[string]string)
	for _, name := range []string{
		"strava", "dummy1", ".", "..", "../strava", "a/b", "a\\b", "a:b", "a-b",
		"a%2Db", "Strava", "STRAVA", "strava ", "strava.", "con", "CON", "aux",
		"com1", "lpt9", "%63on", "strava-sha224-" + strings.Repeat("a", 56),
		"\x00", "\xff", "日本語",
	} {
		dir, err := cacheImporterDirName(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(dir, `/\:-. `) || filepath.Base(dir) != dir {
			t.Errorf("name %q encoded to unsafe directory %q", name, dir)
		}
		// Case folding cannot make Windows/macOS directory names collide.
		folded := strings.ToLower(dir)
		if old, ok := seen[folded]; ok {
			t.Errorf("names %q and %q encoded to colliding directories", old, name)
		}
		seen[folded] = name
		if decoded, err := url.PathUnescape(dir); err != nil || decoded != name {
			t.Errorf("encoding is not reversible: %q -> %q -> %q, %v", name, dir, decoded, err)
		}
		if name == "strava" && dir != name {
			t.Errorf("normal importer name %q was not kept readable: %q", name, dir)
		}
	}
	if _, err := cacheImporterDirName(""); err == nil {
		t.Error("empty importer name accepted for disk cache")
	}
}

func TestHostCacheConcurrentGetters(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(fmt.Sprintf("disk=%v", disk), func(t *testing.T) {
			hc := HostConfig{}
			if disk {
				hc.CacheRoot = filepath.Join(t.TempDir(), "importer-cache")
			}
			h, err := NewHost(hc)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			rc, setup := cacheTestContexts(h, "dummy1", "first")
			type result struct {
				cache sorted.KeyValue
				err   error
			}
			results := make(chan result, 16)
			for i := range 16 {
				go func() {
					get := rc.AccountCache
					if i%2 == 0 {
						get = setup.AccountCache
					}
					cache, err := get()
					results <- result{cache, err}
				}()
			}
			var first sorted.KeyValue
			for range 16 {
				res := <-results
				if res.err != nil {
					t.Fatal(res.err)
				}
				if first == nil {
					first = res.cache
				}
				if res.cache != first {
					t.Error("concurrent getters returned different scope views")
				}
			}
			if len(h.cacheStores) != 1 {
				t.Errorf("concurrent getters opened %d stores; want 1", len(h.cacheStores))
			}
		})
	}
}

func TestDiskCacheScopeNamesCannotOverlap(t *testing.T) {
	root := filepath.Join(t.TempDir(), "importer-cache")
	h, err := NewHost(HostConfig{CacheRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	im := &importer{host: h, name: "strava"}
	ia := &importerAcct{im: im, acct: &Object{pn: blob.RefFromString("scope collision account")}}
	adversarial := &importer{host: h, name: "strava-" + ia.acct.PermanodeRef().String()}
	traversal := &importer{host: h, name: "../strava"}
	views := []sorted.KeyValue{
		requireCache(t, im.cache), requireCache(t, ia.cache),
		requireCache(t, adversarial.cache), requireCache(t, traversal.cache),
	}
	for i, cache := range views {
		if err := cache.Set("state", fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	for i, cache := range views {
		if got, err := cache.Get("state"); err != nil || got != fmt.Sprint(i) {
			t.Errorf("scope %d Get = %q, %v; want %d", i, got, err, i)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "strava-"+ia.acct.PermanodeRef().String(), "cache."+sorted.DefaultKVFileType)); err != nil {
		t.Errorf("expected readable Strava account cache path: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Errorf("got %d physical scopes; want 4", len(entries))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "strava")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("importer name escaped configured cache root: %v", err)
	}
}

func TestCancelledManagedRunCanOpenDiskCache(t *testing.T) {
	root := filepath.Join(t.TempDir(), "importer-cache")
	impl := &cacheBlockingImporter{
		entered: make(chan struct{}), cancelled: make(chan error, 1), release: make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(impl.release) })
	defer release()
	h, err := NewHost(HostConfig{CacheRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	ia := &importerAcct{
		im:   &importer{host: h, name: "dummy1", impl: impl},
		acct: &Object{pn: blob.RefFromString("cancelled disk cache account")},
	}
	ia.start()
	select {
	case <-impl.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("importer did not start")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("importer opened cache before accessing it: %v", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- h.Close() }()
	select {
	case err := <-impl.cancelled:
		if err != nil {
			t.Fatalf("cancelled run could not open/write its first cache: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel importer")
	}
	release()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish")
	}
	reopened := configuredCacheHost(t, test.NewLoader(), jsonconfig.Obj{"cacheRoot": root})
	rc, _ := cacheTestContexts(reopened, "dummy1", "cancelled disk cache account")
	if got, err := requireCache(t, rc.ImporterCache).Get("test:final"); err != nil || got != "saved before close" {
		t.Errorf("cancelled run's final state = %q, %v; want saved before close", got, err)
	}
}

func TestHostCloseSerializesWithCacheOpening(t *testing.T) {
	cache := &closeTrackingCache{KeyValue: sorted.NewMemoryKeyValue(), closed: make(chan struct{})}
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	typ := fmt.Sprintf("importer-cache-test-%d", cacheTestConstructorID.Add(1))
	sorted.RegisterKeyValue(typ, func(cfg jsonconfig.Obj) (sorted.KeyValue, error) {
		cfg.RequiredString("file")
		close(entered)
		<-release
		return cache, nil
	})
	h, err := NewHost(HostConfig{CacheRoot: filepath.Join(t.TempDir(), "importer-cache")})
	if err != nil {
		t.Fatal(err)
	}
	// Substitute a blocking constructor to exercise host opening/close
	// synchronization independently of real backend scheduling.
	h.cacheType = typ
	t.Cleanup(func() { h.Close() })
	rc, _ := cacheTestContexts(h, "dummy1", "first")
	opened := make(chan error, 1)
	go func() {
		_, err := rc.ImporterCache()
		opened <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scope opening did not enter backend constructor")
	}
	timeoutErr := closeCacheHostWithShortTimeout(t, h)
	if got := cache.closeCount(); got != 0 {
		t.Errorf("cache closed %d times before constructor returned", got)
	}
	unblock()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scope opening did not finish")
	}
	select {
	case <-cache.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("newly opened cache was not closed after shutdown")
	}
	if got := cache.closeCount(); got != 1 {
		t.Errorf("newly opened cache closed %d times; want 1", got)
	}
	if cache, err := rc.AccountCache(); cache != nil || !errors.Is(err, errCacheClosed) {
		t.Errorf("new scope after close phase = %v, %v; want closed error", cache, err)
	}
	if err := h.Close(); err != timeoutErr {
		t.Errorf("Close after cleanup = %v; want original timeout %v", err, timeoutErr)
	}
}

func TestHostCloseClosesAllOwnedStores(t *testing.T) {
	callerOwned := &closeTrackingCache{KeyValue: sorted.NewMemoryKeyValue()}
	h, err := NewHost(HostConfig{Cache: callerOwned})
	if err != nil {
		t.Fatal(err)
	}
	firstErr := errors.New("first owned cache close failed")
	first := &closeTrackingCache{KeyValue: sorted.NewMemoryKeyValue(), closeErr: firstErr}
	second := &closeTrackingCache{KeyValue: sorted.NewMemoryKeyValue()}
	h.cacheStores = append(h.cacheStores, first, second)
	if err := h.Close(); !errors.Is(err, firstErr) {
		t.Errorf("Close = %v; want %v", err, firstErr)
	}
	for range 2 {
		if err := h.Close(); !errors.Is(err, firstErr) {
			t.Errorf("repeated Close = %v; want %v", err, firstErr)
		}
	}
	if got := first.closeCount(); got != 1 {
		t.Errorf("first owned cache closed %d times; want 1", got)
	}
	if got := second.closeCount(); got != 1 {
		t.Errorf("second owned cache closed %d times; want 1", got)
	}
	if got := callerOwned.closeCount(); got != 0 {
		t.Errorf("caller-owned cache closed %d times; want 0", got)
	}
}

func TestAccountCacheRequiresValidPermanode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	h, err := NewHost(HostConfig{CacheRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ia := &importerAcct{im: h.imp["dummy1"], acct: &Object{}}
	rc := &RunContext{Host: h, ia: ia}
	setup := &SetupContext{Host: h, ia: ia}
	if cache, err := rc.AccountCache(); cache != nil || err == nil {
		t.Errorf("account cache without valid permanode = %v, %v; want nil, error", cache, err)
	}
	if cache, err := setup.AccountCache(); cache != nil || err == nil {
		t.Errorf("setup account cache without valid permanode = %v, %v; want nil, error", cache, err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("invalid account cache created directories: %v", err)
	}
}
