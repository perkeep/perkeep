// Copyright 2014 The dbm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dbm

//DONE +Top level Sync? Optional? (Measure it)
//	Too slow. Added db.Sync() instead.

//DONE user defined collating
//	- on DB create (sets the default)
//	- per Array? (probably a MUST HAVE feature)
//----
//	After Go will support Unicode locale collating. But that would have
//	to bee a too different API then. (package udbm?)

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"camlistore.org/third_party/github.com/cznic/exp/lldb"
	"camlistore.org/third_party/github.com/cznic/fileutil"
)

const (
	aCacheSize = 500
	fCacheSize = 500
	sCacheSize = 50

	rname        = "2remove" // Array shredder queue
	arraysPrefix = 'A'
	filesPrefix  = 'F'
	systemPrefix = 'S'

	magic = "\x60\xdb\xf1\x1e"
)

// Test hooks
var (
	compress      = true // Dev hook
	activeVictors int32
)

const (
	stDisabled = iota // stDisabled must be zero
	stIdle
	stCollecting
	stIdleArmed
	stCollectingArmed
	stCollectingTriggered
	stEndUpdateFailed
)

func init() {
	if stDisabled != 0 {
		panic("stDisabled != 0")
	}
}

type DB struct {
	_root         *Array          // Root directory, do not access directly
	acache        treeCache       // Arrays cache
	acidNest      int             // Grace period nesting level
	acidState     int             // Grace period FSM state.
	acidTimer     *time.Timer     // Grace period timer
	alloc         *lldb.Allocator // The machinery. Wraps filer
	bkl           sync.Mutex      // Big Kernel Lock
	closeMu       sync.Mutex      // Close() coordination
	closed        chan bool
	emptySize     int64         // Any header size including FLT.
	f             *os.File      // Underlying file. Potentially nil (if filer is lldb.MemFiler)
	fcache        treeCache     // Files cache
	filer         lldb.Filer    // Wraps f
	gracePeriod   time.Duration // WAL grace period
	isMem         bool          // No signal capture
	lastCommitErr error
	lock          *os.File       // The DB file lock
	removing      map[int64]bool // BTrees being removed
	removingMu    sync.Mutex     // Remove() coordination
	scache        treeCache      // System arrays cache
	stop          chan int       // Remove() coordination
	wg            sync.WaitGroup // Remove() coordination
	xact          bool           // Updates are made within automatic structural transactions
}

// Create creates the named DB file mode 0666 (before umask). The file must not
// already exist. If successful, methods on the returned DB can be used for
// I/O; the associated file descriptor has mode os.O_RDWR. If there is an
// error, it will be of type *os.PathError.
//
// For the meaning of opts please see documentation of Options.
func Create(name string, opts *Options) (db *DB, err error) {
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return
	}

	return create(f, lldb.NewSimpleFileFiler(f), opts, false)
}

func create(f *os.File, filer lldb.Filer, opts *Options, isMem bool) (db *DB, err error) {
	defer func() {
		lock := opts.lock
		if err != nil && lock != nil {
			n := lock.Name()
			lock.Close()
			os.Remove(n)
			db = nil
		}
	}()

	if err = opts.check(filer.Name(), true, !isMem); err != nil {
		return
	}

	b := [16]byte{byte(magic[0]), byte(magic[1]), byte(magic[2]), byte(magic[3]), 0x00} // ver 0x00
	if n, err := filer.WriteAt(b[:], 0); n != 16 {
		return nil, &os.PathError{Op: "dbm.Create.WriteAt", Path: filer.Name(), Err: err}
	}

	db = &DB{emptySize: 128, f: f, lock: opts.lock, closed: make(chan bool)}

	if filer, err = opts.acidFiler(db, filer); err != nil {
		return nil, err
	}

	db.filer = filer
	if err = filer.BeginUpdate(); err != nil {
		return
	}

	defer func() {
		if e := filer.EndUpdate(); e != nil {
			if err == nil {
				err = e
			}
		}
	}()

	if db.alloc, err = lldb.NewAllocator(lldb.NewInnerFiler(filer, 16), &lldb.Options{}); err != nil {
		return nil, &os.PathError{Op: "dbm.Create", Path: filer.Name(), Err: err}
	}

	db.alloc.Compress = compress
	db.isMem = isMem
	return db, db.boot()
}

