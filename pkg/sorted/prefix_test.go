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

package sorted_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/sorted"
	_ "perkeep.org/pkg/sorted/defaultkv"
	"perkeep.org/pkg/test"
)

func testPrefixStores(t *testing.T, fn func(*testing.T, sorted.KeyValue)) {
	t.Helper()
	for _, name := range []string{"memory", "defaultkv"} {
		t.Run(name, func(t *testing.T) {
			var kv sorted.KeyValue
			if name == "memory" {
				kv = sorted.NewMemoryKeyValue()
			} else {
				var err error
				kv, err = sorted.NewKeyValue(jsonconfig.Obj{
					"type": sorted.DefaultKVFileType,
					"file": filepath.Join(t.TempDir(), "cache"),
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				if err := kv.Close(); err != nil {
					t.Error(err)
				}
			})
			fn(t, kv)
		})
	}
}

func prefixSet(t *testing.T, kv sorted.KeyValue, key, value string) {
	t.Helper()
	if err := kv.Set(key, value); err != nil {
		t.Fatal(err)
	}
}

func prefixGet(t *testing.T, kv interface{ Get(string) (string, error) }, key, want string) {
	t.Helper()
	if got, err := kv.Get(key); err != nil || got != want {
		t.Errorf("Get(%q) = %q, %v; want %q, nil", key, got, err, want)
	}
}

func prefixEntries(t *testing.T, it sorted.Iterator) []sorted.Entry {
	t.Helper()
	entries := []sorted.Entry{}
	for it.Next() {
		key, value := it.Key(), it.Value()
		if got := string(it.KeyBytes()); got != key {
			t.Errorf("KeyBytes = %q; Key = %q", got, key)
		}
		if got := string(it.ValueBytes()); got != value {
			t.Errorf("ValueBytes = %q; Value = %q", got, value)
		}
		entries = append(entries, sorted.Entry{Key: key, Value: value})
	}
	if it.Next() {
		t.Error("exhausted iterator returned another entry")
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestWithPrefixIsolation(t *testing.T) {
	testPrefixStores(t, func(t *testing.T, raw sorted.KeyValue) {
		prefixSet(t, raw, "a", "raw")
		prefixSet(t, raw, "left0", "adjacent namespace")
		left := sorted.WithPrefix(raw, "left/")
		right := sorted.WithPrefix(raw, "right/")
		keys := []string{"", "\x00", "../right/a", "/", "a", "left/", "\xff", "\xff\xff"}
		want := []sorted.Entry{}
		for _, key := range keys {
			prefixSet(t, left, key, "left:"+key)
			prefixSet(t, right, key, "right:"+key)
			prefixGet(t, left, key, "left:"+key)
			prefixGet(t, raw, "left/"+key, "left:"+key)
			want = append(want, sorted.Entry{Key: key, Value: "left:" + key})
		}
		if got := prefixEntries(t, left.Find("", "")); !reflect.DeepEqual(got, want) {
			t.Errorf("left entries = %q; want %q", got, want)
		}
		if _, err := left.Get("left0"); !errors.Is(err, sorted.ErrNotFound) {
			t.Errorf("Get of unprefixed raw key = %v; want ErrNotFound", err)
		}
		for range 2 {
			if err := left.Delete("a"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := left.Get("a"); !errors.Is(err, sorted.ErrNotFound) {
			t.Errorf("Get deleted key = %v; want ErrNotFound", err)
		}
		prefixGet(t, raw, "a", "raw")
		prefixGet(t, right, "a", "right:a")
		if err := left.Delete(""); err != nil {
			t.Fatal(err)
		}
		prefixGet(t, right, "", "right:")

		nested := sorted.WithPrefix(left, "nested/")
		prefixSet(t, nested, "", "nested empty key")
		prefixSet(t, nested, "k", "nested value")
		prefixGet(t, raw, "left/nested/k", "nested value")
		if got, want := prefixEntries(t, nested.Find("", "")), ([]sorted.Entry{
			{Key: "", Value: "nested empty key"}, {Key: "k", Value: "nested value"},
		}); !reflect.DeepEqual(got, want) {
			t.Errorf("nested entries = %q; want %q", got, want)
		}
		b := nested.BeginBatch()
		b.Delete("")
		b.Set("batch", "nested batch value")
		if err := nested.CommitBatch(b); err != nil {
			t.Fatal(err)
		}
		prefixGet(t, raw, "left/nested/batch", "nested batch value")
	})
}

func TestWithPrefixFindBounds(t *testing.T) {
	for _, tc := range []struct{ prefix, upper string }{
		{"ns/", "ns0"},
		{"ns\xff", "nt"},
		{"a\xfe\xff\xff", "a\xff"},
		{"\x00", "\x01"},
		{"\xff", ""},
		{"\xff\xff", ""},
		{"", ""},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			testPrefixStores(t, func(t *testing.T, raw sorted.KeyValue) {
				observed := &prefixObservedKV{KeyValue: raw}
				kv := sorted.WithPrefix(observed, tc.prefix)
				keys := []string{"", "\x00", "\x00a", "a", "aa", "b", "\xfe", "\xff", "\xff\x00", "\xff\xff"}
				for _, key := range keys {
					prefixSet(t, kv, key, "v:"+key)
				}
				outsiders := []string{"", "a", "ns.", "ns0", "nt", "z", "\xff", "\xff\xff", "\xff\xff\xff"}
				if tc.prefix != "" {
					outsiders = append(outsiders, tc.prefix[:len(tc.prefix)-1])
				}
				if tc.upper != "" {
					outsiders = append(outsiders, tc.upper, tc.upper+"suffix")
				}
				for _, key := range outsiders {
					if !strings.HasPrefix(key, tc.prefix) {
						prefixSet(t, raw, key, "outside")
					}
				}
				for _, bounds := range [][2]string{
					{"", ""}, {"a", ""}, {"\xff", ""}, {"\xff\xff\xff", ""},
					{"", "\x00"}, {"", "a"}, {"\x00", "b"}, {"a", "a"},
					{"b", "a"}, {"\xfe", "\xff\xff"},
				} {
					start, end := bounds[0], bounds[1]
					want := []sorted.Entry{}
					for _, key := range keys {
						if key >= start && (end == "" || key < end) {
							want = append(want, sorted.Entry{Key: key, Value: "v:" + key})
						}
					}
					got := prefixEntries(t, kv.Find(start, end))
					if !reflect.DeepEqual(got, want) {
						t.Errorf("Find(%q, %q) = %q; want %q", start, end, got, want)
					}
					wantEnd := tc.upper
					if end != "" {
						wantEnd = tc.prefix + end
					}
					if observed.start != tc.prefix+start || observed.end != wantEnd {
						t.Errorf("backing Find(%q, %q); want (%q, %q)", observed.start, observed.end, tc.prefix+start, wantEnd)
					}
				}
			})
		})
	}
}

type prefixObservedKV struct {
	sorted.KeyValue
	start, end      string
	begins, commits int
	sets, deletes   int
	closes          int
}

func (kv *prefixObservedKV) Find(start, end string) sorted.Iterator {
	kv.start, kv.end = start, end
	return kv.KeyValue.Find(start, end)
}

func (kv *prefixObservedKV) BeginBatch() sorted.BatchMutation {
	kv.begins++
	return kv.KeyValue.BeginBatch()
}

func (kv *prefixObservedKV) CommitBatch(b sorted.BatchMutation) error {
	kv.commits++
	return kv.KeyValue.CommitBatch(b)
}

func (kv *prefixObservedKV) Set(key, value string) error {
	kv.sets++
	return kv.KeyValue.Set(key, value)
}

func (kv *prefixObservedKV) Delete(key string) error {
	kv.deletes++
	return kv.KeyValue.Delete(key)
}

func (kv *prefixObservedKV) Close() error {
	kv.closes++
	return kv.KeyValue.Close()
}

func TestWithPrefixBatchOwnership(t *testing.T) {
	testPrefixStores(t, func(t *testing.T, raw sorted.KeyValue) {
		observed := &prefixObservedKV{KeyValue: raw}
		kv := sorted.WithPrefix(observed, "a/")
		sibling := sorted.WithPrefix(raw, "b/")
		samePrefix := sorted.WithPrefix(raw, "a/")
		otherStore := sorted.WithPrefix(sorted.NewMemoryKeyValue(), "a/")
		b := kv.BeginBatch()
		b.Set("", "empty")
		b.Set("../b/key", "literal")
		b.Set("\xff", "bytes")
		b.Delete("old")
		b.Delete("missing")
		prefixSet(t, kv, "old", "old value")
		prefixSet(t, sibling, "old", "sibling value")
		if observed.begins != 0 {
			t.Fatal("view BeginBatch opened a backing transaction")
		}
		for _, invalid := range []sorted.BatchMutation{
			nil, reflect.Zero(reflect.TypeOf(b)).Interface().(sorted.BatchMutation),
			sorted.NewBatchMutation(), raw.BeginBatch(),
			sibling.BeginBatch(), samePrefix.BeginBatch(), otherStore.BeginBatch(),
		} {
			if err := kv.CommitBatch(invalid); err == nil {
				t.Errorf("CommitBatch(%T) succeeded; want invalid batch error", invalid)
			}
		}
		for _, wrongOwner := range []sorted.KeyValue{sibling, samePrefix, otherStore, raw} {
			if err := wrongOwner.CommitBatch(b); err == nil {
				t.Errorf("wrong owner's CommitBatch succeeded")
			}
		}
		if observed.begins != 0 || observed.commits != 0 {
			t.Fatal("invalid batches reached backing store")
		}
		if _, ok := b.(interface{ Mutations() []sorted.Mutation }); ok {
			t.Error("view batch exposes backing mutations")
		}
		if _, ok := b.(sorted.ReadTransaction); ok {
			t.Error("view batch exposes unscoped reads")
		}
		if err := kv.CommitBatch(b); err != nil {
			t.Fatal(err)
		}
		if observed.begins != 1 || observed.commits != 1 {
			t.Errorf("backing batch begins/commits = %d/%d; want 1/1", observed.begins, observed.commits)
		}
		prefixGet(t, kv, "", "empty")
		prefixGet(t, raw, "a/../b/key", "literal")
		prefixGet(t, raw, "a/\xff", "bytes")
		prefixGet(t, sibling, "old", "sibling value")
		if _, err := kv.Get("old"); !errors.Is(err, sorted.ErrNotFound) {
			t.Errorf("batch-deleted key = %v; want ErrNotFound", err)
		}
	})
}

func TestWithPrefixSizes(t *testing.T) {
	test.TLog(t)
	testPrefixStores(t, func(t *testing.T, raw sorted.KeyValue) {
		const prefix = "namespace/"
		observed := &prefixObservedKV{KeyValue: raw}
		kv := sorted.WithPrefix(observed, prefix)
		maxKey := strings.Repeat("k", sorted.MaxKeySize-len(prefix))
		maxValue := strings.Repeat("v", sorted.MaxValueSize)
		prefixSet(t, kv, maxKey, maxValue)
		prefixGet(t, kv, maxKey, maxValue)
		for _, tc := range []struct {
			key, value string
			want       error
		}{
			{maxKey + "x", "v", sorted.ErrKeyTooLarge},
			{"too-large-value", maxValue + "x", sorted.ErrValueTooLarge},
		} {
			before := observed.sets
			if err := kv.Set(tc.key, tc.value); !errors.Is(err, tc.want) {
				t.Errorf("oversized Set error = %v; want %v", err, tc.want)
			}
			if observed.sets != before {
				t.Error("oversized Set reached backing store")
			}
			prefixSet(t, kv, "keep", "original")
			b := kv.BeginBatch()
			b.Delete("keep")
			b.Set("new", "new value")
			b.Set(tc.key, tc.value)
			b.Set("later", "later value")
			if err := kv.CommitBatch(b); !errors.Is(err, tc.want) {
				t.Errorf("oversized batch error = %v; want %v", err, tc.want)
			}
			if observed.begins != 0 || observed.commits != 0 {
				t.Error("invalid batch opened or committed a backing transaction")
			}
			prefixGet(t, kv, "keep", "original")
			for _, key := range []string{"new", "later", tc.key} {
				if _, err := kv.Get(key); !errors.Is(err, sorted.ErrNotFound) {
					t.Errorf("invalid batch key %q = %v; want ErrNotFound", key, err)
				}
			}
		}
		if err := kv.Delete(maxKey + "x"); !errors.Is(err, sorted.ErrKeyTooLarge) {
			t.Errorf("oversized Delete error = %v; want ErrKeyTooLarge", err)
		}
		if observed.deletes != 0 {
			t.Error("oversized Delete reached backing store")
		}
		b := kv.BeginBatch()
		b.Set("new", "value")
		b.Delete(maxKey + "x")
		if err := kv.CommitBatch(b); !errors.Is(err, sorted.ErrKeyTooLarge) {
			t.Errorf("oversized batch Delete error = %v; want ErrKeyTooLarge", err)
		}
		if observed.begins != 0 {
			t.Error("invalid delete batch opened backing transaction")
		}
		b = kv.BeginBatch()
		b.Set(maxKey, maxValue)
		if err := kv.CommitBatch(b); err != nil {
			t.Fatal(err)
		}
		prefixGet(t, kv, maxKey, maxValue)

		maxPrefix := sorted.WithPrefix(raw, strings.Repeat("p", sorted.MaxKeySize))
		prefixSet(t, maxPrefix, "", "max-prefix empty key")
		if err := maxPrefix.Set("x", "v"); !errors.Is(err, sorted.ErrKeyTooLarge) {
			t.Errorf("key exceeding limit by prefix = %v; want ErrKeyTooLarge", err)
		}
		overPrefix := sorted.WithPrefix(raw, strings.Repeat("p", sorted.MaxKeySize+1))
		if err := overPrefix.Set("", "v"); !errors.Is(err, sorted.ErrKeyTooLarge) {
			t.Errorf("oversized prefix = %v; want ErrKeyTooLarge", err)
		}
	})
}

func TestWithPrefixClose(t *testing.T) {
	testPrefixStores(t, func(t *testing.T, raw sorted.KeyValue) {
		observed := &prefixObservedKV{KeyValue: raw}
		kv := sorted.WithPrefix(observed, "owned/")
		sibling := sorted.WithPrefix(observed, "sibling/")
		b := kv.BeginBatch()
		b.Set("before-close", "batch")
		for range 2 {
			if err := kv.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if observed.closes != 0 {
			t.Errorf("view Close closed backing store %d times", observed.closes)
		}
		prefixSet(t, raw, "raw", "alive")
		prefixSet(t, kv, "after-close", "view alive")
		prefixSet(t, sibling, "key", "sibling alive")
		if err := kv.CommitBatch(b); err != nil {
			t.Fatal(err)
		}
		prefixGet(t, kv, "before-close", "batch")
		prefixGet(t, kv, "after-close", "view alive")
		prefixGet(t, sibling, "key", "sibling alive")
		prefixGet(t, raw, "raw", "alive")
	})
}

func TestWithPrefixReadTransaction(t *testing.T) {
	testPrefixStores(t, func(t *testing.T, raw sorted.KeyValue) {
		kv := sorted.WithPrefix(raw, "tx/")
		reader, ok := kv.(sorted.TransactionalReader)
		_, backingSupports := raw.(sorted.TransactionalReader)
		if ok != backingSupports {
			t.Fatal("view did not preserve optional TransactionalReader support")
		}
		if !ok {
			return // Memory has no TransactionalReader; exercised above.
		}
		prefixSet(t, raw, "outside", "outside")
		prefixSet(t, raw, "tx0", "adjacent")
		prefixSet(t, kv, "", "empty")
		prefixSet(t, kv, "a", "old")
		prefixSet(t, kv, "b", "delete later")
		tx := reader.BeginReadTx()
		prefixSet(t, kv, "a", "new")
		prefixSet(t, kv, "c", "add later")
		if err := kv.Delete("b"); err != nil {
			t.Fatal(err)
		}
		if err := kv.Close(); err != nil {
			t.Fatal(err)
		}
		prefixGet(t, tx, "", "empty")
		prefixGet(t, tx, "a", "old")
		if _, err := tx.Get("outside"); !errors.Is(err, sorted.ErrNotFound) {
			t.Errorf("transaction Get outside namespace = %v; want ErrNotFound", err)
		}
		want := []sorted.Entry{{Key: "", Value: "empty"}, {Key: "a", Value: "old"}, {Key: "b", Value: "delete later"}}
		if got := prefixEntries(t, tx.Find("", "")); !reflect.DeepEqual(got, want) {
			t.Errorf("snapshot entries = %q; want %q", got, want)
		}
		if got := prefixEntries(t, tx.Find("a", "b")); !reflect.DeepEqual(got, want[1:2]) {
			t.Errorf("bounded snapshot entries = %q; want %q", got, want[1:2])
		}
		if _, ok := tx.(sorted.KeyValue); ok {
			t.Error("read transaction exposes mutations")
		}
		if err := tx.Close(); err != nil {
			t.Fatal(err)
		}
		prefixGet(t, kv, "a", "new")
		prefixSet(t, raw, "still", "alive")
	})
}

// These optional interfaces must not be inherited by a prefix view.
type prefixUnsafeKV struct{ sorted.KeyValue }

func (*prefixUnsafeKV) Wipe() error { return nil }
func (*prefixUnsafeKV) BeginReindex() (sorted.ReindexSession, error) {
	return nil, nil
}
func (*prefixUnsafeKV) RawStorage() sorted.KeyValue { return nil }

func TestWithPrefixOptionalInterfaces(t *testing.T) {
	for _, prefix := range []string{"", "namespace/"} {
		kv := sorted.WithPrefix(&prefixUnsafeKV{sorted.NewMemoryKeyValue()}, prefix)
		if _, ok := kv.(sorted.Wiper); ok {
			t.Error("prefix view exposes Wiper")
		}
		if _, ok := kv.(sorted.Reindexer); ok {
			t.Error("prefix view exposes Reindexer")
		}
		if _, ok := kv.(interface{ RawStorage() sorted.KeyValue }); ok {
			t.Error("prefix view exposes raw backend")
		}
	}
}

type prefixTransactionalUnsafeKV struct {
	*prefixUnsafeKV
	tx *prefixObservedReadTransaction
}

func (kv *prefixTransactionalUnsafeKV) BeginReadTx() sorted.ReadTransaction { return kv.tx }

type prefixObservedReadTransaction struct {
	kv       sorted.KeyValue
	closes   int
	closeErr error
}

func (tx *prefixObservedReadTransaction) Get(key string) (string, error) {
	return tx.kv.Get(key)
}
func (tx *prefixObservedReadTransaction) Find(start, end string) sorted.Iterator {
	return tx.kv.Find(start, end)
}
func (tx *prefixObservedReadTransaction) Close() error {
	tx.closes++
	return tx.closeErr
}

func TestWithPrefixReadTransactionClose(t *testing.T) {
	raw := sorted.NewMemoryKeyValue()
	observed := &prefixObservedKV{KeyValue: raw}
	closeErr := errors.New("snapshot close error")
	backingTx := &prefixObservedReadTransaction{kv: raw, closeErr: closeErr}
	backing := &prefixTransactionalUnsafeKV{
		prefixUnsafeKV: &prefixUnsafeKV{KeyValue: observed}, tx: backingTx,
	}
	kv := sorted.WithPrefix(backing, "scope/")
	if _, ok := kv.(sorted.Wiper); ok {
		t.Error("transactional view exposes Wiper")
	}
	if _, ok := kv.(sorted.Reindexer); ok {
		t.Error("transactional view exposes Reindexer")
	}
	if _, ok := kv.(interface{ RawStorage() sorted.KeyValue }); ok {
		t.Error("transactional view exposes raw backend")
	}
	tx := kv.(sorted.TransactionalReader).BeginReadTx()
	if err := kv.Close(); err != nil {
		t.Fatal(err)
	}
	if observed.closes != 0 || backingTx.closes != 0 {
		t.Error("view Close closed store or transaction")
	}
	if err := tx.Close(); !errors.Is(err, closeErr) {
		t.Errorf("transaction Close = %v; want %v", err, closeErr)
	}
	if backingTx.closes != 1 || observed.closes != 0 {
		t.Error("transaction Close did not close only the snapshot")
	}
	prefixSet(t, raw, "still", "alive")
}

// prefixBufferIterator reuses its byte buffers, as real backend iterators may.
type prefixBufferIterator struct {
	keys       []string
	key, value []byte
	next       int
	closeErr   error
}

func (it *prefixBufferIterator) Next() bool {
	if it.next == len(it.keys) {
		return false
	}
	it.key = append(it.key[:0], it.keys[it.next]...)
	it.value = append(it.value[:0], "value"...)
	it.next++
	return true
}
func (it *prefixBufferIterator) Key() string        { return string(it.key) }
func (it *prefixBufferIterator) KeyBytes() []byte   { return it.key }
func (it *prefixBufferIterator) Value() string      { return string(it.value) }
func (it *prefixBufferIterator) ValueBytes() []byte { return it.value }
func (it *prefixBufferIterator) Close() error {
	clear(it.key)
	clear(it.value)
	return it.closeErr
}

type prefixIteratorKV struct {
	sorted.KeyValue
	it sorted.Iterator
}

func (kv *prefixIteratorKV) Find(_, _ string) sorted.Iterator { return kv.it }

func TestWithPrefixIteratorBytesAndErrors(t *testing.T) {
	closeErr := errors.New("iterator close error")
	backingIt := &prefixBufferIterator{
		keys: []string{"prefix/one", "prefix/two", "adjacent"},
		key:  make([]byte, 0, 32), value: make([]byte, 0, 32), closeErr: closeErr,
	}
	kv := sorted.WithPrefix(&prefixIteratorKV{KeyValue: sorted.NewMemoryKeyValue(), it: backingIt}, "prefix/")
	it := kv.Find("", "")
	if !it.Next() {
		t.Fatal("no first key")
	}
	key := it.KeyBytes()
	if string(key) != "one" || &key[0] != &backingIt.key[len("prefix/")] {
		t.Fatal("KeyBytes did not return a trimmed backing slice")
	}
	value := it.ValueBytes()
	if &value[0] != &backingIt.value[0] {
		t.Fatal("ValueBytes did not preserve backing slice")
	}
	if !it.Next() || it.Key() != "two" || string(key) != "two" {
		t.Fatal("iterator did not preserve backend byte lifetime")
	}
	if it.Next() || it.Next() {
		t.Error("iterator leaked a key without the prefix")
	}
	if err := it.Close(); !errors.Is(err, closeErr) {
		t.Errorf("Close error = %v; want %v", err, closeErr)
	}
	if !slices.Equal(value, make([]byte, len(value))) {
		t.Error("Close did not close the backing iterator")
	}
}
