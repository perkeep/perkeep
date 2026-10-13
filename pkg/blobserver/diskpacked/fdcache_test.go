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

package diskpacked

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func fdCacheTestNew(t *testing.T, maxOpen int) *fdCache {
	t.Helper()
	c := newFDCache(t.TempDir(), maxOpen)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Close(ctx); err != nil {
			t.Errorf("fdCache.Close during cleanup: %v", err)
		}
	})
	return c
}

func fdCacheTestWrite(t *testing.T, c *fdCache, name, data string) string {
	t.Helper()
	path := filepath.Join(c.root, name)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fdCacheTestEntries(t *testing.T, c *fdCache, paths ...string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) != len(paths) {
		t.Fatalf("cached entries = %d; want %d (entries: %v)", len(c.entries), len(paths), c.entries)
	}
	if c.queue.Len() != len(c.entries) {
		t.Fatalf("queue length = %d; entries = %d", c.queue.Len(), len(c.entries))
	}
	for _, path := range paths {
		if c.entries[path] == nil {
			t.Errorf("%q not in cache", path)
		}
	}
}

// observedContext reports when OpenSection or open enters its capacity-wait
// select (both methods call Done only at that point). This avoids sleeps and
// makes the waiting/cancellation tests independent of goroutine scheduling.
type fdCacheTestObservedContext struct {
	context.Context
	enteredWait chan struct{}
	once        sync.Once
}

func (c *fdCacheTestObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.enteredWait) })
	return c.Context.Done()
}

func TestFDCacheInvalidCapacity(t *testing.T) {
	for _, capacity := range []int{0, -1} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("newFDCache did not panic for nonpositive maxOpen")
				}
			}()
			newFDCache(t.TempDir(), capacity)
		})
	}
}

func TestFDCacheOpenSectionValidation(t *testing.T) {
	c := fdCacheTestNew(t, 1)
	path := fdCacheTestWrite(t, c, "a", "abcdef")

	for _, tc := range []struct {
		name        string
		off, length int64
	}{
		{"negative offset", -1, 1},
		{"negative length", 0, -1},
		{"both negative", -1, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := c.OpenSection(t.Context(), path, tc.off, tc.length)
			if err == nil || r != nil {
				t.Fatalf("OpenSection(%d, %d) = (%v, %v); want (nil, error)", tc.off, tc.length, r, err)
			}
		})
	}
	fdCacheTestEntries(t, c)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.OpenSection(ctx, path, 0, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("OpenSection with canceled context: got %v; want context.Canceled", err)
	}
	if err := c.open(ctx, path); !errors.Is(err, context.Canceled) {
		t.Errorf("open with canceled context: got %v; want context.Canceled", err)
	}
	fdCacheTestEntries(t, c)
}