// CreateMem creates an in-memory DB not backed by a disk file.  Memory DBs are
// resource limited as they are completely held in memory and are not
// automatically persisted.
//
// For the meaning of opts please see documentation of Options.
func CreateMem(opts *Options) (db *DB, err error) {
	f := lldb.NewMemFiler()
	if opts.ACID == ACIDFull {
		opts.ACID = ACIDTransactions
	}
	return create(nil, f, opts, true)
}

// CreateTemp creates a new temporary DB in the directory dir with a basename
// beginning with prefix and name ending in suffix. If dir is the empty string,
// CreateTemp uses the default directory for temporary files (see os.TempDir).
// Multiple programs calling CreateTemp simultaneously will not choose the same
// file name for the DB. The caller can use Name() to find the pathname of the
// DB file. It is the caller's responsibility to remove the file when no longer
// needed.
//
// For the meaning of opts please see documentation of Options.
func CreateTemp(dir, prefix, suffix string, opts *Options) (db *DB, err error) {
	f, err := fileutil.TempFile(dir, prefix, suffix)
	if err != nil {
		return
	}

	return create(f, lldb.NewSimpleFileFiler(f), opts, false)
}

// Open opens the named DB file for reading/writing. If successful, methods on
// the returned DB can be used for I/O; the associated file descriptor has mode
// os.O_RDWR. If there is an error, it will be of type *os.PathError.
//
// For the meaning of opts please see documentation of Options.
func Open(name string, opts *Options) (db *DB, err error) {
	defer func() {
		lock := opts.lock
		if err != nil && lock != nil {
			n := lock.Name()
			lock.Close()
			os.Remove(n)
			db = nil
		}
		if err != nil {
			if db != nil {
				db.Close()
				db = nil
			}
		}
	}()

	if err = opts.check(name, false, true); err != nil {
		return
	}

	f, err := os.OpenFile(name, os.O_RDWR, 0666)
	if err != nil {
		return
	}

	filer := lldb.Filer(lldb.NewSimpleFileFiler(f))
	sz, err := filer.Size()
	if err != nil {
		return
	}

	if sz%16 != 0 {
		return nil, &os.PathError{Op: "dbm.Open:", Path: name, Err: fmt.Errorf("file size %d(%#x) is not 0 (mod 16)", sz, sz)}
	}

	var b [16]byte
	if n, err := filer.ReadAt(b[:], 0); n != 16 || err != nil {
		return nil, &os.PathError{Op: "dbm.Open.ReadAt", Path: name, Err: err}
	}

	var h header
	if err = h.rd(b[:]); err != nil {
		return nil, &os.PathError{Op: "dbm.Open:validate header", Path: name, Err: err}
	}

	db = &DB{f: f, lock: opts.lock, closed: make(chan bool)}
	if filer, err = opts.acidFiler(db, filer); err != nil {
		return nil, err
	}

	db.filer = filer
	switch h.ver {
	default:
		return nil, &os.PathError{Op: "dbm.Open", Path: name, Err: fmt.Errorf("unknown dbm file format version %#x", h.ver)}
	case 0x00:
		return open00(name, db)
	}

}

// Close closes the DB, rendering it unusable for I/O. It returns an error, if
// any. Failing to call Close before exiting a program can render the DB
// unusable or, in case of using WAL/2PC, the last committed transaction may
// get lost.
//
// Close is idempotent.
func (db *DB) Close() (err error) {
	if err = db.enter(); err != nil {
		return
	}

	doLeave := true
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		if doLeave {
			db.leave(&err)
		}
	}()

	db.closeMu.Lock()
	defer db.closeMu.Unlock()

	select {
	case _ = <-db.closed:
		return
	default:
	}

	defer close(db.closed)

	if db.acidTimer != nil {
		db.acidTimer.Stop()
	}

	var e error
	for db.acidNest > 0 {
		db.acidNest--
		if err := db.filer.EndUpdate(); err != nil {
			e = err
		}
	}
	err = e

	doLeave = false
	e = db.leave(&err)
	if err = db.close(); err == nil {
		err = e
	}

	if lock := db.lock; lock != nil {
		n := lock.Name()
		e1 := lock.Close()
		db.lock = nil
		e2 := os.Remove(n)
		if err == nil {
			err = e1
		}
		if err == nil {
			err = e2
		}
	}
	return
}

