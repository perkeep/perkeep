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

package sqlkv

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go4.org/syncutil"
	"perkeep.org/pkg/sorted"
)

// No Execer/Queryer interfaces: database/sql must use native statement handles,
// so these counts distinguish genuine reuse from caching SQL strings alone.
type recordingSQL struct {
	mu sync.Mutex
	recordingStats
	failPrepareAt                           int
	prepareErr, beginErr, execErr, queryErr error
	nextErr, rowsCloseErr, connCloseErr     error
}

type recordingStats struct {
	prepares, commits, rollbacks, rowsClosed int
	statements                               []recordedStatement
}

type recordedStatement struct {
	query                  string
	closes, execs, queries int
	activeRows             int
}

func (r *recordingSQL) stats() recordingStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.recordingStats
	s.statements = append([]recordedStatement(nil), s.statements...)
	return s
}

func (r *recordingSQL) Connect(context.Context) (driver.Conn, error) {
	return &recordingConn{r}, nil
}
func (r *recordingSQL) Driver() driver.Driver { return r }
func (r *recordingSQL) Open(string) (driver.Conn, error) {
	return r.Connect(context.Background())
}

type recordingConn struct{ r *recordingSQL }

func (c *recordingConn) Prepare(query string) (driver.Stmt, error) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	c.r.prepares++
	if c.r.prepares == c.r.failPrepareAt {
		return nil, c.r.prepareErr
	}
	id := len(c.r.statements)
	c.r.statements = append(c.r.statements, recordedStatement{query: query})
	return &recordingStmt{c.r, id}, nil
}
func (c *recordingConn) Close() error { return c.r.connCloseErr }
func (c *recordingConn) Begin() (driver.Tx, error) {
	if c.r.beginErr != nil {
		return nil, c.r.beginErr
	}
	return &recordingTx{c.r}, nil
}
func (c *recordingConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}

type recordingTx struct{ r *recordingSQL }

func (tx *recordingTx) Commit() error {
	tx.r.mu.Lock()
	defer tx.r.mu.Unlock()
	tx.r.commits++
	return nil
}
func (tx *recordingTx) Rollback() error {
	tx.r.mu.Lock()
	defer tx.r.mu.Unlock()
	tx.r.rollbacks++
	return nil
}

type recordingStmt struct {
	r  *recordingSQL
	id int
}

func (s *recordingStmt) Close() error {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	s.r.statements[s.id].closes++
	return nil
}
func (*recordingStmt) NumInput() int { return -1 }
func (s *recordingStmt) Exec([]driver.Value) (driver.Result, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	record := &s.r.statements[s.id]
	record.execs++
	if record.closes != 0 {
		return nil, errors.New("exec on finalized native statement")
	}
	return driver.RowsAffected(1), s.r.execErr
}
func (s *recordingStmt) Query([]driver.Value) (driver.Rows, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	record := &s.r.statements[s.id]
	record.queries++
	if record.closes != 0 || record.activeRows != 0 {
		return nil, errors.New("query on finalized statement or overlapping native cursor")
	}
	if s.r.queryErr != nil {
		return nil, s.r.queryErr
	}
	record.activeRows++
	columns := []string{"v"}
	if strings.HasPrefix(record.query, "SELECT k, v") {
		columns = []string{"k", "v"}
	}
	return &recordingRows{s: s, columns: columns, nextErr: s.r.nextErr, closeErr: s.r.rowsCloseErr}, nil
}

type recordingRows struct {
	s                 *recordingStmt
	columns           []string
	nextErr, closeErr error
	yielded           bool
}

func (r *recordingRows) Columns() []string { return r.columns }
func (r *recordingRows) Close() error {
	r.s.r.mu.Lock()
	defer r.s.r.mu.Unlock()
	r.s.r.rowsClosed++
	r.s.r.statements[r.s.id].activeRows--
	return r.closeErr
}
func (r *recordingRows) Next(values []driver.Value) error {
	if r.yielded {
		if r.nextErr != nil {
			return r.nextErr
		}
		return io.EOF
	}
	r.yielded = true
	values[0] = "value"
	if len(values) == 2 {
		values[0], values[1] = "key", "value"
	}
	return nil
}

