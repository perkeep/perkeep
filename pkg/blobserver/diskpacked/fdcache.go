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
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// fdCache bounds the number of open files it owns.
// It uses SIEVE for eviction and pins active leases.
type fdCache struct {
	root     string
	mu       sync.Mutex
	entries  map[string]*entry
	queue    list.List     // front: newest, back: oldest
	hand     *list.Element // next candidate; nil means oldest
	maxOpen  int
	changed  chan struct{}
	closing  bool
	closeErr error
}

type entry struct {
	path    string
	file    *os.File
	refs    int
	visited bool
	node    *list.Element
}

// sectionReader is a section of a cached file, read under a lease.
// The lease is released at io.EOF or Close. A later read
// (after a Seek, or via ReadAt) takes a new lease, reopening
// the file if it was evicted in the meantime.
type sectionReader struct {
	*io.SectionReader // reads via (*sectionFile)(s)
	cache             *fdCache
	path              string
	ctx               context.Context // for waits when taking a new lease

	mu       sync.Mutex // guards the fields below
	entry    *entry     // most recently leased entry
	leased   bool       // whether a lease on entry is held
	closed   bool
	closeErr error
}

// sectionFile is the io.ReaderAt under a sectionReader's io.SectionReader.
type sectionFile sectionReader

func newFDCache(root string, maxOpen int) *fdCache {
	if maxOpen <= 0 {
		panic("maxOpen must be positive")
	}
	return &fdCache{
		root:    root,
		entries: make(map[string]*entry),
		maxOpen: maxOpen,
		changed: make(chan struct{}),
	}
}

// signalLocked wakes all waiters.
// Caller must hold c.mu.
func (c *fdCache) signalLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

// victimLocked selects an unpinned victim using SIEVE.
// Two passes handle a queue of visited entries and
// prevent infinite looping when every entry is pinned.
// Caller must hold c.mu.
func (c *fdCache) victimLocked() *entry {
	n := c.queue.Len()

	for scanned := 0; scanned < 2*n; scanned++ {
		if c.hand == nil {
			c.hand = c.queue.Back()
		}

		node := c.hand

		// Move toward newer entries.
		// Wrap to the oldest on the next iteration.
		c.hand = node.Prev()

		e := node.Value.(*entry)

		// Pinned: never evict an active reader.
		if e.refs != 0 {
			continue
		}

		// Second chance: clear the bit,
		// but do not move the entry.
		if e.visited {
			e.visited = false
			continue
		}

		// Unpinned and unvisited: victim found.
		return e
	}

	// Every entry is pinned.
	return nil
}

var _ io.ReadSeekCloser = (*sectionReader)(nil)

// OpenSection obtains a lease on a file.
// If all FD slots are pinned, it waits for capacity.
// The returned Section must be closed after its last read.
//
// ctx bounds waits for capacity, including those of reads
// that take a new lease after an earlier one was released.
func (c *fdCache) OpenSection(
	ctx context.Context,
	path string,
	off, length int64,
) (*sectionReader, error) {
	if off < 0 || length < 0 {
		return nil, fmt.Errorf(
			"negative section offset or length",
		)
	}

	e, err := c.acquire(ctx, path)
	if err != nil {
		return nil, err
	}
	s := &sectionReader{
		cache:  c,
		path:   path,
		ctx:    ctx,
		entry:  e,
		leased: true,
	}
	s.SectionReader = io.NewSectionReader((*sectionFile)(s), off, length)
	return s, nil
}

// acquire returns the cache entry for path with a lease
// taken on it, opening the file if needed.
// If all FD slots are pinned, it waits for capacity.
func (c *fdCache) acquire(ctx context.Context, path string) (*entry, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		c.mu.Lock()

		if c.closing {
			c.mu.Unlock()
			return nil, os.ErrClosed
		}

		// SIEVE cache hit:
		// Set visited, but do not reorder the queue.
		if e := c.entries[path]; e != nil {
			e.refs++
			e.visited = true
			c.mu.Unlock()
			return e, nil
		}

		// Cache miss with no free FD slot.
		if len(c.entries) == c.maxOpen {
			victim := c.victimLocked()

			if victim == nil {
				// All entries are pinned.
				// Release mutex while waiting.
				changed := c.changed
				c.mu.Unlock()

				select {
				case <-changed:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				continue
			}

			// Evict the selected SIEVE victim.
			delete(c.entries, victim.path)
			c.queue.Remove(victim.node)

			openFdsVar.Add(c.root, -1)
			if err := victim.file.Close(); err != nil {
				c.mu.Unlock()
				return nil, fmt.Errorf(
					"evict %q: %w", victim.path, err,
				)
			}
		}

		// Open under mutex to enforce the FD limit
		// and prevent duplicate opens for the same path.
		f, err := os.Open(path)
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		openFdsVar.Add(c.root, 1)

		// SIEVE insertion:
		// Newest entry, visited initially false.
		e := &entry{
			path: path,
			file: f,
			refs: 1,
		}

		e.node = c.queue.PushFront(e)
		c.entries[path] = e

		c.mu.Unlock()
		return e, nil
	}
}

