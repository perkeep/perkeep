/*
Copyright 2012 The Perkeep Authors.

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
package sqlkv // import "perkeep.org/pkg/sorted/sqlkv"

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"go4.org/syncutil"
	"perkeep.org/internal/leak"
	"perkeep.org/pkg/sorted"
)

// KeyValue implements the sorted.KeyValue interface using an *sql.DB.
type KeyValue struct {
	DB *sql.DB

	// SetFunc is an optional func to use when REPLACE INTO does not exist
	SetFunc      func(*sql.DB, string, string) error
	BatchSetFunc func(*sql.Tx, string, string) error

	// PlaceHolderFunc optionally replaces ? placeholders
	// with the right ones for the rdbms in use.
	PlaceHolderFunc func(string) string

	// Gate optionally limits concurrent access.
	//
	// This originally existed just for SQLite, whose driver likes
	// to return "the database is locked"
	// (perkeep.org/issue/114), so this keeps some pressure
	// off. But we still trust SQLite to deal with concurrency in
	// most cases.
	//
	// It's also used to limit the number of MySQL connections.
	Gate *syncutil.Gate

	// TablePrefix optionally provides a prefix for SQL table
	// names. This is typically "dbname.", ending in a period.
	TablePrefix string

	queriesInitOnce sync.Once // guards initialization of both queries and replacer
	replacer        *strings.Replacer

	queriesMu sync.RWMutex
	queries   map[string]string
}

// sql returns the query, replacing placeholders using PlaceHolderFunc,
// and /*TPRE*/ with TablePrefix.
func (kv *KeyValue) sql(sqlStmt string) string {
	// string manipulation is done only once
	kv.queriesInitOnce.Do(func() {
		kv.queries = make(map[string]string, 8) // we have 8 queries in this file
		kv.replacer = strings.NewReplacer("/*TPRE*/", kv.TablePrefix)
	})
	kv.queriesMu.RLock()
	sqlQuery, ok := kv.queries[sqlStmt]
	kv.queriesMu.RUnlock()
	if ok {
		return sqlQuery
	}
	kv.queriesMu.Lock()
	// check again, now holding the lock
	if sqlQuery, ok = kv.queries[sqlStmt]; ok {
		kv.queriesMu.Unlock()
		return sqlQuery
	}
	sqlQuery = sqlStmt
	if f := kv.PlaceHolderFunc; f != nil {
		sqlQuery = f(sqlQuery)
	}
	sqlQuery = kv.replacer.Replace(sqlQuery)
	kv.queries[sqlStmt] = sqlQuery
	kv.queriesMu.Unlock()
	return sqlQuery
}

type batchTx struct {
	tx  *sql.Tx
	err error // sticky
	kv  *KeyValue
}

func (b *batchTx) Set(key, value string) {
	if b.err != nil {
		return
	}
	if err := sorted.CheckSizes(key, value); err != nil {
		log.Printf("Skipping storing (%q:%q): %v", key, value, err)
		return
	}
	if b.kv.BatchSetFunc != nil {
		b.err = b.kv.BatchSetFunc(b.tx, key, value)
		return
	}
	_, b.err = b.tx.Exec(b.kv.sql("REPLACE INTO /*TPRE*/rows (k, v) VALUES (?, ?)"), key, value)
}

func (b *batchTx) Delete(key string) {
	if b.err != nil {
		return
	}
	_, b.err = b.tx.Exec(b.kv.sql("DELETE FROM /*TPRE*/rows WHERE k=?"), key)
}

func (b *batchTx) Find(start, end string) sorted.Iterator {
	if b.err != nil {
		return &iter{
			kv:         b.kv,
			closeCheck: leak.NewChecker(),
			err:        b.err,
		}
	}
	return find(b.kv, b.tx, start, end)
}

func (b *batchTx) Get(key string) (value string, err error) {
	if b.err != nil {
		return "", b.err
	}
	return get(b.kv, b.tx, key)
}

func (b *batchTx) Close() error {
	if b.err != nil {
		return b.err
	}
	if b.kv.Gate != nil {
		defer b.kv.Gate.Done()
	}
	return b.tx.Commit()
}

func (kv *KeyValue) beginTx(txOpts *sql.TxOptions) *batchTx {
	if kv.Gate != nil {
		kv.Gate.Start()
	}
	tx, err := kv.DB.BeginTx(context.TODO(), txOpts)
	if err != nil {
		log.Printf("SQL BEGIN BATCH: %v", err)
	}
	return &batchTx{
		tx:  tx,
		err: err,
		kv:  kv,
	}
}