func newRecordingKV(t *testing.T) (*KeyValue, *recordingSQL) {
	t.Helper()
	r := new(recordingSQL)
	db := sql.OpenDB(r)
	db.SetMaxOpenConns(1)
	kv := &KeyValue{DB: db, Gate: syncutil.NewGate(1)}
	t.Cleanup(func() { kv.Close() })
	return kv, r
}

func prepareRecordingKV(t *testing.T, kv *KeyValue) {
	t.Helper()
	if err := kv.Prepare(); err != nil {
		t.Fatal(err)
	}
}

func checkNativeStatements(t *testing.T, r *recordingSQL, prepares, closed int) {
	t.Helper()
	s := r.stats()
	if s.prepares != prepares {
		t.Errorf("native Prepare calls = %d; want %d", s.prepares, prepares)
	}
	for i, stmt := range s.statements {
		if stmt.closes != closed {
			t.Errorf("native statement %d (%q) closed %d times; want %d", i, stmt.query, stmt.closes, closed)
		}
	}
}

func consumeRecordingIterator(t *testing.T, it sorted.Iterator) {
	t.Helper()
	if !it.Next() || it.Key() != "key" || it.Value() != "value" {
		t.Error("iterator did not return the driver's row")
	}
	if it.Next() {
		t.Error("unexpected second row")
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
}

// All gate probes run within a synctest bubble, not on scheduler-dependent sleeps.
func probeRecordingGate(g *syncutil.Gate) <-chan struct{} {
	acquired := make(chan struct{})
	go func() {
		g.Start()
		g.Done()
		close(acquired)
	}()
	synctest.Wait()
	return acquired
}

func checkRecordingGateHeld(t *testing.T, acquired <-chan struct{}) {
	t.Helper()
	synctest.Wait()
	select {
	case <-acquired:
		t.Fatal("Gate released before the operation was finished")
	default:
	}
}

func checkRecordingGateReleased(t *testing.T, g *syncutil.Gate, acquired <-chan struct{}) {
	t.Helper()
	synctest.Wait()
	select {
	case <-acquired:
	default:
		// Free the blocked probe even when reporting a regression.
		g.Done()
		synctest.Wait()
		t.Fatal("operation leaked its Gate slot")
	}
}

func checkRecordingResources(t *testing.T, kv *KeyValue) {
	t.Helper()
	checkRecordingGateReleased(t, kv.Gate, probeRecordingGate(kv.Gate))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := kv.DB.Conn(ctx)
	if err != nil {
		t.Fatalf("connection leaked after failure: %v", err)
	}
	conn.Close()
}

func TestPreparedDriverReuse(t *testing.T) {
	kv, r := newRecordingKV(t)
	prepareRecordingKV(t, kv)
	prepareRecordingKV(t, kv) // Idempotent before first use.
	checkNativeStatements(t, r, 5, 0)
	for range 100 {
		if got, err := kv.Get("key"); err != nil || got != "value" {
			t.Fatalf("Get = %q, %v", got, err)
		}
		if err := kv.Set("key", "value"); err != nil {
			t.Fatal(err)
		}
		if err := kv.Delete("key"); err != nil {
			t.Fatal(err)
		}
	}
	consumeRecordingIterator(t, kv.Find("", ""))
	consumeRecordingIterator(t, kv.Find("a", "z"))
	for range 3 {
		b := kv.BeginBatch()
		for range 100 {
			b.Set("key", "value")
			b.Delete("key")
		}
		if err := kv.CommitBatch(b); err != nil {
			t.Fatal(err)
		}
		// Closing Tx.Stmt children must not finalize the DB parent handles.
		checkNativeStatements(t, r, 5, 0)
	}
	s := r.stats()
	execs, queries := 0, 0
	for _, stmt := range s.statements {
		execs += stmt.execs
		queries += stmt.queries
	}
	if s.commits != 3 || execs != 800 || queries != 102 {
		t.Errorf("commit/exec/query counts = %d/%d/%d; want 3/800/102", s.commits, execs, queries)
	}
	if err := kv.Close(); err != nil {
		t.Fatal(err)
	}
	checkNativeStatements(t, r, 5, 1)
	if err := kv.Close(); err != nil {
		t.Fatal(err)
	}
	checkNativeStatements(t, r, 5, 1)
}

func TestPreparedDriverOptIn(t *testing.T) {
	kv, r := newRecordingKV(t)
	if err := kv.Set("key", "value"); err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Get("key"); err != nil {
		t.Fatal(err)
	}
	if err := kv.Delete("key"); err != nil {
		t.Fatal(err)
	}
	consumeRecordingIterator(t, kv.Find("", ""))
	consumeRecordingIterator(t, kv.Find("a", "z"))
	checkNativeStatements(t, r, 5, 1) // Without Prepare, every use is ad-hoc.
}

func TestPreparedDriverTxReadsRemainAdHoc(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "batch", true: "read"}[readOnly], func(t *testing.T) {
			kv, r := newRecordingKV(t)
			prepareRecordingKV(t, kv)
			var rt sorted.ReadTransaction
			var finish func() error
			if readOnly {
				rt = kv.BeginReadTx()
				finish = rt.Close
			} else {
				b := kv.BeginBatch()
				rt = b.(sorted.ReadTransaction)
				finish = func() error { return kv.CommitBatch(b) }
			}
			// Same Find variant twice, while both native cursors are live.
			first, second := rt.Find("", ""), rt.Find("a", "")
			for range 2 {
				if got, err := rt.Get("key"); err != nil || got != "value" {
					t.Errorf("transaction Get = %q, %v", got, err)
				}
			}
			consumeRecordingIterator(t, first)
			consumeRecordingIterator(t, second)
			if err := finish(); err != nil {
				t.Fatal(err)
			}
			s := r.stats()
			if s.prepares != 9 || len(s.statements) != 9 {
				t.Fatalf("native prepares/handles = %d/%d; want 9/9", s.prepares, len(s.statements))
			}
			for i, stmt := range s.statements {
				want := 0
				if i >= 5 {
					want = 1
				}
				if stmt.closes != want {
					t.Errorf("native statement %d closes = %d; want %d", i, stmt.closes, want)
				}
			}
		})
	}
}

