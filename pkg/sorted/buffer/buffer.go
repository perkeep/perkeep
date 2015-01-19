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

// Package buffer provides a sorted.KeyValue implementation that
// buffers one KeyValue implementation in front of an another. It's
// used for cases such as reindexing where you need a KeyValue but it
// doesn't need to be flushed and consistent until the end.
package buffer

import (
	"fmt"
	"sync"

	"camlistore.org/pkg/sorted"
)

// New returnes a sorted.KeyValue implementation that adds a Flush
// method to flush the buffer to the backing storage. A flush will
// also be performed when maxBufferBytes are reached. If
// maxBufferBytes <= 0, no automatic flushing is performed.
func New(buffer, backing sorted.KeyValue, maxBufferBytes int64) *KeyValue {
	return &KeyValue{
		buf:       buffer,
		back:      backing,
		maxBuffer: maxBufferBytes,
	}
}

var _ sorted.KeyValue = (*KeyValue)(nil)

type KeyValue struct {
	buf, back sorted.KeyValue
	maxBuffer int64

	bufMu    sync.Mutex
	buffered int64

	// This read lock should be held during Set/Get/Delete/BatchCommit,
	// and the write lock should be held during Flush.
	mu sync.RWMutex
}

func (kv *KeyValue) Flush() error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	var (
		bmback = kv.back.BeginBatch()
		bmbuf  = kv.buf.BeginBatch()
		commit = false
		it     = kv.buf.Find("", "")
	)
	for it.Next() {
		bmback.Set(it.Key(), it.Value())
		bmbuf.Delete(it.Key())
		commit = true
	}
	if err := it.Close(); err != nil {
		return err
	}
	if commit {
		if err := kv.back.CommitBatch(bmback); err != nil {
			return err
		}
		if err := kv.buf.CommitBatch(bmbuf); err != nil {
			return err
		}
		kv.bufMu.Lock()
		kv.buffered = 0
		kv.bufMu.Unlock()
	}
	return nil
}

func (kv *KeyValue) Get(key string) (string, error) {
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	v, err := kv.buf.Get(key)
	switch err {
	case sorted.ErrNotFound:
		break
	case nil:
		return v, nil
	default:
		return "", err
	}
	return kv.back.Get(key)
}

func (kv *KeyValue) Set(key, value string) error {
	if err := sorted.CheckSizes(key, value); err != nil {
		return err
	}
	kv.mu.RLock()
	err := kv.buf.Set(key, value)
	kv.mu.RUnlock()
	if err == nil {
		kv.bufMu.Lock()
		kv.buffered += int64(len(key) + len(value))
		doFlush := kv.buffered > kv.maxBuffer
		kv.bufMu.Unlock()
		if doFlush {
			err = kv.Flush()
		}
	}
	return err
}

func (kv *KeyValue) Delete(key string) error {
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	// This isn't an ideal implementation, since it synchronously
	// deletes from the backing store. But deletes aren't really
	// used, so ignoring for now.
	// Could also use a syncutil.Group to do these in parallel,
	// but the buffer should be an in-memory implementation
	// anyway, so should be fast.
	err1 := kv.buf.Delete(key)
	err2 := kv.back.Delete(key)
	if err1 != nil {
		return err1
	}
	return err2
}

func (kv *KeyValue) BeginBatch() sorted.BatchMutation {
	return new(batch)
}

func (kv *KeyValue) CommitBatch(bm sorted.BatchMutation) error {
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	b, ok := bm.(*batch)
	if !ok {
		return fmt.Errorf("unexpected BatchMutation type %T", bm)
	}
	var (
		// A batch mutation for applying this mutation to the buffer.
		bmbuf = kv.buf.BeginBatch()
		// A lazily created batch mutation for deleting from the backing
		// storage; this should be rare. (See Delete above.)
		bmback sorted.BatchMutation
	)
	for _, m := range b.mods {
		if m.isDelete {
			bmbuf.Delete(m.key)
			if bmback == nil {
				bmback = kv.back.BeginBatch()
			}
			bmback.Delete(m.key)
			continue
		} else {
			if err := sorted.CheckSizes(m.key, m.value); err != nil {
				return err
			}
		}
		bmbuf.Set(m.key, m.value)
	}
	if err := kv.buf.CommitBatch(bmbuf); err != nil {
		return err
	}
	if bmback != nil {
		return kv.back.CommitBatch(bmback)
	}
	return nil
}

func (kv *KeyValue) Close() error {
	if err := kv.Flush(); err != nil {
		return err
	}
	return kv.back.Close()
}

func (kv *KeyValue) Find(start, end string) sorted.Iterator {
	// TODO(adg): hold read lock while iterating? seems complicated
	ibuf := kv.buf.Find(start, end)
	iback := kv.back.Find(start, end)
	return &iter{
		buf:  subIter{Iterator: ibuf},
		back: subIter{Iterator: iback},
	}
}

type batch struct {
	mu   sync.Mutex
	mods []mod
}

type mod struct {
	isDelete   bool
	key, value string
}

func (b *batch) Set(key, value string) {
	defer b.mu.Unlock()
	b.mu.Lock()
	b.mods = append(b.mods, mod{key: key, value: value})
}

func (b *batch) Delete(key string) {
	defer b.mu.Unlock()
	b.mu.Lock()
	b.mods = append(b.mods, mod{key: key, isDelete: true})
}

type iter struct {
	buf, back subIter
}

func (it *iter) current() *subIter {
	switch {
	case it.back.eof:
		return &it.buf
	case it.buf.eof:
		return &it.back
	case it.buf.key <= it.back.key:
		return &it.buf
	default:
		return &it.back
	}
}

func (it *iter) Next() bool {
	// Call Next on both iterators for the first time, if we haven't
	// already, so that the key comparisons below are valid.
	start := false
	if it.buf.key == "" && !it.buf.eof {
		start = it.buf.next()
	}
	if it.back.key == "" && !it.buf.eof {
		start = it.back.next() || start
	}
	if start {
		// We started iterating with at least one value.
		return true
	}
	// Bail if both iterators are done.
	if it.buf.eof && it.back.eof {
		return false
	}
	// If one iterator is done, advance the other.
	if it.buf.eof {
		return it.back.next()
	}
	if it.back.eof {
		return it.buf.next()
	}
	// If both iterators still going,
	// advance the one that is further behind,
	// or both simultaneously if they point to the same key.
	switch {
	case it.buf.key < it.back.key:
		it.buf.next()
	case it.buf.key > it.back.key:
		it.back.next()
	case it.buf.key == it.back.key:
		n1, n2 := it.buf.next(), it.back.next()
		if !n1 && !n2 {
			// Both finished simultaneously.
			return false
		}
	}
	return true
}

func (it *iter) Key() string {
	return it.current().key
}

func (it *iter) Value() string {
	return it.current().Value()
}

func (it *iter) KeyBytes() []byte {
	return it.current().KeyBytes()
}

func (it *iter) ValueBytes() []byte {
	return it.current().ValueBytes()
}

func (it *iter) Close() error {
	err1 := it.buf.Close()
	err2 := it.back.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// subIter is an iterator (either the backing storage or the buffer) that
// keeps track of the current key and whether it has reached EOF.
type subIter struct {
	sorted.Iterator
	key string
	eof bool
}

func (it *subIter) next() bool {
	if it.Next() {
		it.key = it.Key()
		return true
	}
	it.eof = true
	return false
}
