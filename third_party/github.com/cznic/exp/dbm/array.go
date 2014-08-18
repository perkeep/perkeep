// Copyright 2014 The dbm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dbm

import (
	"fmt"
	"io"

	"camlistore.org/third_party/github.com/cznic/exp/lldb"
)

// Array is a reference to a subtree of an array.
type Array struct {
	db        *DB
	tree      *lldb.BTree
	prefix    []byte
	name      string
	namespace byte
}

// MemArray returns an Array associated with a subtree of an anonymous array,
// determined by subscripts. MemArrays are resource limited as they are
// completely held in memory and are not automatically persisted.
func MemArray(subscripts ...interface{}) (a Array, err error) {
	a.db = &DB{}
	if a, err = a.Array(subscripts...); err != nil {
		return a, err
	}

	a.tree = lldb.NewBTree(collate)
	return
}

func bpack(a []byte) []byte {
	if cap(a) > len(a) {
		return append([]byte(nil), a...)
	}

	return a
}

// Named trees (arrays) can get removed, but references to them (Arrays) may
// outlive that. db.bkl locked is assumed. ok => a.tree != nil && err == nil.
func (a *Array) validate(canCreate bool) (ok bool, err error) {
	if a.tree != nil && (a.tree.Handle() == 1 || a.tree.IsMem()) {
		return true, nil
	}

	switch a.namespace {
	case arraysPrefix:
		a.tree, err = a.db.acache.getTree(a.db, arraysPrefix, a.name, canCreate, aCacheSize)
	case filesPrefix:
		a.tree, err = a.db.fcache.getTree(a.db, filesPrefix, a.name, canCreate, fCacheSize)
	case systemPrefix:
		a.tree, err = a.db.scache.getTree(a.db, systemPrefix, a.name, canCreate, sCacheSize)
	default:
		panic("internal error")
	}

	switch {
	case a.tree == nil && err == nil:
		return false, nil
	case a.tree == nil && err != nil:
		return false, err
	case a.tree != nil && err == nil:
		return true, nil
		//case a.tree != nil && err != nil:
	}
	panic("internal error")
}

// Array returns an object associated with a subtree of array 'a', determined
// by subscripts.
func (a *Array) Array(subscripts ...interface{}) (r Array, err error) {
	if err = a.db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		a.db.leave(&err)
	}()

	return a.array(subscripts...)
}

func (a *Array) array(subscripts ...interface{}) (r Array, err error) {
	r = *a
	prefix, err := lldb.EncodeScalars(subscripts...)
	if err != nil {
		return
	}

	r.prefix = append(bpack(r.prefix), prefix...)
	return
}

func (a *Array) bset(val, key []byte) (err error) {
	err = a.tree.Set(append(a.prefix, key...), val)
	return
}

func (a *Array) binc(delta int64, key []byte) (r int64, err error) {
	_, _, err = a.tree.Put(
		nil, //TODO buffers
		append(a.prefix, key...),
		func(key []byte, old []byte) (new []byte, write bool, err error) {
			write = true
			if len(old) != 0 {
				decoded, err := lldb.DecodeScalars(old)
				switch {
				case err != nil:
					// nop
				case len(decoded) != 1:
					// nop
				default:
					r, _ = decoded[0].(int64)
				}
			}
			r += delta
			new, err = lldb.EncodeScalars(r)
			return
		},
	)
	return
}

func (a *Array) bget(key []byte) (value []byte, err error) {
	return a.tree.Get(nil, append(a.prefix, key...))
}

func (a *Array) bdelete(key []byte) (err error) {
	return a.tree.Delete(append(a.prefix, key...))
}

// Set sets the value at subscripts in subtree 'a'. Any previous value, if
// existed, is overwritten by the new one.
func (a *Array) Set(value interface{}, subscripts ...interface{}) (err error) {
	if err = a.db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		a.db.leave(&err)
	}()

	if t := a.tree; t != nil && !t.IsMem() && a.tree.Handle() == 1 {
		return &lldb.ErrPERM{Src: "dbm.Array.Set"}
	}

	if ok, err := a.validate(true); !ok {
		return err
	}

	return a.set(value, subscripts...)
}

