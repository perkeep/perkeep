/*
Copyright 2011 The Perkeep Authors

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

package sorted

import (
	"errors"
	"sync"

	"github.com/google/btree"
	"go4.org/jsonconfig"
)

// NewMemoryKeyValue returns a KeyValue implementation that's backed only
// by memory. It's mostly useful for tests and development.
func NewMemoryKeyValue() KeyValue {
	kv := new(memKeys)
	kv.Wipe()
	return kv
}

var _ Wiper = (*memKeys)(nil)

// memEntry is a key/value pair in a memKeys tree, ordered by key.
type memEntry struct {
	key, value string
}

func memEntryLess(a, b memEntry) bool { return a.key < b.key }

// memBTreeDegree is the B-tree's degree: each node holds between
// memBTreeDegree-1 and 2*memBTreeDegree-1 entries.
const memBTreeDegree = 32

// memKeys is an in-memory implementation of KeyValue, for tests and
// development, and for servers configured with a memory-only index.
type memKeys struct {
	mu sync.RWMutex // guards tree
	// tree is the data. Find iterates over a copy-on-write clone of it, so an
	// iterator sees a consistent snapshot as of the Find call. Clone mutates
	// the tree's bookkeeping, so it needs mu held for writing.
	tree *btree.BTreeG[memEntry]
}

func (mk *memKeys) Wipe() error {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	mk.tree = btree.NewG(memBTreeDegree, memEntryLess)
	return nil
}

func (mk *memKeys) Get(key string) (string, error) {
	mk.mu.RLock()
	defer mk.mu.RUnlock()
	e, ok := mk.tree.Get(memEntry{key: key})
	if !ok {
		return "", ErrNotFound
	}
	return e.value, nil
}

func (mk *memKeys) Find(start, end string) Iterator {
	mk.mu.Lock()
	snap := mk.tree.Clone()
	mk.mu.Unlock()
	return &memIter{tree: snap, start: start, end: end}
}

func (mk *memKeys) Set(key, value string) error {
	if err := CheckSizes(key, value); err != nil {
		return nil
	}
	mk.mu.Lock()
	defer mk.mu.Unlock()
	mk.tree.ReplaceOrInsert(memEntry{key, value})
	return nil
}

func (mk *memKeys) Delete(key string) error {
	mk.mu.Lock()
	defer mk.mu.Unlock()
	mk.tree.Delete(memEntry{key: key})
	return nil
}

func (mk *memKeys) BeginBatch() BatchMutation {
	return &batch{}
}

func (mk *memKeys) CommitBatch(bm BatchMutation) error {
	b, ok := bm.(*batch)
	if !ok {
		return errors.New("invalid batch type; not an instance returned by BeginBatch")
	}
	mk.mu.Lock()
	defer mk.mu.Unlock()
	for _, m := range b.Mutations() {
		if m.IsDelete() {
			mk.tree.Delete(memEntry{key: m.Key()})
			continue
		}
		// TODO(mpl): we need to force a reindex when we have a proper solution
		// for storing these too large attributes, if ever.
		if err := CheckSizes(m.Key(), m.Value()); err != nil {
			continue
		}
		mk.tree.ReplaceOrInsert(memEntry{m.Key(), m.Value()})
	}
	return nil
}

func (mk *memKeys) Close() error { return nil }

// memIter iterates over the keys in [start, end) of a snapshot of a
// memKeys tree. An empty end means no end.
type memIter struct {
	tree       *btree.BTreeG[memEntry] // snapshot; not shared with writers
	start, end string

	cur     memEntry // the current entry, valid if started && !done
	started bool
	done    bool
}

func (it *memIter) Next() bool {
	if it.done {
		return false
	}
	pivot := memEntry{key: it.start}
	if it.started {
		pivot = it.cur
	}
	found := false
	it.tree.AscendGreaterOrEqual(pivot, func(e memEntry) bool {
		if it.started && e.key == it.cur.key {
			return true // the current entry; keep going to the next one
		}
		if it.end == "" || e.key < it.end {
			it.cur, found = e, true
		}
		return false
	})
	it.started = true
	if !found {
		it.done = true
		it.tree = nil
	}
	return found
}

func (it *memIter) Key() string        { return it.cur.key }
func (it *memIter) Value() string      { return it.cur.value }
func (it *memIter) KeyBytes() []byte   { return []byte(it.cur.key) }
func (it *memIter) ValueBytes() []byte { return []byte(it.cur.value) }

func (it *memIter) Close() error {
	it.done = true
	it.tree = nil
	return nil
}

func init() {
	RegisterKeyValue("memory", func(cfg jsonconfig.Obj) (KeyValue, error) {
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
		return NewMemoryKeyValue(), nil
	})
}
