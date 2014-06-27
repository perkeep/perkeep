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
		buffer:    buffer,
		back:      backing,
		maxBuffer: maxBufferBytes,
	}
}

var _ sorted.KeyValue = (*KeyValue)(nil)

type KeyValue struct {
	buffer, back sorted.KeyValue
	maxBuffer    int64

	mu       sync.Mutex
	buffered int64
}

func (kv *KeyValue) Flush() error {
	panic("TODO: implement Flush")
}

func (kv *KeyValue) Get(key string) (string, error) {
	v, err := kv.buffer.Get(key)
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
	return kv.buffer.Set(key, value)
}

func (kv *KeyValue) Delete(key string) error {
	// This isn't an ideal implementation, since it synchronously
	// deletes from the backing store. But deletes aren't really
	// used, so ignoring for now.
	// Could also use a syncutil.Group to do these in parallel,
	// but the buffer should be an in-memory implementation
	// anyway, so should be fast.
	err1 := kv.buffer.Delete(key)
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
	b, ok := bm.(*batch)
	if !ok {
		return fmt.Errorf("unexpected BatchMutation type %T", bm)
	}
	_ = b
	panic("TODO")
}

func (kv *KeyValue) Close() error {
	if err := kv.Flush(); err != nil {
		return err
	}
	return kv.back.Close()
}

func (kv *KeyValue) Find(start, end string) sorted.Iterator {
	ibuf := kv.buffer.Find(start, end)
	iback := kv.back.Find(start, end)
	return &iter{
		ibuf:  ibuf,
		iback: iback,
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
	ibuf    sorted.Iterator
	iback   sorted.Iterator
	bufEOF  bool
	backEOF bool
}

func (it *iter) Key() string        { panic("TODO") }
func (it *iter) Value() string      { panic("TODO") }
func (it *iter) KeyBytes() []byte   { panic("TODO") }
func (it *iter) ValueBytes() []byte { panic("TODO") }
func (it *iter) Next() bool         { panic("TODO") }

func (it *iter) Close() error {
	err1 := it.ibuf.Close()
	err2 := it.iback.Close()
	if err1 != nil {
		return err1
	}
	return err2
}
