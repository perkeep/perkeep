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

package sorted

import (
	"fmt"
	"strings"
)

// WithPrefix returns a view of kv whose keys have prefix prepended in the
// backing store and removed when iterating. An empty prefix views the entire
// store. Prefixes are used verbatim, so one view's prefix must not be a prefix
// of another's if the views need disjoint namespaces.
//
// Key size limits include the prefix. Invalid mutations return size errors;
// for batches, these errors are deferred until CommitBatch, which writes
// nothing if any mutation is invalid. A batch belongs to the view that created
// it, even if another view uses the same prefix.
//
// Closing the view does not close kv. The view exposes no optional backend
// interfaces except TransactionalReader, when supported by kv. Closing a read
// transaction releases that transaction, not kv.
func WithPrefix(kv KeyValue, prefix string) KeyValue {
	p := &prefixKV{kv: kv, prefix: prefix}
	if reader, ok := kv.(TransactionalReader); ok {
		return &prefixTransactionalKV{prefixKV: p, reader: reader}
	}
	return p
}

type prefixKV struct {
	kv     KeyValue
	prefix string
}

func (p *prefixKV) Get(key string) (string, error) {
	return p.kv.Get(p.prefix + key)
}

func (p *prefixKV) Set(key, value string) error {
	key = p.prefix + key
	if err := CheckSizes(key, value); err != nil {
		return err
	}
	return p.kv.Set(key, value)
}

func (p *prefixKV) Delete(key string) error {
	key = p.prefix + key
	if err := CheckSizes(key, ""); err != nil {
		return err
	}
	return p.kv.Delete(key)
}

func (p *prefixKV) BeginBatch() BatchMutation {
	return &prefixBatch{owner: p}
}

func (p *prefixKV) CommitBatch(bm BatchMutation) error {
	b, ok := bm.(*prefixBatch)
	if !ok || b == nil || b.owner != p {
		return fmt.Errorf("sorted: batch %T does not belong to this prefix view", bm)
	}
	if b.err != nil {
		return b.err
	}
	// Delay opening the backing batch until all mutations have been checked.
	// Some backends open a transaction in BeginBatch, with no way to abort it.
	batch := p.kv.BeginBatch()
	for _, m := range b.mutations {
		if m.IsDelete() {
			batch.Delete(m.Key())
		} else {
			batch.Set(m.Key(), m.Value())
		}
	}
	return p.kv.CommitBatch(batch)
}

func (p *prefixKV) Find(start, end string) Iterator {
	start, end = prefixBounds(p.prefix, start, end)
	return &prefixIterator{it: p.kv.Find(start, end), prefix: p.prefix}
}

func (*prefixKV) Close() error { return nil }

type prefixBatch struct {
	owner     *prefixKV
	mutations []mutation
	err       error
}

func (b *prefixBatch) Set(key, value string) {
	if b.err != nil {
		return
	}
	key = b.owner.prefix + key
	if b.err = CheckSizes(key, value); b.err != nil {
		return
	}
	b.mutations = append(b.mutations, mutation{key: key, value: value})
}

func (b *prefixBatch) Delete(key string) {
	if b.err != nil {
		return
	}
	key = b.owner.prefix + key
	if b.err = CheckSizes(key, ""); b.err != nil {
		return
	}
	b.mutations = append(b.mutations, mutation{key: key, delete: true})
}

// prefixBounds translates a local range. For an unbounded end, the upper
// bound is the first byte string after all strings beginning with prefix.
// An empty or all-0xff prefix has no finite upper bound.
func prefixBounds(prefix, start, end string) (string, string) {
	if end != "" {
		return prefix + start, prefix + end
	}
	for i := len(prefix) - 1; i >= 0; i-- {
		if prefix[i] != 0xff {
			return prefix + start, prefix[:i] + string([]byte{prefix[i] + 1})
		}
	}
	return prefix + start, ""
}

type prefixIterator struct {
	it     Iterator
	prefix string
	done   bool
}

func (it *prefixIterator) Next() bool {
	if it.done {
		return false
	}
	if !it.it.Next() || !strings.HasPrefix(it.it.Key(), it.prefix) {
		it.done = true
		return false
	}
	return true
}

func (it *prefixIterator) Key() string        { return it.it.Key()[len(it.prefix):] }
func (it *prefixIterator) KeyBytes() []byte   { return it.it.KeyBytes()[len(it.prefix):] }
func (it *prefixIterator) Value() string      { return it.it.Value() }
func (it *prefixIterator) ValueBytes() []byte { return it.it.ValueBytes() }

func (it *prefixIterator) Close() error {
	it.done = true
	return it.it.Close()
}

type prefixTransactionalKV struct {
	*prefixKV
	reader TransactionalReader
}

func (p *prefixTransactionalKV) BeginReadTx() ReadTransaction {
	return &prefixReadTransaction{tx: p.reader.BeginReadTx(), prefix: p.prefix}
}

type prefixReadTransaction struct {
	tx     ReadTransaction
	prefix string
}

func (tx *prefixReadTransaction) Get(key string) (string, error) {
	return tx.tx.Get(tx.prefix + key)
}

func (tx *prefixReadTransaction) Find(start, end string) Iterator {
	start, end = prefixBounds(tx.prefix, start, end)
	return &prefixIterator{it: tx.tx.Find(start, end), prefix: tx.prefix}
}

func (tx *prefixReadTransaction) Close() error { return tx.tx.Close() }