func TestPreparedDriverPrepareFailureRetry(t *testing.T) {
	for failAt := 1; failAt <= 5; failAt++ {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				kv, r := newRecordingKV(t)
				wantErr := errors.New("native prepare failure")
				r.failPrepareAt, r.prepareErr = failAt, wantErr
				if err := kv.Prepare(); !errors.Is(err, wantErr) {
					t.Fatalf("Prepare error = %v; want %v", err, wantErr)
				}
				checkNativeStatements(t, r, failAt, 1)
				if got := len(r.stats().statements); got != failAt-1 {
					t.Fatalf("successful handles before failure = %d; want %d", got, failAt-1)
				}
				checkRecordingResources(t, kv)
				prepareRecordingKV(t, kv) // Failure must not poison a sync.Once.
				prepareRecordingKV(t, kv)
				if got := r.stats().prepares; got != failAt+5 {
					t.Fatalf("Prepare calls after retry = %d; want %d", got, failAt+5)
				}
				if _, err := kv.Get("key"); err != nil {
					t.Fatal(err)
				}
				if err := kv.Close(); err != nil {
					t.Fatal(err)
				}
				checkNativeStatements(t, r, failAt+5, 1)
			})
		})
	}
}

func TestPreparedDriverHooksAndRewriting(t *testing.T) {
	for _, hooks := range []string{"none", "set", "batch", "both"} {
		t.Run(hooks, func(t *testing.T) {
			kv, r := newRecordingKV(t)
			kv.TablePrefix = "tenant."
			kv.PlaceHolderFunc = func(q string) string { return strings.ReplaceAll(q, "?", ":arg") }
			setCalls, batchCalls := 0, 0
			if hooks == "set" || hooks == "both" {
				kv.SetFunc = func(*sql.DB, string, string) error { setCalls++; return nil }
			}
			if hooks == "batch" || hooks == "both" {
				kv.BatchSetFunc = func(*sql.Tx, string, string) error { batchCalls++; return nil }
			}
			prepareRecordingKV(t, kv)
			want := 5
			if hooks == "both" {
				want = 4
			}
			for _, stmt := range r.stats().statements {
				if !strings.Contains(stmt.query, "tenant.rows") || strings.Contains(stmt.query, "?") || strings.Contains(stmt.query, "/*TPRE*/") {
					t.Errorf("unrewritten prepared query: %q", stmt.query)
				}
				if hooks == "both" && strings.HasPrefix(stmt.query, "REPLACE") {
					t.Error("prepared unused REPLACE with both hooks installed")
				}
			}
			if err := kv.Set("key", "value"); err != nil {
				t.Fatal(err)
			}
			b := kv.BeginBatch()
			b.Set("key", "value")
			b.Delete("key")
			if err := kv.CommitBatch(b); err != nil {
				t.Fatal(err)
			}
			if (kv.SetFunc != nil && setCalls != 1) || (kv.BatchSetFunc != nil && batchCalls != 1) {
				t.Errorf("hook calls: Set %d, BatchSet %d", setCalls, batchCalls)
			}
			checkNativeStatements(t, r, want, 0)
			kv.Close()
			checkNativeStatements(t, r, want, 1)
		})
	}
}

