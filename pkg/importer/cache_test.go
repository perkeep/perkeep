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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/sorted"
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
	if first.Cache() == nil || second.Cache() == nil {
		t.Fatal("NewHost returned a nil cache")
	}
	if err := first.Cache().Set("test:state", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Cache().Get("test:state"); !errors.Is(err, sorted.ErrNotFound) {
		t.Errorf("second host Get = %v; want ErrNotFound", err)
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
	if h.Cache() != cache {
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

type cacheLoader struct {
	blobserver.Loader
	baseURL, prefix string
}

func (ld cacheLoader) BaseURL() string  { return ld.baseURL }
func (ld cacheLoader) MyPrefix() string { return ld.prefix }

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

func TestHostDefaultCachePersistenceAndIsolation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PERKEEP_CACHE_DIR", dir)
	ld := cacheLoader{test.NewLoader(), "https://example.test", "/importer/"}
	first := configuredCacheHost(t, ld, jsonconfig.Obj{})
	if err := first.Cache().Set("test:state", "saved"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := configuredCacheHost(t, ld, jsonconfig.Obj{})
	if got, err := reopened.Cache().Get("test:state"); err != nil || got != "saved" {
		t.Fatalf("reopened Get = %q, %v; want saved", got, err)
	}
	for _, other := range []cacheLoader{
		{test.NewLoader(), ld.baseURL, "/other-importer/"},
		{test.NewLoader(), "https://other.example.test", ld.prefix},
		// These concatenate to the same importer URL, but are different
		// baseURL/prefix pairs and must not share a cache.
		{test.NewLoader(), "https://example.test/a", "/b/"},
		{test.NewLoader(), "https://example.test", "/a/b/"},
	} {
		host := configuredCacheHost(t, other, jsonconfig.Obj{})
		if _, err := host.Cache().Get("test:state"); !errors.Is(err, sorted.ErrNotFound) {
			t.Errorf("%s + %s Get = %v; want ErrNotFound", other.baseURL, other.prefix, err)
		}
		if err := host.Cache().Set("test:state", "isolated"); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "importer"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Errorf("got %d default cache stores; want 5", len(entries))
	}
}

func TestHostConfiguredCachePersistence(t *testing.T) {
	file := filepath.Join(t.TempDir(), "custom-cache."+sorted.DefaultKVFileType)
	config := func() jsonconfig.Obj {
		return jsonconfig.Obj{"cache": map[string]any{
			"type": sorted.DefaultKVFileType, "file": file,
		}}
	}
	first := configuredCacheHost(t, test.NewLoader(), config())
	if err := first.Cache().Set("test:state", "configured"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := configuredCacheHost(t, test.NewLoader(), config())
	if got, err := reopened.Cache().Get("test:state"); err != nil || got != "configured" {
		t.Fatalf("reopened Get = %q, %v; want configured", got, err)
	}
}

func TestHostCacheConfigErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "cache."+sorted.DefaultKVFileType)
	for _, cfg := range []jsonconfig.Obj{
		{"cache": "not an object"},
		{"cache": map[string]any{}},
		{"cache": map[string]any{"type": "no-such-importer-cache"}},
		{"cache": map[string]any{"type": sorted.DefaultKVFileType}},
		{"cache": map[string]any{"type": sorted.DefaultKVFileType, "file": file, "bogus": true}},
		{
			"cache":  map[string]any{"type": sorted.DefaultKVFileType, "file": file},
			"dummy1": map[string]any{"clientSecret": "secret-without-client-id"},
		},
	} {
		if _, err := newFromConfig(test.NewLoader(), cfg); err == nil {
			t.Errorf("newFromConfig(%v) succeeded; want error", cfg)
		}
	}
	// In particular, NewHost configuration failure must release the file
	// opened by newFromConfig, so it can immediately be opened again.
	configuredCacheHost(t, test.NewLoader(), jsonconfig.Obj{"cache": map[string]any{
		"type": sorted.DefaultKVFileType, "file": file,
	}})
}

var cacheTestConstructorID atomic.Uint64

func TestHostCacheConstructorErrorClosesStore(t *testing.T) {
	for _, needWipe := range []bool{false, true} {
		cache := &closeTrackingCache{KeyValue: sorted.NewMemoryKeyValue()}
		typ := fmt.Sprintf("importer-cache-test-%d", cacheTestConstructorID.Add(1))
		sorted.RegisterKeyValue(typ, func(jsonconfig.Obj) (sorted.KeyValue, error) {
			if needWipe {
				return cache, sorted.NeedWipeError{Msg: "test cache needs repair"}
			}
			// Return an open store and leave the unknown key for the
			// registry's final configuration validation to reject.
			return cache, nil
		})
		cfg := jsonconfig.Obj{"type": typ}
		if !needWipe {
			cfg["unknown"] = true
		}
		_, err := newCache("", "", cfg)
		if err == nil {
			t.Fatal("newCache succeeded; want configuration error")
		}
		if got := cache.closeCount(); got != 1 {
			t.Errorf("newCache closed failed store %d times; want 1", got)
		}
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
	im.cancelled <- rc.Host.Cache().Set("test:final", "saved before close")
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
			h.ownsCache = true
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
	err := rc.Host.Cache().Set("test:final", "saved after shutdown timeout")
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
	h.ownsCache = true
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
	h.ownsCache = true
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