func (a *Array) set(value interface{}, subscripts ...interface{}) (err error) {
	val, err := encVal(value)
	if err != nil {
		return
	}

	key, err := lldb.EncodeScalars(subscripts...)
	if err != nil {
		return
	}

	return a.bset(val, key)
}

// Inc atomically increments the value at subscripts by delta and returns the
// new value. If the value doesn't exists before calling Inc or if the value is
// not an integer then the value is considered to be zero.
func (a *Array) Inc(delta int64, subscripts ...interface{}) (val int64, err error) {
	if err = a.db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		a.db.leave(&err)
	}()

	if t := a.tree; t != nil && !t.IsMem() && a.tree.Handle() == 1 {
		return 0, &lldb.ErrPERM{Src: "dbm.Array.Inc"}
	}

	if ok, err := a.validate(true); !ok {
		return 0, err
	}

	return a.inc(delta, subscripts...)
}

func (a *Array) inc(delta int64, subscripts ...interface{}) (val int64, err error) {
	key, err := lldb.EncodeScalars(subscripts...)
	if err != nil {
		return
	}

	return a.binc(delta, key)
}

// Get returns the value at subscripts in subtree 'a', or nil if no such value
// exists.
func (a *Array) Get(subscripts ...interface{}) (value interface{}, err error) {
	if err = a.db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		a.db.leave(&err)
	}()

	if ok, e := a.validate(false); !ok || err != nil {
		err = e
		return
	}

	value, err = a.get(subscripts...)
	if value == nil {
		return
	}

	if t := a.tree; t != nil && !t.IsMem() && t.Handle() == 1 {
		value = 0
	}
	return
}

func (a *Array) get(subscripts ...interface{}) (value interface{}, err error) {
	key, err := lldb.EncodeScalars(subscripts...)
	if err != nil {
		return
	}

	val, err := a.bget(key)
	if err != nil {
		return
	}

	if val == nil {
		return
	}

	va, err := lldb.DecodeScalars(val)
	if err != nil {
		return nil, err
	}

	value = va
	if len(va) == 1 {
		value = va[0]
	}
	return
}

// Delete deletes the value at subscripts in array.
func (a *Array) Delete(subscripts ...interface{}) (err error) {
	if err = a.db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		a.db.leave(&err)
	}()

	if t := a.tree; t != nil && !t.IsMem() && a.tree.Handle() == 1 {
		return &lldb.ErrPERM{Src: "dbm.Array.Delete"}
	}

	if ok, err := a.validate(false); !ok {
		return err
	}

	return a.delete(subscripts...)
}

func (a *Array) delete(subscripts ...interface{}) (err error) {
	key, err := lldb.EncodeScalars(subscripts...)
	if err != nil {
		return
	}

	return a.bdelete(key)
}

// Clear empties the subtree at subscripts in 'a'.
func (a *Array) Clear(subscripts ...interface{}) (err error) {
	//TODO optimize for clear "everything"

	if err = a.db.enter(); err != nil {
		return
	}

	doLeave := true
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		if doLeave {
			a.db.leave(&err)
		}
	}()

	if t := a.tree; t != nil && !t.IsMem() && a.tree.Handle() == 1 {
		return &lldb.ErrPERM{Src: "dbm.Array.Clear"}
	}

	if ok, err := a.validate(false); !ok {
		return err
	}

	doLeave = false
	if a.db.leave(&err) != nil {
		return
	}

	a0 := *a
	a0.prefix = nil

	prefix, err := lldb.DecodeScalars(a.prefix)
	if err != nil {
		panic("internal error")
	}

	subscripts = append(prefix, subscripts...)
	n := len(subscripts)

	bSubscripts, err := lldb.EncodeScalars(subscripts...)
	if err != nil {
		panic("internal error")
	}

	s, err := a0.Slice(nil, nil)
	if err != nil {
		return
	}

	return s.Do(func(actualSubscripts, value []interface{}) (more bool, err error) {
		if len(actualSubscripts) < n {
			return
		}

		common := actualSubscripts[:n]
		bcommon, err := lldb.EncodeScalars(common...)
		if err != nil {
			panic("internal error")
		}

		switch collate(bcommon, bSubscripts) {
		case -1:
			return true, nil
		case 0:
			return true, a0.Delete(actualSubscripts...)
		}
		// case 1:
		return false, nil
	})
}