func (kv *KeyValue) BeginBatch() sorted.BatchMutation {
	return kv.beginTx(nil)
}

func (kv *KeyValue) CommitBatch(b sorted.BatchMutation) error {
	if kv.Gate != nil {
		defer kv.Gate.Done()
	}
	bt, ok := b.(*batchTx)
	if !ok {
		return fmt.Errorf("wrong BatchMutation type %T", b)
	}
	if bt.err != nil {
		if err := bt.tx.Rollback(); err != nil {
			log.Printf("Transaction rollback error: %v", err)
		}
		return bt.err
	}
	return bt.tx.Commit()
}

func (kv *KeyValue) BeginReadTx() sorted.ReadTransaction {
	return kv.beginTx(&sql.TxOptions{
		ReadOnly: true,
		// Needed so that repeated reads of the same data are always
		// consistent:
		Isolation: sql.LevelSerializable,
	})

}

func (kv *KeyValue) Get(key string) (value string, err error) {
	if kv.Gate != nil {
		kv.Gate.Start()
		defer kv.Gate.Done()
	}
	return get(kv, kv.DB, key)
}

func (kv *KeyValue) Set(key, value string) error {
	if err := sorted.CheckSizes(key, value); err != nil {
		log.Printf("Skipping storing (%q:%q): %v", key, value, err)
		return nil
	}
	if kv.Gate != nil {
		kv.Gate.Start()
		defer kv.Gate.Done()
	}
	if kv.SetFunc != nil {
		return kv.SetFunc(kv.DB, key, value)
	}
	_, err := kv.DB.Exec(kv.sql("REPLACE INTO /*TPRE*/rows (k, v) VALUES (?, ?)"), key, value)
	return err
}

func (kv *KeyValue) Delete(key string) error {
	if kv.Gate != nil {
		kv.Gate.Start()
		defer kv.Gate.Done()
	}
	_, err := kv.DB.Exec(kv.sql("DELETE FROM /*TPRE*/rows WHERE k=?"), key)
	return err
}

// TODO(mpl): implement Wipe for each of the SQLs, as it's done for MySQL, and
// remove this one below.

func (kv *KeyValue) Wipe() error {
	if kv.Gate != nil {
		kv.Gate.Start()
		defer kv.Gate.Done()
	}
	_, err := kv.DB.Exec(kv.sql("DELETE FROM /*TPRE*/rows"))
	return err
}

func (kv *KeyValue) Close() error { return kv.DB.Close() }

// Something we can make queries on. This will either be an *sql.DB or an *sql.Tx.
type queryObject interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// Common logic for KeyValue.Find and batchTx.Find.
func find(kv *KeyValue, qobj queryObject, start, end string) *iter {
	var rows *sql.Rows
	var err error
	if end == "" {
		rows, err = qobj.Query(kv.sql("SELECT k, v FROM /*TPRE*/rows WHERE k >= ? ORDER BY k "), start)
	} else {
		rows, err = qobj.Query(kv.sql("SELECT k, v FROM /*TPRE*/rows WHERE k >= ? AND k < ? ORDER BY k "), start, end)
	}
	if err != nil {
		log.Printf("unexpected query error: %v", err)
		return &iter{err: err}
	}

	return &iter{
		kv:         kv,
		rows:       rows,
		closeCheck: leak.NewChecker(),
	}
}

// Common logic for KeyValue.Get and batchTx.Get
func get(kv *KeyValue, qobj queryObject, key string) (value string, err error) {
	err = qobj.QueryRow(kv.sql("SELECT v FROM /*TPRE*/rows WHERE k=?"), key).Scan(&value)
	if err == sql.ErrNoRows {
		err = sorted.ErrNotFound
	}
	return
}

func (kv *KeyValue) Find(start, end string) sorted.Iterator {
	var releaseGate func() // nil if unused
	if kv.Gate != nil {
		var once sync.Once
		kv.Gate.Start()
		releaseGate = func() {
			once.Do(kv.Gate.Done)
		}
	}
	it := find(kv, kv.DB, start, end)
	it.releaseGate = releaseGate
	return it
}

// iter is a iterator over sorted key/value pairs in rows.
type iter struct {
	kv  *KeyValue
	err error // accumulated error, returned at Close

	closeCheck  *leak.Checker
	releaseGate func() // if non-nil, called on Close

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
	if t.releaseGate != nil {
		t.releaseGate()
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