func (db *DB) close() (err error) {
	if db.stop != nil {
		close(db.stop)
		db.wg.Wait()
		db.stop = nil
	}

	if db.f == nil { // lldb.MemFiler
		return
	}

	err = db.filer.Sync()
	if err2 := db.filer.Close(); err2 != nil && err == nil {
		err = err2
	}
	return
}

func (db *DB) root() (r *Array, err error) {
	if r = db._root; r != nil {
		return
	}

	sz, err := db.filer.Size()
	if err != nil {
		return
	}

	switch {
	case sz < db.emptySize:
		panic(fmt.Errorf("internal error: %d", sz))
	case sz == db.emptySize:
		tree, h, err := lldb.CreateBTree(db.alloc, collate)
		if err != nil {
			return nil, err
		}

		if h != 1 {
			panic("internal error")
		}

		r = &Array{db, tree, nil, "", 0}
		db._root = r
		return r, nil
	default:
		tree, err := lldb.OpenBTree(db.alloc, collate, 1)
		if err != nil {
			return nil, err
		}

		r = &Array{db, tree, nil, "", 0}
		db._root = r
		return r, nil
	}
}

// Array returns an Array associated with a subtree of array, determined by
// subscripts.
func (db *DB) Array(array string, subscripts ...interface{}) (a Array, err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	return db.array_(false, array, subscripts...)
}

func (db *DB) array_(canCreate bool, array string, subscripts ...interface{}) (a Array, err error) {
	a.db = db
	if a, err = a.array(subscripts...); err != nil {
		return
	}
	a.tree, err = db.acache.getTree(db, arraysPrefix, array, canCreate, aCacheSize)
	a.name = array
	a.namespace = arraysPrefix
	return
}

func (db *DB) sysArray(canCreate bool, array string) (a Array, err error) {
	a.db = db
	a.tree, err = db.scache.getTree(db, systemPrefix, array, canCreate, sCacheSize)
	a.name = array
	a.namespace = systemPrefix
	return a, err
}

func (db *DB) fileArray(canCreate bool, name string) (f File, err error) {
	var a Array
	a.db = db
	a.tree, err = db.fcache.getTree(db, filesPrefix, name, canCreate, fCacheSize)
	a.name = name
	a.namespace = filesPrefix
	return File(a), err
}

// Set sets the value at subscripts in array. Any previous value, if existed,
// is overwritten by the new one.
func (db *DB) Set(value interface{}, array string, subscripts ...interface{}) (err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	a, err := db.array_(true, array, subscripts...)
	if err != nil {
		return
	}

	return a.set(value)
}

// Get returns the value at subscripts in array, or nil if no such value
// exists.
func (db *DB) Get(array string, subscripts ...interface{}) (value interface{}, err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	a, err := db.array_(false, array, subscripts...)
	if a.tree == nil || err != nil {
		return
	}

	return a.get()
}

// Slice returns a new Slice of array, with a subscripts range of [from, to].
// If from is nil it works as 'from lowest existing key'.  If to is nil it
// works as 'to highest existing key'.
func (db *DB) Slice(array string, subscripts, from, to []interface{}) (s *Slice, err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	a, err := db.array_(false, array, subscripts...)
	if a.tree == nil || err != nil {
		return
	}

	return a.Slice(from, to)
}

// Delete deletes the value at subscripts in array.
func (db *DB) Delete(array string, subscripts ...interface{}) (err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	a, err := db.array_(false, array, subscripts...)
	if a.tree == nil || err != nil {
		return
	}

	return a.delete(subscripts...)
}

