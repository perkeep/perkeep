/*
Copyright 2026 The Perkeep Authors.

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

package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"

	"perkeep.org/pkg/sorted"
)

const (
	reindexMaxMutations = 1024
	reindexMaxBytes     = 4 << 20

	reindexGetSQL    = "SELECT v FROM rows WHERE k=?"
	reindexSetSQL    = "REPLACE INTO rows (k, v) VALUES (?, ?)"
	reindexDeleteSQL = "DELETE FROM rows WHERE k=?"
)

var (
	_ sorted.Reindexer      = (*keyValue)(nil)
	_ sorted.ReindexSession = (*reindexSession)(nil)
	_ sorted.BatchMutation  = (*reindexBatch)(nil)
	_ sorted.Iterator       = (*reindexIterator)(nil)
)

// BeginReindex reserves the store until Finish or Close. The initial wipe and
// incomplete-reindex marker are committed together before any bulk writes.
// Neither the journal mode nor the synchronous setting is changed.
func (kv *keyValue) BeginReindex() (sorted.ReindexSession, error) {
	kv.Gate.Start()
	conn, err := kv.db.Conn(context.Background())
	if err != nil {
		kv.Gate.Done()
		return nil, err
	}
	s := &reindexSession{
		owner:        kv,
		conn:         conn,
		maxMutations: reindexMaxMutations,
		maxBytes:     reindexMaxBytes,
	}
	s.currentTx, err = conn.BeginTx(context.Background(), nil)
	if err == nil {
		_, err = s.currentTx.Exec("DELETE FROM rows")
	}
	if err == nil {
		_, err = s.currentTx.Exec("REPLACE INTO meta (metakey, value) VALUES ('reindexing', '1')")
	}
	if err == nil {
		err = s.commitLocked()
	}
	if err != nil {
		s.failLocked(err)
		s.Finish(false)
		return nil, fmt.Errorf("sqlite: beginning reindex: %w", err)
	}
	return s, nil
}

// reindexSession deliberately does not embed keyValue: all reads and writes
// must use the current transaction without trying to acquire the parent's gate.
// In particular, it does not implement sorted.TransactionalReader.
type reindexSession struct {
	mu    sync.Mutex
	owner *keyValue
	conn  *sql.Conn

	currentTx                    *sql.Tx
	getStmt, setStmt, deleteStmt *sql.Stmt // closed by currentTx
	mutations, bytes             int
	maxMutations, maxBytes       int

	err             error // first SQL error, even if the caller ignores it
	done            bool
	finishedSuccess bool
	finishErr       error
}

func (s *reindexSession) activeLocked() error {
	if s.err != nil {
		return s.err
	}
	if s.done {
		return sql.ErrTxDone
	}
	return nil
}

func (s *reindexSession) failLocked(err error) error {
	if err != nil && s.err == nil {
		s.err = err
	}
	return s.err
}

func (s *reindexSession) ensureTxLocked() error {
	if err := s.activeLocked(); err != nil {
		return err
	}
	if s.currentTx != nil {
		return nil
	}
	tx, err := s.conn.BeginTx(context.Background(), nil)
	if err != nil {
		return s.failLocked(err)
	}
	s.currentTx = tx
	for _, q := range []struct {
		dst **sql.Stmt
		sql string
	}{
		{&s.getStmt, reindexGetSQL},
		{&s.setStmt, reindexSetSQL},
		{&s.deleteStmt, reindexDeleteSQL},
	} {
		stmt, err := tx.Prepare(q.sql)
		if err != nil {
			return s.failLocked(err)
		}
		*q.dst = stmt
	}
	return nil
}

// discardSQLiteConn is necessary after a failed COMMIT: database/sql marks the
// Tx done even when SQLite's native transaction is still active. A Rollback on
// that Tx can no longer abort it, and returning the connection to the pool would
// allow unrelated work to run inside the failed transaction.
func discardSQLiteConn(conn *sql.Conn) {
	conn.Raw(func(any) error { return driver.ErrBadConn })
	conn.Close()
}

func (s *reindexSession) clearTxLocked() {
	s.currentTx = nil
	s.getStmt, s.setStmt, s.deleteStmt = nil, nil, nil
	s.mutations, s.bytes = 0, 0
}

func (s *reindexSession) commitLocked() error {
	tx := s.currentTx
	err := tx.Commit()
	s.clearTxLocked()
	if err != nil {
		tx.Rollback() // best effort; Commit normally already marked the Tx done
		discardSQLiteConn(s.conn)
		s.conn = nil
		return s.failLocked(err)
	}
	return nil
}

func (s *reindexSession) rollbackLocked() {
	if s.currentTx == nil {
		return
	}
	err := s.currentTx.Rollback()
	s.clearTxLocked()
	if err != nil {
		s.failLocked(err)
		discardSQLiteConn(s.conn)
		s.conn = nil
	}
}

func (s *reindexSession) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureTxLocked(); err != nil {
		return "", err
	}
	var value string
	err := s.getStmt.QueryRow(key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", sorted.ErrNotFound
	}
	if err != nil {
		return "", s.failLocked(err)
	}
	return value, nil
}

func (s *reindexSession) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.activeLocked(); err != nil {
		return err
	}
	// Match sqlkv's behavior: oversize plain and batched sets are skipped.
	if sorted.CheckSizes(key, value) != nil {
		return nil
	}
	return s.applyLocked([]reindexMutation{{key: key, value: value}}, len(key)+len(value))
}

func (s *reindexSession) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.activeLocked(); err != nil {
		return err
	}
	return s.applyLocked([]reindexMutation{{key: key, delete: true}}, len(key))
}

type reindexMutation struct {
	key, value string
	delete     bool
}

// applyLocked commits only between logical batches, never within one. An
// oversize logical batch gets a transaction of its own. Ordinary groups stay
// within the mutation and byte budgets.
func (s *reindexSession) applyLocked(mutations []reindexMutation, bytes int) error {
	if len(mutations) == 0 {
		return nil
	}
	if s.mutations != 0 &&
		(s.mutations+len(mutations) > s.maxMutations || s.bytes+bytes > s.maxBytes) {
		if err := s.commitLocked(); err != nil {
			return err
		}
	}
	if err := s.ensureTxLocked(); err != nil {
		return err
	}
	for _, m := range mutations {
		var err error
		if m.delete {
			_, err = s.deleteStmt.Exec(m.key)
		} else {
			_, err = s.setStmt.Exec(m.key, m.value)
		}
		if err != nil {
			return s.failLocked(err)
		}
	}
	s.mutations += len(mutations)
	s.bytes += bytes
	if s.mutations >= s.maxMutations || s.bytes >= s.maxBytes {
		return s.commitLocked()
	}
	return nil
}

// reindexBatch is an in-memory logical batch. Its state, like the transaction,
// is protected by the owning session's mutex.
type reindexBatch struct {
	owner     *reindexSession
	mutations []reindexMutation
	bytes     int
	err       error
	done      bool
}

func (s *reindexSession) BeginBatch() sorted.BatchMutation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &reindexBatch{owner: s, err: s.activeLocked()}
}

func (b *reindexBatch) activeLocked() bool {
	if b.done {
		b.err = sql.ErrTxDone
	}
	if b.err == nil {
		b.err = b.owner.activeLocked()
	}
	return b.err == nil
}

func (b *reindexBatch) Set(key, value string) {
	b.owner.mu.Lock()
	defer b.owner.mu.Unlock()
	if !b.activeLocked() || sorted.CheckSizes(key, value) != nil {
		return
	}
	b.mutations = append(b.mutations, reindexMutation{key: key, value: value})
	b.bytes += len(key) + len(value)
}

func (b *reindexBatch) Delete(key string) {
	b.owner.mu.Lock()
	defer b.owner.mu.Unlock()
	if !b.activeLocked() {
		return
	}
	b.mutations = append(b.mutations, reindexMutation{key: key, delete: true})
	b.bytes += len(key)
}

func (s *reindexSession) CommitBatch(batch sorted.BatchMutation) error {
	b, ok := batch.(*reindexBatch)
	if !ok || b == nil || b.owner != s {
		return fmt.Errorf("BatchMutation %T does not belong to this reindex session", batch)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if b.done {
		return sql.ErrTxDone
	}
	b.done = true
	defer func() { b.mutations = nil }()
	if err := s.activeLocked(); err != nil {
		return err
	}
	if b.err != nil {
		return b.err
	}
	return s.applyLocked(b.mutations, b.bytes)
}

// Find copies its complete result set while holding the session mutex and
// closes the SQL cursor before returning. Its memory usage is proportional to
// the result set (not bounded by the write-group budgets). This allows nested
// and overlapping iterators, and transaction rollover with an iterator open,
// without reserving the connection or session mutex.
func (s *reindexSession) Find(start, end string) sorted.Iterator {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureTxLocked(); err != nil {
		return &reindexIterator{err: err}
	}
	var rows *sql.Rows
	var err error
	if end == "" {
		rows, err = s.currentTx.Query("SELECT k, v FROM rows WHERE k >= ? ORDER BY k", start)
	} else {
		rows, err = s.currentTx.Query("SELECT k, v FROM rows WHERE k >= ? AND k < ? ORDER BY k", start, end)
	}
	if err != nil {
		return &reindexIterator{err: s.failLocked(err)}
	}
	var result []reindexRow
	for rows.Next() {
		var r reindexRow
		if err = rows.Scan(&r.key, &r.value); err != nil {
			break
		}
		result = append(result, r) // Scan into strings copies driver-owned data
	}
	if err == nil {
		err = rows.Err()
	}
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return &reindexIterator{err: s.failLocked(err)}
	}
	return &reindexIterator{rows: result, pos: -1}
}

// Finish is idempotent; the first call determines the outcome. An aborted
// session cannot subsequently be promoted to success. Failed or aborted
// sessions leave the durable marker and require a fresh rebuild.
func (s *reindexSession) Finish(success bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		if success && !s.finishedSuccess && s.finishErr == nil {
			return sql.ErrTxDone
		}
		return s.finishErr
	}
	if success && s.err == nil {
		// Even an empty reindex needs a final transaction to remove the marker.
		if s.ensureTxLocked() == nil {
			_, err := s.currentTx.Exec("DELETE FROM meta WHERE metakey='reindexing'")
			if err != nil {
				s.failLocked(err)
			} else {
				s.commitLocked()
			}
		}
	}
	s.rollbackLocked()
	if s.conn != nil {
		s.failLocked(s.conn.Close())
		s.conn = nil
	}
	s.done = true
	s.finishedSuccess = success && s.err == nil
	s.finishErr = s.err
	s.owner.Gate.Done()
	return s.finishErr
}

// Close aborts only this session, not the parent database.
func (s *reindexSession) Close() error { return s.Finish(false) }

type reindexRow struct{ key, value string }

type reindexIterator struct {
	rows   []reindexRow
	pos    int
	err    error
	closed bool
}

func (it *reindexIterator) Next() bool {
	if it.closed || it.err != nil || it.pos+1 >= len(it.rows) {
		return false
	}
	it.pos++
	return true
}

func (it *reindexIterator) Key() string        { return it.rows[it.pos].key }
func (it *reindexIterator) Value() string      { return it.rows[it.pos].value }
func (it *reindexIterator) KeyBytes() []byte   { return []byte(it.Key()) }
func (it *reindexIterator) ValueBytes() []byte { return []byte(it.Value()) }
func (it *reindexIterator) Close() error {
	it.closed = true
	it.rows = nil
	return it.err
}