func TestPreparedDriverBeginFailure(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "batch", true: "read"}[readOnly], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				kv, r := newRecordingKV(t)
				prepareRecordingKV(t, kv)
				wantErr := errors.New("native begin failure")
				r.beginErr = wantErr
				var finish func() error
				if readOnly {
					rt := kv.BeginReadTx()
					if _, err := rt.Get("key"); !errors.Is(err, wantErr) {
						t.Errorf("failed transaction Get error = %v", err)
					}
					it := rt.Find("", "")
					if it.Next() {
						t.Error("failed transaction iterator returned a row")
					}
					if err := it.Close(); !errors.Is(err, wantErr) {
						t.Errorf("failed transaction iterator Close error = %v", err)
					}
					finish = rt.Close
				} else {
					b := kv.BeginBatch()
					b.Set("key", "value")
					b.Delete("key")
					finish = func() error { return kv.CommitBatch(b) }
				}
				if err := finish(); !errors.Is(err, wantErr) {
					t.Errorf("finish error = %v; want %v", err, wantErr)
				}
				finish() // Must not panic on nil Tx or release the Gate twice.
				if s := r.stats(); s.commits != 0 || s.rollbacks != 0 {
					t.Errorf("failed Begin finished a native transaction: %+v", s)
				}
				checkRecordingResources(t, kv)
				r.beginErr = nil
				if err := kv.CommitBatch(kv.BeginBatch()); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestPreparedDriverBatchExecFailure(t *testing.T) {
	for _, mutation := range []string{"set", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				kv, r := newRecordingKV(t)
				prepareRecordingKV(t, kv)
				wantErr := errors.New("native exec failure")
				r.execErr = wantErr
				b := kv.BeginBatch()
				if mutation == "set" {
					b.Set("key", "value")
				} else {
					b.Delete("key")
				}
				b.Set("ignored", "ignored")
				b.Delete("ignored")
				if err := kv.CommitBatch(b); !errors.Is(err, wantErr) {
					t.Errorf("CommitBatch error = %v; want %v", err, wantErr)
				}
				kv.CommitBatch(b)
				s := r.stats()
				execs := 0
				for _, stmt := range s.statements {
					execs += stmt.execs
				}
				if s.rollbacks != 1 || s.commits != 0 || execs != 1 {
					t.Errorf("rollback/commit/exec counts = %d/%d/%d; want 1/0/1", s.rollbacks, s.commits, execs)
				}
				checkNativeStatements(t, r, 5, 0)
				checkRecordingResources(t, kv)
				r.execErr = nil
				if err := kv.Set("key", "value"); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestPreparedDriverFinishOnce(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "batch", true: "read"}[readOnly], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				kv, r := newRecordingKV(t)
				prepareRecordingKV(t, kv)
				var finish func() error
				var b sorted.BatchMutation
				if readOnly {
					finish = kv.BeginReadTx().Close
				} else {
					b = kv.BeginBatch()
					finish = func() error { return kv.CommitBatch(b) }
				}
				probe := probeRecordingGate(kv.Gate)
				checkRecordingGateHeld(t, probe)
				if err := finish(); err != nil {
					t.Error(err)
				}
				checkRecordingGateReleased(t, kv.Gate, probe)
				finish()
				if b != nil {
					b.Set("finished", "finished")
					b.Delete("finished")
					if err := kv.CommitBatch(b); !errors.Is(err, sql.ErrTxDone) {
						t.Errorf("post-finish mutation error = %v; want ErrTxDone", err)
					}
					for _, stmt := range r.stats().statements {
						if stmt.execs != 0 {
							t.Error("post-finish mutation reached native driver")
						}
					}
				}
				if s := r.stats(); s.commits != 1 || s.rollbacks != 0 {
					t.Errorf("double finish reached driver: commits %d, rollbacks %d", s.commits, s.rollbacks)
				}
				checkRecordingResources(t, kv)
			})
		})
	}
}