// Clear empties the subtree at subscripts in array.
func (db *DB) Clear(array string, subscripts ...interface{}) (err error) {
	if err = db.enter(); err != nil {
		return
	}

	doLeave := true
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		if doLeave {
			db.leave(&err)
		}
	}()

	a, err := db.array_(false, array, subscripts...)
	if a.tree == nil || err != nil {
		return
	}

	doLeave = false
	e := db.leave(&err)
	if err = a.Clear(); err == nil {
		err = e
	}
	return
}

// Name returns the name of the DB file.
func (db *DB) Name() string {
	return db.filer.Name()
}

// Size returns the size of the DB file.
func (db *DB) Size() (sz int64, err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	return db.filer.Size()
}

func (db *DB) setRemoving(h int64, flag bool) (r bool) {
	db.removingMu.Lock()
	defer db.removingMu.Unlock()

	if db.removing == nil {
		db.removing = map[int64]bool{h: flag}
		return
	}

	r = db.removing[h]
	switch flag {
	case true:
		db.removing[h] = flag
	case false:
		delete(db.removing, h)
	}
	return
}

// RemoveArray removes array from the DB.
func (db *DB) RemoveArray(array string) (err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	return db.removeArray(arraysPrefix, array)
}

// RemoveFile removes file from the DB.
func (db *DB) RemoveFile(file string) (err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	return db.removeArray(filesPrefix, file)
}

func (db *DB) removeArray(prefix int, array string) (err error) {
	if db.stop == nil {
		db.stop = make(chan int)
	}

	t, err := db.acache.getTree(db, prefix, array, false, aCacheSize)
	if t == nil || err != nil {
		return
	}

	h := t.Handle()
	if db.setRemoving(h, true) {
		return
	}

	delete(db.acache, array)

	root, err := db.root()
	if err != nil {
		return
	}

	removes, err := db.sysArray(true, rname)
	if err != nil {
		return
	}

	if err = removes.set(nil, h); err != nil {
		return
	}

	if err = root.delete(prefix, array); err != nil {
		return
	}

	db.wg.Add(1)
	go db.victor(removes, h)

	return
}

func (db *DB) boot() (err error) {
	const tmp = "/tmp/"

	aa, err := db.Arrays()
	if err != nil {
		return
	}

	s, err := aa.Slice([]interface{}{tmp}, nil)
	if err = noEof(err); err != nil {
		return
	}

	s.Do(func(subscripts, value []interface{}) (r bool, err error) {
		k := subscripts[0].(string)
		if !strings.HasPrefix(k, tmp) {
			return false, nil
		}

		return true, db.RemoveArray(k)

	})

	ff, err := db.Files()
	if err != nil {
		return
	}

	s, err = ff.Slice([]interface{}{tmp}, nil)
	if err = noEof(err); err != nil {
		return
	}

	s.Do(func(subscripts, value []interface{}) (r bool, err error) {
		k := subscripts[0].(string)
		if !strings.HasPrefix(k, tmp) {
			return false, nil
		}

		return true, db.RemoveFile(k)

	})

	removes, err := db.sysArray(false, rname)
	if removes.tree == nil || err != nil {
		return
	}

	s, err = removes.Slice(nil, nil)
	if err = noEof(err); err != nil {
		return
	}

	var a []int64
	s.Do(func(subscripts, value []interface{}) (r bool, err error) {
		r = true
		switch {
		case len(subscripts) == 1:
			h, ok := subscripts[0].(int64)
			if ok {
				a = append(a, h)
				return
			}

			fallthrough
		default:
			err = removes.Delete(subscripts)
			return
		}
	})

	if db.stop == nil {
		db.stop = make(chan int)
	}

	for _, h := range a {
		if db.setRemoving(h, true) {
			continue
		}

		db.wg.Add(1)
		go db.victor(removes, h)
	}
	return
}