// release drops a lease on e taken by acquire.
// If the cache is shutting down, the file is closed on its
// last release and any error from closing it is returned.
func (c *fdCache) release(e *entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	e.refs--
	if e.refs != 0 {
		return nil
	}

	var err error
	if c.closing {
		// Cache is shutting down:
		// close the file on its last release.
		delete(c.entries, e.path)
		c.queue.Remove(e.node)

		openFdsVar.Add(c.root, -1)
		err = e.file.Close()
		c.closeErr = errors.Join(c.closeErr, err)
	}

	// An FD may now be evictable.
	c.signalLocked()
	return err
}

// Close releases the Section's lease.
// It does not normally close the underlying file:
// idle files remain cached until eviction or shutdown.
// Reads after Close fail with os.ErrClosed.
func (s *sectionReader) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseLocked()
	s.closed = true
	return s.closeErr
}

// Read reads from the Section and releases the lease once the
// Section is fully consumed (io.EOF), so an eagerly-read but
// never-closed Section does not pin an FD forever.
func (s *sectionReader) Read(p []byte) (int, error) {
	n, err := s.SectionReader.Read(p)
	if err == io.EOF {
		s.mu.Lock()
		s.releaseLocked()
		s.mu.Unlock()
	}
	return n, err
}

// releaseLocked drops the Section's lease, if it holds one.
// Caller must hold s.mu.
func (s *sectionReader) releaseLocked() {
	if !s.leased {
		return
	}
	s.leased = false
	if err := s.cache.release(s.entry); err != nil {
		s.closeErr = err
	}
}

// ReadAt reads from the cached file, first taking a new lease
// if the previous one was released.
func (f *sectionFile) ReadAt(p []byte, off int64) (int, error) {
	s := (*sectionReader)(f)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, os.ErrClosed
	}
	if !s.leased {
		e, err := s.cache.acquire(s.ctx, s.path)
		if err != nil {
			return 0, err
		}
		s.entry, s.leased = e, true
	}
	return s.entry.file.ReadAt(p, off)
}

// open opens path for reading and caches it, without taking a lease.
// It is used to eagerly open the current pack file for reading.
func (c *fdCache) open(ctx context.Context, path string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		c.mu.Lock()

		if c.closing {
			c.mu.Unlock()
			return os.ErrClosed
		}
		if _, ok := c.entries[path]; ok {
			c.mu.Unlock()
			return nil
		}

		if len(c.entries) == c.maxOpen {
			victim := c.victimLocked()
			if victim == nil {
				changed := c.changed
				c.mu.Unlock()

				select {
				case <-changed:
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}

			delete(c.entries, victim.path)
			c.queue.Remove(victim.node)

			openFdsVar.Add(c.root, -1)
			if err := victim.file.Close(); err != nil {
				c.mu.Unlock()
				return fmt.Errorf("evict %q: %w", victim.path, err)
			}
		}

		f, err := os.Open(path)
		if err != nil {
			c.mu.Unlock()
			return err
		}
		openFdsVar.Add(c.root, 1)

		e := &entry{path: path, file: f}
		e.node = c.queue.PushFront(e)
		c.entries[path] = e

		c.mu.Unlock()
		return nil
	}
}

// Shutdown rejects new leases and immediately closes
// idle files, without waiting for active leases.
// Active files remain open until their last release.
// It returns any errors from closing files so far.
func (c *fdCache) Shutdown() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shutdownLocked()
	return c.closeErr
}

// Close rejects new leases and immediately closes
// idle files. It waits for active leases to finish,
// unless the context expires.
//
// If the context expires, active files remain open
// until their respective Section.Close() calls.
func (c *fdCache) Close(ctx context.Context) error {
	c.mu.Lock()
	c.shutdownLocked()

	for len(c.entries) != 0 {
		if err := ctx.Err(); err != nil {
			result := errors.Join(c.closeErr, err)
			c.mu.Unlock()
			return result
		}

		changed := c.changed
		c.mu.Unlock()

		select {
		case <-changed:
		case <-ctx.Done():
		}

		c.mu.Lock()
	}

	result := c.closeErr
	c.mu.Unlock()

	return result
}

// shutdownLocked implements Shutdown.
// Caller must hold c.mu.
func (c *fdCache) shutdownLocked() {
	if !c.closing {
		c.closing = true
		c.hand = nil

		for path, e := range c.entries {
			if e.refs != 0 {
				continue
			}

			delete(c.entries, path)
			c.queue.Remove(e.node)

			openFdsVar.Add(c.root, -1)
			c.closeErr = errors.Join(
				c.closeErr, e.file.Close(),
			)
		}

		c.signalLocked()
	}
}