type foreignRecordingBatch struct{}

func (foreignRecordingBatch) Set(string, string) {}
func (foreignRecordingBatch) Delete(string)      {}

func TestPreparedDriverForeignBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kv, r := newRecordingKV(t)
		other, otherR := newRecordingKV(t)
		prepareRecordingKV(t, kv)
		prepareRecordingKV(t, other)
		own, foreign := kv.BeginBatch(), other.BeginBatch()
		probe := probeRecordingGate(kv.Gate)
		checkRecordingGateHeld(t, probe)
		for _, b := range []sorted.BatchMutation{foreignRecordingBatch{}, foreign, (*batchTx)(nil)} {
			if err := kv.CommitBatch(b); err == nil {
				t.Error("accepted a foreign batch")
			}
			checkRecordingGateHeld(t, probe)
		}
		if s, otherS := r.stats(), otherR.stats(); s.commits+s.rollbacks+otherS.commits+otherS.rollbacks != 0 {
			t.Error("foreign CommitBatch finished an unrelated native transaction")
		}
		if err := kv.CommitBatch(own); err != nil {
			t.Error(err)
		}
		checkRecordingGateReleased(t, kv.Gate, probe)
		if err := other.CommitBatch(foreign); err != nil {
			t.Fatal(err)
		}
		checkRecordingResources(t, kv)
		checkRecordingResources(t, other)
	})
}

func TestPreparedDriverOperationFailures(t *testing.T) {
	for _, operation := range []string{"get", "set", "delete", "find-query", "find-next", "find-close"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				kv, r := newRecordingKV(t)
				prepareRecordingKV(t, kv)
				wantErr := errors.New("injected " + operation + " failure")
				var err error
				switch operation {
				case "get":
					r.queryErr = wantErr
					_, err = kv.Get("key")
				case "set":
					r.execErr = wantErr
					err = kv.Set("key", "value")
				case "delete":
					r.execErr = wantErr
					err = kv.Delete("key")
				default:
					switch operation {
					case "find-query":
						r.queryErr = wantErr
					case "find-next":
						r.nextErr = wantErr
					case "find-close":
						r.rowsCloseErr = wantErr
					}
					it := kv.Find("", "")
					if operation != "find-query" {
						probe := probeRecordingGate(kv.Gate)
						checkRecordingGateHeld(t, probe)
						if !it.Next() {
							t.Error("missing first iterator row")
						}
						if operation == "find-next" && it.Next() {
							t.Error("Next succeeded despite driver error")
						}
						err = it.Close()
						checkRecordingGateReleased(t, kv.Gate, probe)
					} else {
						if it.Next() {
							t.Error("iterator returned a row despite query failure")
						}
						err = it.Close()
					}
					// Error and Gate release remain stable on repeated Close.
					if again := it.Close(); !errors.Is(again, wantErr) {
						t.Errorf("second iterator Close error = %v; want %v", again, wantErr)
					}
				}
				if !errors.Is(err, wantErr) {
					t.Errorf("operation error = %v; want %v", err, wantErr)
				}
				checkRecordingResources(t, kv)
				checkNativeStatements(t, r, 5, 0)
			})
		})
	}
}

func TestPreparedDriverCloseFailure(t *testing.T) {
	kv, r := newRecordingKV(t)
	prepareRecordingKV(t, kv)
	wantErr := errors.New("native connection close failure")
	r.connCloseErr = wantErr
	if err := kv.Close(); !errors.Is(err, wantErr) {
		t.Errorf("Close error = %v; want %v", err, wantErr)
	}
	checkNativeStatements(t, r, 5, 1)
}