func (db *DB) victor(removes Array, h int64) {
	atomic.AddInt32(&activeVictors, 1)
	var err error
	var finished bool
	defer func() {
		if finished {
			func() {
				db.enter()

				defer func() {
					if e := recover(); e != nil {
						err = fmt.Errorf("%v", e)
					}
					db.leave(&err)
				}()

				lldb.RemoveBTree(db.alloc, h)
				removes.delete(h)
				db.setRemoving(h, false)
			}()
		}
		db.wg.Done()
		atomic.AddInt32(&activeVictors, -1)
	}()

	db.enter()

	doLeave := true
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		if doLeave {
			db.leave(&err)
		}
	}()

	t, err := lldb.OpenBTree(db.alloc, collate, h)
	if err != nil {
		finished = true
		return
	}

	doLeave = false
	if db.leave(&err) != nil {
		return
	}

	for {
		runtime.Gosched()
		select {
		case _, ok := <-db.stop:
			if !ok {
				return
			}
		default:
		}

		db.enter()
		doLeave = true
		if finished, err = t.DeleteAny(); finished || err != nil {
			return
		}

		doLeave = false
		if db.leave(&err) != nil {
			return
		}
	}
}

// Arrays returns a read-only meta array which registers other arrays by name
// as its keys. The associated values are meaningless but non-nil if the value
// exists.
func (db *DB) Arrays() (a Array, err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	p, err := db.root()
	if err != nil {
		return a, err
	}

	return p.array(arraysPrefix)
}

// Files returns a read-only meta array which registers all Files in the DB by
// name as its keys. The associated values are meaningless but non-nil if the
// value exists.
func (db *DB) Files() (a Array, err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	p, err := db.root()
	if err != nil {
		return a, err
	}

	return p.array(filesPrefix)
}

func (db *DB) enter() (err error) {
	db.bkl.Lock()
	switch db.acidState {
	default:
		panic("internal error")
	case stDisabled:
		// nop
	case stIdle:
		if err = db.filer.BeginUpdate(); err != nil {
			return
		}

		db.acidNest = 1
		db.acidTimer = time.AfterFunc(db.gracePeriod, db.timeout)
		db.acidState = stCollecting
	case stCollecting:
		db.acidNest++
	case stIdleArmed:
		db.acidNest = 1
		db.acidState = stCollectingArmed
	case stCollectingArmed:
		db.acidNest++
	case stCollectingTriggered:
		db.acidNest++
	case stEndUpdateFailed:
		return db.leave(&err)
	}

	if db.xact {
		err = db.filer.BeginUpdate()
	}
	return
}

func (db *DB) leave(err *error) error {
	switch db.acidState {
	default:
		panic("internal error")
	case stDisabled:
		// nop
	case stIdle:
		panic("internal error")
	case stCollecting:
		db.acidNest--
		if db.acidNest == 0 {
			db.acidState = stIdleArmed
		}
	case stIdleArmed:
		panic("internal error")
	case stCollectingArmed:
		db.acidNest--
		if db.acidNest == 0 {
			db.acidState = stIdleArmed
		}
	case stCollectingTriggered:
		db.acidNest--
		if db.acidNest == 0 {
			if e := db.filer.EndUpdate(); e != nil && err == nil {
				*err = e
			}
			db.acidState = stIdle
		}
	case stEndUpdateFailed:
		db.bkl.Unlock()
		return fmt.Errorf("Last transaction commit failed: %v", db.lastCommitErr)
	}

	if db.xact {
		switch {
		case *err != nil:
			db.filer.Rollback() // return the original, input error
		default:
			*err = db.filer.EndUpdate()
			if *err != nil {
				db.acidState = stEndUpdateFailed
				db.lastCommitErr = *err
			}
		}
	}
	db.bkl.Unlock()
	return *err
}

func (db *DB) timeout() {
	db.bkl.Lock()
	defer db.bkl.Unlock()

	select {
	case _ = <-db.closed:
		return
	default:
	}

	switch db.acidState {
	default:
		panic("internal error")
	case stIdle:
		panic("internal error")
	case stCollecting:
		db.acidState = stCollectingTriggered
	case stIdleArmed:
		if err := db.filer.EndUpdate(); err != nil { // If EndUpdate fails, no WAL was written (automatic Rollback)
			db.acidState = stEndUpdateFailed
			db.lastCommitErr = err
			return
		}

		db.acidState = stIdle
	case stCollectingArmed:
		db.acidState = stCollectingTriggered
	case stCollectingTriggered:
		panic("internal error")
	}
}

