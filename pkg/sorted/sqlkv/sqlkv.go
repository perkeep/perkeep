/*
Copyright 2012 The Camlistore Authors.

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

// Package sqlkv implements the sorted.KeyValue interface using an *sql.DB.
package sqlkv

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"

	"camlistore.org/pkg/leak"
	"camlistore.org/pkg/sorted"
)

// KeyValue implements the sorted.KeyValue interface using an *sql.DB.
type KeyValue struct {
	DB *sql.DB

	// SetFunc is an optional func to use when REPLACE INTO does not exist
	SetFunc      func(*sql.DB, string, string) error
	BatchSetFunc func(*sql.Tx, string, string) error

	// PlaceHolderFunc optionally replaces ? placeholders with the right ones for the rdbms
	// in use
	PlaceHolderFunc func(string) string

	// Serial determines whether a Go-level mutex protects DB from
	// concurrent access.  This isn't perfect and exists just for
	// SQLite, whose driver likes to return "the database is
	// locked" (camlistore.org/issue/114), so this keeps some
	// pressure off. But we still trust SQLite to deal with
	// concurrency in most cases.
	Serial bool

	// TablePrefix optionally provides a prefix for SQL table
	// names. This is typically "dbname.", ending in a period.
	TablePrefix string

	mu sync.Mutex // the mutex used, if Serial is set
}

func (kv *KeyValue) sql(v string) string {
	// TODO(bradfitz): all this string manipulation is redundant at runtime.
	// We should do it once at the beginning and keep the strings around.
	if f := kv.PlaceHolderFunc; f != nil {
		v = f(v)
	}
	return strings.Replace(v, "/*TPRE*/", kv.TablePrefix, -1)
}

type batchTx struct {
	tx  *sql.Tx
	err error // sticky

	// SetFunc is an optional func to use when REPLACE INTO does not exist
	SetFunc func(*sql.Tx, string, string) error

	// PlaceHolderFunc optionally replaces ? placeholders with the right ones for the rdbms
	// in use
	PlaceHolderFunc func(string) string
}

func (b *batchTx) sql(v string) string {
	if f := b.PlaceHolderFunc; f != nil {
		return f(v)
	}
	return v
}

func (b *batchTx) Set(key, value string) {
	if b.err != nil {
		return
	}
	if b.SetFunc != nil {
		b.err = b.SetFunc(b.tx, key, value)
		return
	}
	_, b.err = b.tx.Exec(b.sql("REPLACE INTO /*TPRE*/rows (k, v) VALUES (?, ?)"), key, value)
}

func (b *batchTx) Delete(key string) {
	if b.err != nil {
		return
	}
	_, b.err = b.tx.Exec(b.sql("DELETE FROM /*TPRE*/rows WHERE k=?"), key)
}

func (kv *KeyValue) BeginBatch() sorted.BatchMutation {
	if kv.Serial {
		kv.mu.Lock()
	}
	tx, err := kv.DB.Begin()
	return &batchTx{
		tx:              tx,
		err:             err,
		SetFunc:         kv.BatchSetFunc,
		PlaceHolderFunc: kv.PlaceHolderFunc,
	}
}

func (kv *KeyValue) CommitBatch(b sorted.BatchMutation) error {
	if kv.Serial {
		defer kv.mu.Unlock()
	}
	bt, ok := b.(*batchTx)
	if !ok {
		return fmt.Errorf("wrong BatchMutation type %T", b)
	}
	if bt.err != nil {
		return bt.err
	}
	return bt.tx.Commit()
}

func (kv *KeyValue) Get(key string) (value string, err error) {
	if kv.Serial {
		kv.mu.Lock()
		defer kv.mu.Unlock()
	}
	err = kv.DB.QueryRow(kv.sql("SELECT v FROM /*TPRE*/rows WHERE k=?"), key).Scan(&value)
	if err == sql.ErrNoRows {
		err = sorted.ErrNotFound
	}
	return
}

func (kv *KeyValue) Set(key, value string) error {
	if kv.Serial {
		kv.mu.Lock()
		defer kv.mu.Unlock()
	}
	if kv.SetFunc != nil {
		return kv.SetFunc(kv.DB, key, value)
	}
	_, err := kv.DB.Exec(kv.sql("REPLACE INTO /*TPRE*/rows (k, v) VALUES (?, ?)"), key, value)
	return err
}

func (kv *KeyValue) Delete(key string) error {
	if kv.Serial {
		kv.mu.Lock()
		defer kv.mu.Unlock()
	}
	_, err := kv.DB.Exec(kv.sql("DELETE FROM /*TPRE*/rows WHERE k=?"), key)
	return err
}

func (kv *KeyValue) Wipe() error {
	if kv.Serial {
		kv.mu.Lock()
		defer kv.mu.Unlock()
	}
	_, err := kv.DB.Exec(kv.sql("DELETE FROM /*TPRE*/rows"))
	return err
}

func (kv *KeyValue) Close() error { return kv.DB.Close() }

func (kv *KeyValue) Find(start, end string) sorted.Iterator {
	if kv.Serial {
		kv.mu.Lock()
		defer kv.mu.Unlock()
	}
	var rows *sql.Rows
	var err error
	if end == "" {
		rows, err = kv.DB.Query(kv.sql("SELECT k, v FROM /*TPRE*/rows WHERE k >= ? ORDER BY k "), start)
	} else {
		rows, err = kv.DB.Query(kv.sql("SELECT k, v FROM /*TPRE*/rows WHERE k >= ? AND k < ? ORDER BY k "), start, end)
	}
	if err != nil {
		log.Printf("unexpected query error: %v", err)
		return &iter{err: err}
	}

	it := &iter{
		kv:         kv,
		rows:       rows,
		closeCheck: leak.NewChecker(),
	}
	return it
}

var wordThenPunct = regexp.MustCompile(`^\w+\W$`)

// iter is a iterator over sorted key/value pairs in rows.
type iter struct {
	kv  *KeyValue
	end string // optional end bound
	err error  // accumulated error, returned at Close

	closeCheck *leak.Checker

	rows *sql.Rows // if non-nil, the rows we're reading from

	key        sql.RawBytes
	val        sql.RawBytes
	skey, sval *string // if non-nil, it's been stringified
}

var errClosed = errors.New("sqlkv: Iterator already closed")

func (t *iter) KeyBytes() []byte { return t.key }
func (t *iter) Key() string {
	if t.skey != nil {
		return *t.skey
	}
	str := string(t.key)
	t.skey = &str
	return str
}

func (t *iter) ValueBytes() []byte { return t.val }
func (t *iter) Value() string {
	if t.sval != nil {
		return *t.sval
	}
	str := string(t.val)
	t.sval = &str
	return str
}

func (t *iter) Close() error {
	t.closeCheck.Close()
	if t.rows != nil {
		t.rows.Close()
		t.rows = nil
	}
	err := t.err
	t.err = errClosed
	return err
}

func (t *iter) Next() bool {
	if t.err != nil {
		return false
	}
	t.skey, t.sval = nil, nil
	if !t.rows.Next() {
		return false
	}
	t.err = t.rows.Scan(&t.key, &t.val)
	if t.err != nil {
		log.Printf("unexpected Scan error: %v", t.err)
		return false
	}
	return true
}