// Slice returns a new Slice from Array, with a subscripts range of [from, to].
// If from is nil it works as 'from lowest existing key'.  If to is nil it
// works as 'to highest existing key'.
func (a *Array) Slice(from, to []interface{}) (s *Slice, err error) {
	if err = a.db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		a.db.leave(&err)
	}()

	prefix, err := lldb.DecodeScalars(a.prefix)
	if err != nil {
		return
	}

	return &Slice{
		a:      a,
		prefix: prefix,
		from:   from,
		to:     to,
	}, nil
}

// Dump outputs a human readable dump of a to w.  Intended use is only for
// examples or debugging. Some type information is lost in the rendering, for
// example a float value '17.' and an integer value '17' may both output as
// '17'.
//
// Note: Dump will lock the database until finished.
func (a *Array) Dump(w io.Writer) (err error) {
	if err = a.db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		a.db.leave(&err)
	}()

	return a.tree.Dump(w)
}

func (a *Array) Tree() (tr *lldb.BTree, err error) {
	_, err = a.validate(false)
	if err != nil {
		return
	}

	return a.tree, nil
}

// Enumerator returns a "raw" enumerator of the whole array. It's initially
// positioned on the first (asc is true) or last (asc is false)
// subscripts/value pair in the array.
//
// This method is safe for concurrent use by multiple goroutines.
func (a *Array) Enumerator(asc bool) (en *Enumerator, err error) {
	if err = a.db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			switch x := e.(type) {
			case error:
				err = x
			default:
				err = fmt.Errorf("%v", e)
			}
		}
		a.db.leave(&err)
	}()

	var e Enumerator
	switch asc {
	case true:
		e.en, err = a.tree.SeekFirst()
	default:
		e.en, err = a.tree.SeekLast()
	}
	if err != nil {
		return
	}

	e.db = a.db
	return &e, nil
}

// Enumerator provides visiting all K/V pairs in a DB/range.
type Enumerator struct {
	db *DB
	en *lldb.BTreeEnumerator
}

// Next returns the currently enumerated raw KV pair, if it exists and moves to
// the next KV in the key collation order. If there is no KV pair to return,
// err == io.EOF is returned.
//
// This method is safe for concurrent use by multiple goroutines.
func (e *Enumerator) Next() (key, value []interface{}, err error) {
	if err = e.db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			switch x := e.(type) {
			case error:
				err = x
			default:
				err = fmt.Errorf("%v", e)
			}
		}
		e.db.leave(&err)
	}()

	k, v, err := e.en.Next()
	if err != nil {
		return
	}

	if key, err = lldb.DecodeScalars(k); err != nil {
		return
	}

	value, err = lldb.DecodeScalars(v)
	return
}

// Prev returns the currently enumerated raw KV pair, if it exists and moves to
// the previous KV in the key collation order. If there is no KV pair to
// return, err == io.EOF is returned.
//
// This method is safe for concurrent use by multiple goroutines.
func (e *Enumerator) Prev() (key, value []interface{}, err error) {
	if err = e.db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			switch x := e.(type) {
			case error:
				err = x
			default:
				err = fmt.Errorf("%v", e)
			}
		}
		e.db.leave(&err)
	}()

	k, v, err := e.en.Prev()
	if err != nil {
		return
	}

	if key, err = lldb.DecodeScalars(k); err != nil {
		return
	}

	value, err = lldb.DecodeScalars(v)
	return
}