// Sync commits the current contents of the DB file to stable storage.
// Typically, this means flushing the file system's in-memory copy of recently
// written data to disk.
//
// NOTE: There's no good reason to invoke Sync if db uses 2PC/WAL (see
// Options.ACID).
func (db *DB) Sync() (err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	return db.filer.Sync()
}

// File returns a File associated with name.
func (db *DB) File(name string) (f File, err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	f, err = db.fileArray(false, name)
	if err != nil {
		panic(fmt.Errorf("internal error: \"%v\"", err))
	}

	return
}

// Inc atomically increments the value at subscripts of array by delta and
// returns the new value. If the value doesn't exists before calling Inc or if
// the value is not an integer then the value is considered to be zero.
func (db *DB) Inc(delta int64, array string, subscripts ...interface{}) (val int64, err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	a, err := db.array_(true, array, subscripts...)
	if err != nil {
		return
	}

	return a.inc(delta)
}

// BeginUpdate increments a "nesting" counter (initially zero). Every
// call to BeginUpdate must be eventually "balanced" by exactly one of
// EndUpdate or Rollback. Calls to BeginUpdate may nest.
func (db *DB) BeginUpdate() (err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	return db.filer.BeginUpdate()
}

// EndUpdate decrements the "nesting" counter. If it's zero after that then
// assume the "storage" has reached structural integrity (after a batch of
// partial updates). Invocation of an unbalanced EndUpdate is an error.
func (db *DB) EndUpdate() (err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	return db.filer.EndUpdate()
}

// Rollback cancels and undoes the innermost pending update level (if
// transactions are eanbled).  Rollback decrements the "nesting" counter.
// Invocation of an unbalanced Rollback is an error.
func (db *DB) Rollback() (err error) {
	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	return db.filer.Rollback()
}

// Verify attempts to find any structural errors in DB wrt the organization of
// it as defined by lldb.Allocator. 'bitmap' is a scratch pad for necessary
// bookkeeping and will grow to at most to DB size/128 (0,78%). Any problems
// found are reported to 'log' except non verify related errors like disk read
// fails etc. If 'log' returns false or the error doesn't allow to (reliably)
// continue, the verification process is stopped and an error is returned from
// the Verify function. Passing a nil log works like providing a log function
// always returning false. Any non-structural errors, like for instance Filer
// read errors, are NOT reported to 'log', but returned as the Verify's return
// value, because Verify cannot proceed in such cases. Verify returns nil only
// if it fully completed verifying DB without detecting any error.
//
// It is recommended to limit the number reported problems by returning false
// from 'log' after reaching some limit. Huge and corrupted DB can produce an
// overwhelming error report dataset.
//
// The verifying process will scan the whole DB at least 3 times (a trade
// between processing space and time consumed). It doesn't read the content of
// free blocks above the head/tail info bytes. If the 3rd phase detects lost
// free space, then a 4th scan (a faster one) is performed to precisely report
// all of them.
//
// Statistics are returned via 'stats' if non nil. The statistics are valid
// only if Verify succeeded, ie. it didn't reported anything to log and it
// returned a nil error.
func (db *DB) Verify(log func(error) bool, stats *lldb.AllocStats) (err error) {
	bitmapf, err := fileutil.TempFile(".", "verifier", ".tmp")
	if err != nil {
		return
	}

	defer func() {
		tn := bitmapf.Name()
		bitmapf.Close()
		os.Remove(tn)
	}()

	bitmap := lldb.NewSimpleFileFiler(bitmapf)

	if err = db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		db.leave(&err)
	}()

	return db.alloc.Verify(bitmap, log, stats)
}

// PeakWALSize reports the maximum size WAL has ever used.
func (db *DB) PeakWALSize() int64 {
	af, ok := db.filer.(*lldb.ACIDFiler0)
	if !ok {
		return 0
	}

	return af.PeakWALSize()
}

// IsMem reports whether db is backed by memory only.
func (db *DB) IsMem() bool {
	return db.isMem
}