func TestFDCacheOpenSectionReadSeekAndReuse(t *testing.T) {
	c := fdCacheTestNew(t, 2)
	path := fdCacheTestWrite(t, c, "a", "abcdefghij")
	ctx := t.Context()

	first, err := c.OpenSection(ctx, path, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := c.OpenSection(ctx, path, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	if first.entry != second.entry {
		t.Error("cache hit opened a second entry for the same file")
	}
	c.mu.Lock()
	refs, visited, queueLen := first.entry.refs, first.entry.visited, c.queue.Len()
	c.mu.Unlock()
	if refs != 2 || !visited || queueLen != 1 {
		t.Errorf("after cache hit: refs=%d, visited=%v, queue length=%d; want 2, true, 1", refs, visited, queueLen)
	}

	buf := make([]byte, 2)
	if _, err := io.ReadFull(first, buf); err != nil || string(buf) != "cd" {
		t.Fatalf("first section initial read = %q, %v; want cd", buf, err)
	}
	if pos, err := first.Seek(1, io.SeekStart); err != nil || pos != 1 {
		t.Fatalf("Seek(1, SeekStart) = (%d, %v); want (1, nil)", pos, err)
	}
	if got, err := io.ReadAll(first); err != nil || string(got) != "defg" {
		t.Errorf("first section remaining data = %q, %v; want defg", got, err)
	}
	if got, err := io.ReadAll(second); err != nil || string(got) != "abcdefghij" {
		t.Errorf("second section data = %q, %v", got, err)
	}

	c.mu.Lock()
	refs = first.entry.refs
	c.mu.Unlock()
	if refs != 0 {
		t.Errorf("references after EOF = %d; want 0", refs)
	}
	if err := first.Close(); err != nil {
		t.Errorf("Close after EOF: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Errorf("repeated Close: %v", err)
	}
	fdCacheTestEntries(t, c, path) // idle files remain cached
}

func TestFDCacheEOFReleasesLease(t *testing.T) {
	for _, tc := range []struct {
		name   string
		off    int64
		length int64
		want   string
	}{
		{"section boundary", 1, 3, "bcd"},
		{"underlying file EOF", 2, 100, "cdef"},
		{"empty section", 0, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fdCacheTestNew(t, 1)
			firstPath := fdCacheTestWrite(t, c, "first", "abcdef")
			secondPath := fdCacheTestWrite(t, c, "second", "second")

			r, err := c.OpenSection(t.Context(), firstPath, tc.off, tc.length)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			data, err := io.ReadAll(r)
			if err != nil || string(data) != tc.want {
				t.Fatalf("ReadAll = %q, %v; want %q, nil", data, err, tc.want)
			}

			c.mu.Lock()
			refs := r.entry.refs
			c.mu.Unlock()
			if refs != 0 {
				t.Fatalf("refs after EOF = %d; want 0", refs)
			}
			// No explicit Section.Close was needed for eviction.
			if err := c.open(t.Context(), secondPath); err != nil {
				t.Fatalf("open after EOF: %v", err)
			}
			fdCacheTestEntries(t, c, secondPath)
			if err := r.Close(); err != nil {
				t.Errorf("Close after eviction: %v", err)
			}
			if err := r.Close(); err != nil {
				t.Errorf("repeated Close after eviction: %v", err)
			}
		})
	}
}

func TestFDCacheReadAfterEOFEviction(t *testing.T) {
	c := fdCacheTestNew(t, 1)
	firstPath := fdCacheTestWrite(t, c, "first", "abcdef")
	secondPath := fdCacheTestWrite(t, c, "second", "second")

	r, err := c.OpenSection(t.Context(), firstPath, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if got, err := io.ReadAll(r); err != nil || string(got) != "bcde" {
		t.Fatalf("ReadAll = %q, %v; want bcde", got, err)
	}
	// The lease was released at EOF, so this evicts firstPath.
	if err := c.open(t.Context(), secondPath); err != nil {
		t.Fatal(err)
	}
	fdCacheTestEntries(t, c, secondPath)

	if _, err := r.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(r); err != nil || string(got) != "bcde" {
		t.Errorf("ReadAll after Seek = %q, %v; want bcde", got, err)
	}
	fdCacheTestEntries(t, c, firstPath)

	// EOF released the new lease too; evict firstPath again.
	if err := c.open(t.Context(), secondPath); err != nil {
		t.Fatal(err)
	}
	fdCacheTestEntries(t, c, secondPath)
	buf := make([]byte, 2)
	if n, err := r.ReadAt(buf, 2); err != nil || string(buf[:n]) != "de" {
		t.Errorf("ReadAt after eviction = %q, %v; want de", buf[:n], err)
	}
	fdCacheTestEntries(t, c, firstPath)

	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	c.mu.Lock()
	refs := r.entry.refs
	c.mu.Unlock()
	if refs != 0 {
		t.Errorf("refs after Close = %d; want 0", refs)
	}
	if _, err := r.ReadAt(buf, 0); !errors.Is(err, os.ErrClosed) {
		t.Errorf("ReadAt after Close = %v; want os.ErrClosed", err)
	}
}

func TestFDCacheEagerOpen(t *testing.T) {
	c := fdCacheTestNew(t, 1)
	path := fdCacheTestWrite(t, c, "a", "data")
	ctx := t.Context()

	if err := c.open(ctx, path); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	e := c.entries[path]
	c.mu.Unlock()
	if err := c.open(ctx, path); err != nil {
		t.Fatal(err)
	}
	fdCacheTestEntries(t, c, path)
	c.mu.Lock()
	if c.entries[path] != e || e.refs != 0 || e.visited {
		t.Errorf("repeated eager open changed cached entry: refs=%d, visited=%v", e.refs, e.visited)
	}
	c.mu.Unlock()

	r, err := c.OpenSection(ctx, path, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.entry != e {
		t.Error("OpenSection did not reuse eagerly opened entry")
	}
	if got, err := io.ReadAll(r); err != nil || string(got) != "data" {
		t.Errorf("ReadAll = %q, %v; want data", got, err)
	}
}

func TestFDCacheOpenFailures(t *testing.T) {
	c := fdCacheTestNew(t, 1)
	path := filepath.Join(c.root, "missing")

	if _, err := c.OpenSection(t.Context(), path, 0, 1); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("OpenSection missing file = %v; want os.ErrNotExist", err)
	}
	if err := c.open(t.Context(), path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("open missing file = %v; want os.ErrNotExist", err)
	}
	fdCacheTestEntries(t, c)

	if err := os.WriteFile(path, []byte("created"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.open(t.Context(), path); err != nil {
		t.Errorf("retry open after creating file: %v", err)
	}
	fdCacheTestEntries(t, c, path)
}

func TestFDCacheSIEVESecondChance(t *testing.T) {
	c := fdCacheTestNew(t, 3)
	a := fdCacheTestWrite(t, c, "a", "a")
	b := fdCacheTestWrite(t, c, "b", "b")
	third := fdCacheTestWrite(t, c, "c", "c")
	d := fdCacheTestWrite(t, c, "d", "d")
	e := fdCacheTestWrite(t, c, "e", "e")
	ctx := t.Context()

	for _, path := range []string{a, b, third} {
		if err := c.open(ctx, path); err != nil {
			t.Fatal(err)
		}
	}
	// Mark the oldest entry as visited. A SIEVE hit must not move its node.
	r, err := c.OpenSection(ctx, a, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	newest, oldest := c.queue.Front().Value.(*entry).path, c.queue.Back().Value.(*entry).path
	c.mu.Unlock()
	if newest != third || oldest != a {
		t.Fatalf("cache hit reordered queue: newest=%q, oldest=%q", newest, oldest)
	}

	// The hand skips visited a (clearing its bit), then evicts unvisited b.
	if err := c.open(ctx, d); err != nil {
		t.Fatal(err)
	}
	fdCacheTestEntries(t, c, a, third, d)
	c.mu.Lock()
	visited := c.entries[a].visited
	c.mu.Unlock()
	if visited {
		t.Error("SIEVE did not clear the visited bit when granting a second chance")
	}

	// The hand continues from c, rather than restarting at the oldest.
	if err := c.open(ctx, e); err != nil {
		t.Fatal(err)
	}
	fdCacheTestEntries(t, c, a, d, e)
}

func TestFDCacheSIEVEAllVisited(t *testing.T) {
	c := fdCacheTestNew(t, 2)
	a := fdCacheTestWrite(t, c, "a", "a")
	b := fdCacheTestWrite(t, c, "b", "b")
	third := fdCacheTestWrite(t, c, "c", "c")
	ctx := t.Context()

	for _, path := range []string{a, b} {
		if err := c.open(ctx, path); err != nil {
			t.Fatal(err)
		}
		r, err := c.OpenSection(ctx, path, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// Both entries need a second chance; the second pass must make progress.
	if err := c.open(ctx, third); err != nil {
		t.Fatal(err)
	}
	fdCacheTestEntries(t, c, b, third) // a was oldest
}

func TestFDCacheEvictionSkipsPinnedEntries(t *testing.T) {
	c := fdCacheTestNew(t, 2)
	a := fdCacheTestWrite(t, c, "a", "active")
	b := fdCacheTestWrite(t, c, "b", "idle")
	third := fdCacheTestWrite(t, c, "c", "new")
	ctx := t.Context()

	active, err := c.OpenSection(ctx, a, 0, 6)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	if err := c.open(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := c.open(ctx, third); err != nil {
		t.Fatal(err)
	}
	fdCacheTestEntries(t, c, a, third)
	if got, err := io.ReadAll(active); err != nil || string(got) != "active" {
		t.Errorf("pinned reader after eviction = %q, %v", got, err)
	}
}

func TestFDCacheWaitsForCapacity(t *testing.T) {
	for _, method := range []string{"OpenSection", "open"} {
		t.Run(method, func(t *testing.T) {
			c := fdCacheTestNew(t, 1)
			a := fdCacheTestWrite(t, c, "a", "a")
			b := fdCacheTestWrite(t, c, "b", "b")
			active, err := c.OpenSection(t.Context(), a, 0, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer active.Close()

			base, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			ctx := &fdCacheTestObservedContext{Context: base, enteredWait: make(chan struct{})}
			result := make(chan error, 1)
			go func() {
				if method == "open" {
					result <- c.open(ctx, b)
					return
				}
				r, err := c.OpenSection(ctx, b, 0, 1)
				if r != nil {
					err = errors.Join(err, r.Close())
				}
				result <- err
			}()

			select {
			case <-ctx.enteredWait:
				// The request has reached the capacity-wait branch.
			case err := <-result:
				t.Fatalf("request returned without waiting for capacity: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("request never entered capacity wait")
			}
			select {
			case err := <-result:
				t.Fatalf("request completed while all slots were pinned: %v", err)
			default:
			}

			if err := active.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatalf("request after release: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("request did not wake after releasing a pinned entry")
			}
			fdCacheTestEntries(t, c, b)
		})
	}
}

func TestFDCacheWaitCancellation(t *testing.T) {
	for _, method := range []string{"OpenSection", "open"} {
		t.Run(method, func(t *testing.T) {
			c := fdCacheTestNew(t, 1)
			a := fdCacheTestWrite(t, c, "a", "a")
			b := fdCacheTestWrite(t, c, "b", "b")
			active, err := c.OpenSection(t.Context(), a, 0, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer active.Close()

			base, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx := &fdCacheTestObservedContext{Context: base, enteredWait: make(chan struct{})}
			result := make(chan error, 1)
			go func() {
				if method == "open" {
					result <- c.open(ctx, b)
					return
				}
				r, err := c.OpenSection(ctx, b, 0, 1)
				if r != nil {
					err = errors.Join(err, r.Close())
				}
				result <- err
			}()
			select {
			case <-ctx.enteredWait:
			case err := <-result:
				t.Fatalf("request returned before cancellation: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("request never entered capacity wait")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("waiting request returned %v; want context.Canceled", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("canceled request did not wake")
			}
			fdCacheTestEntries(t, c, a)
		})
	}
}

func TestFDCacheCloseWaitsForLeases(t *testing.T) {
	c := fdCacheTestNew(t, 2)
	a := fdCacheTestWrite(t, c, "a", "active")
	b := fdCacheTestWrite(t, c, "b", "idle")
	ctx := t.Context()

	active, err := c.OpenSection(ctx, a, 0, 6)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	if err := c.open(ctx, b); err != nil {
		t.Fatal(err)
	}

	c.mu.Lock()
	activeFile := c.entries[a].file
	idleFile := c.entries[b].file
	closingStarted := c.changed
	c.mu.Unlock()

	result := make(chan error, 1)
	go func() { result <- c.Close(context.Background()) }()
	select {
	case <-closingStarted: // Closing immediately removes idle entries.
	case <-time.After(5 * time.Second):
		t.Fatal("Close never started")
	}
	fdCacheTestEntries(t, c, a)
	fdCacheTestAssertClosed(t, idleFile, "idle file not closed during shutdown")
	if _, err := activeFile.Stat(); err != nil {
		t.Errorf("active file was closed before lease release: %v", err)
	}
	if _, err := c.OpenSection(ctx, b, 0, 1); !errors.Is(err, os.ErrClosed) {
		t.Errorf("OpenSection after shutdown = %v; want os.ErrClosed", err)
	}
	if err := c.open(ctx, b); !errors.Is(err, os.ErrClosed) {
		t.Errorf("open after shutdown = %v; want os.ErrClosed", err)
	}
	select {
	case err := <-result:
		t.Fatalf("Close returned with an outstanding lease: %v", err)
	default:
	}

	// The existing reader remains usable while Close waits.
	if got, err := io.ReadAll(active); err != nil || string(got) != "active" {
		t.Errorf("read during shutdown = %q, %v", got, err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Errorf("Close after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish after last lease released")
	}
	fdCacheTestAssertClosed(t, activeFile, "active file still open after shutdown")
	fdCacheTestEntries(t, c)
	if err := c.Close(ctx); err != nil {
		t.Errorf("repeated cache Close: %v", err)
	}
}

func TestFDCacheCloseCanceledLeavesActiveReaderOpen(t *testing.T) {
	c := fdCacheTestNew(t, 1)
	path := fdCacheTestWrite(t, c, "a", "active")
	active, err := c.OpenSection(t.Context(), path, 0, 6)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	file := active.entry.file

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close with outstanding lease and canceled context = %v; want context.Canceled", err)
	}
	fdCacheTestEntries(t, c, path)
	if _, err := file.Stat(); err != nil {
		t.Errorf("Close canceled but active file was closed: %v", err)
	}
	if _, err := c.OpenSection(t.Context(), path, 0, 1); !errors.Is(err, os.ErrClosed) {
		t.Errorf("OpenSection after canceled Close = %v; want os.ErrClosed", err)
	}
	if got, err := io.ReadAll(active); err != nil || string(got) != "active" {
		t.Errorf("outstanding reader after canceled Close = %q, %v", got, err)
	}
	if err := active.Close(); err != nil {
		t.Errorf("Section.Close after canceled Close: %v", err)
	}
	fdCacheTestAssertClosed(t, file, "last release did not close file")
	fdCacheTestEntries(t, c)
	if err := c.Close(context.Background()); err != nil {
		t.Errorf("repeated Close: %v", err)
	}
}

func TestFDCacheClosingWakesCapacityWaiters(t *testing.T) {
	c := fdCacheTestNew(t, 1)
	a := fdCacheTestWrite(t, c, "a", "a")
	b := fdCacheTestWrite(t, c, "b", "b")
	active, err := c.OpenSection(t.Context(), a, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()

	base, cancelWait := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelWait()
	waitingCtx := &fdCacheTestObservedContext{Context: base, enteredWait: make(chan struct{})}
	waiter := make(chan error, 1)
	go func() {
		r, err := c.OpenSection(waitingCtx, b, 0, 1)
		if r != nil {
			err = errors.Join(err, r.Close())
		}
		waiter <- err
	}()
	select {
	case <-waitingCtx.enteredWait:
	case <-time.After(5 * time.Second):
		t.Fatal("request never entered capacity wait")
	}

	closeCtx, cancelClose := context.WithCancel(t.Context())
	cancelClose()
	if err := c.Close(closeCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close while lease is held = %v; want context.Canceled", err)
	}
	select {
	case err := <-waiter:
		if !errors.Is(err, os.ErrClosed) {
			t.Errorf("waiting OpenSection after shutdown = %v; want os.ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiting OpenSection was not woken by shutdown")
	}
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFDCacheConcurrentSamePath(t *testing.T) {
	c := fdCacheTestNew(t, 1)
	path := fdCacheTestWrite(t, c, "same", "content")
	const readers = 24
	type result struct {
		section *sectionReader
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, readers)
	for i := 0; i < readers; i++ {
		go func() {
			<-start
			r, err := c.OpenSection(t.Context(), path, 0, 7)
			results <- result{r, err}
		}()
	}
	close(start)

	sections := make([]*sectionReader, 0, readers)
	for i := 0; i < readers; i++ {
		got := <-results
		if got.err != nil {
			t.Errorf("concurrent OpenSection: %v", got.err)
		}
		if got.section != nil {
			sections = append(sections, got.section)
		}
	}
	defer func() {
		for _, r := range sections {
			r.Close()
		}
	}()
	if len(sections) != readers {
		t.Fatalf("got %d readers; want %d", len(sections), readers)
	}
	c.mu.Lock()
	refs, count, queueLen := c.entries[path].refs, len(c.entries), c.queue.Len()
	c.mu.Unlock()
	if refs != readers || count != 1 || queueLen != 1 {
		t.Errorf("concurrent opens: refs=%d, entries=%d, queue=%d; want %d, 1, 1", refs, count, queueLen, readers)
	}
	for _, r := range sections {
		if err := r.Close(); err != nil {
			t.Errorf("reader Close: %v", err)
		}
	}
	c.mu.Lock()
	refs = c.entries[path].refs
	c.mu.Unlock()
	if refs != 0 {
		t.Errorf("refs after all readers closed = %d; want 0", refs)
	}
}

func TestFDCacheConcurrentAccess(t *testing.T) {
	c := fdCacheTestNew(t, 2)
	contents := []string{"file zero", "file one", "file two", "file three"}
	paths := make([]string, len(contents))
	for i, s := range contents {
		paths[i] = fdCacheTestWrite(t, c, fmt.Sprintf("file-%d", i), s)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	const workers, iterations = 12, 30
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				index := (worker + i) % len(paths)
				r, err := c.OpenSection(ctx, paths[index], 0, int64(len(contents[index])))
				if err != nil {
					errs <- fmt.Errorf("worker %d: OpenSection: %w", worker, err)
					return
				}
				data, readErr := io.ReadAll(r)
				closeErr := r.Close()
				if readErr != nil || closeErr != nil || string(data) != contents[index] {
					errs <- fmt.Errorf("worker %d: data=%q, read=%v, close=%v", worker, data, readErr, closeErr)
					return
				}
				c.mu.Lock()
				count := len(c.entries)
				c.mu.Unlock()
				if count > c.maxOpen {
					errs <- fmt.Errorf("worker %d: %d open entries; limit %d", worker, count, c.maxOpen)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.refs != 0 {
			t.Errorf("entry %q leaked %d leases", e.path, e.refs)
		}
	}
}

func TestFDCacheCloseErrorIsPreserved(t *testing.T) {
	// Simulate a Close failure by closing the underlying file behind the
	// cache's back. The error must propagate, including on a repeated Close.
	c := newFDCache(t.TempDir(), 1)
	path := fdCacheTestWrite(t, c, "a", "a")
	if err := c.open(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	f := c.entries[path].file
	c.mu.Unlock()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := c.Close(context.Background()); !errors.Is(err, os.ErrClosed) {
			t.Errorf("Close #%d = %v; want os.ErrClosed", i+1, err)
		}
	}
	fdCacheTestEntries(t, c)
}

func TestFDCacheEvictionCloseError(t *testing.T) {
	for _, method := range []string{"OpenSection", "open"} {
		t.Run(method, func(t *testing.T) {
			c := fdCacheTestNew(t, 1)
			a := fdCacheTestWrite(t, c, "a", "a")
			b := fdCacheTestWrite(t, c, "b", "b")
			ctx := t.Context()
			if err := c.open(ctx, a); err != nil {
				t.Fatal(err)
			}
			c.mu.Lock()
			f := c.entries[a].file
			c.mu.Unlock()
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}

			var err error
			if method == "OpenSection" {
				_, err = c.OpenSection(ctx, b, 0, 1)
			} else {
				err = c.open(ctx, b)
			}
			if !errors.Is(err, os.ErrClosed) {
				t.Errorf("eviction error = %v; want wrapped os.ErrClosed", err)
			}
			fdCacheTestEntries(t, c) // Failed eviction removes the stale entry.
			if err := c.open(ctx, b); err != nil {
				t.Errorf("cache not usable after failed eviction: %v", err)
			}
		})
	}
}

func TestFDCacheCloseCancellationWhileWaiting(t *testing.T) {
	c := fdCacheTestNew(t, 1)
	path := fdCacheTestWrite(t, c, "a", "a")
	active, err := c.OpenSection(t.Context(), path, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()

	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &fdCacheTestObservedContext{Context: base, enteredWait: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- c.Close(ctx) }()
	select {
	case <-ctx.enteredWait:
		// Close has started waiting for the active reader to finish.
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not wait for outstanding lease")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Close after cancellation = %v; want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not respond to cancellation")
	}
	// Cancellation must not invalidate the outstanding lease.
	if _, err := active.entry.file.Stat(); err != nil {
		t.Errorf("active FD unexpectedly closed: %v", err)
	}
	if err := active.Close(); err != nil {
		t.Error(err)
	}
	fdCacheTestEntries(t, c)
}

// Test whether the file is handle os closed
//
// On Windows, calling Stat() on a closed *os.File can return:
//
//	GetFileType ...: The handle is invalid.
//
// This does not satisfy errors.Is(err, os.ErrClosed), even though the file is closed.
// The portable fix is to use ReadAt() when checking whether the file has been closed:
func fdCacheTestAssertClosed(t *testing.T, f *os.File, msg string) {
	t.Helper()

	var buf [1]byte
	_, err := f.ReadAt(buf[:], 0)
	if !errors.Is(err, os.ErrClosed) {
		if msg == "" {
			msg = "file is not closed"
		}
		t.Errorf("%s: %v", msg, err)
	}
}
