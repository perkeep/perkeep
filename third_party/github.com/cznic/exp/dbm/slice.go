// Copyright 2014 The dbm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Functions analogous to package "os".

package dbm

import (
	"camlistore.org/third_party/github.com/cznic/exp/lldb"
)

// Slice represents a slice of an Array.
type Slice struct {
	a        *Array
	prefix   []interface{}
	from, to []interface{}
}

// Do calls f for every subscripts-value pair in s in ascending collation order
// of the subscripts.  Do returns non nil error for general errors (eg. file
// read error).  If f returns false or a non nil error then Do terminates and
// returns the value of error from f.
//
// Note: f can get called with a subscripts-value pair which actually may no
// longer exist - if some other goroutine introduces such data race.
// Coordination required to avoid this situation, if applicable/desirable, must
// be provided by the client of dbm.
func (s *Slice) Do(f func(subscripts, value []interface{}) (bool, error)) (err error) {
	var (
		db    = s.a.db
		noVal bool
	)

	if err = db.enter(); err != nil {
		return
	}

	doLeave := true
	defer func() {
		if doLeave {
			db.leave(&err)
		}
	}()

	ok, err := s.a.validate(false)
	if !ok {
		return err
	}

	tree := s.a.tree
	if !tree.IsMem() && tree.Handle() == 1 {
		noVal = true
	}

	switch {
	case s.from == nil && s.to == nil:
		bprefix, err := lldb.EncodeScalars(s.prefix...)
		if err != nil {
			return err
		}

		enum, _, err := tree.Seek(bprefix)
		if err != nil {
			return noEof(err)
		}

		for {
			bk, bv, err := enum.Next()
			if err != nil {
				return noEof(err)
			}

			k, err := lldb.DecodeScalars(bk)
			if err != nil {
				return noEof(err)
			}

			if n := len(s.prefix); n != 0 {
				if len(k) < len(s.prefix) {
					return nil
				}

				c, err := lldb.Collate(k[:n], s.prefix, nil)
				if err != nil {
					return err
				}

				if c > 0 {
					return nil
				}
			}

			v, err := lldb.DecodeScalars(bv)
			if err != nil {
				return err
			}

			doLeave = false
			if db.leave(&err) != nil {
				return err
			}

			if noVal && v != nil {
				v = []interface{}{0}
			}
			if more, err := f(k[len(s.prefix):], v); !more || err != nil {
				return noEof(err)
			}

			if err = db.enter(); err != nil {
				return err
			}

			doLeave = true
		}
	case s.from == nil && s.to != nil:
		bprefix, err := lldb.EncodeScalars(s.prefix...)
		if err != nil {
			return err
		}

		enum, _, err := tree.Seek(bprefix)
		if err != nil {
			return noEof(err)
		}

		to := append(append([]interface{}(nil), s.prefix...), s.to...)
		for {
			bk, bv, err := enum.Next()
			if err != nil {
				return noEof(err)
			}

			k, err := lldb.DecodeScalars(bk)
			if err != nil {
				return err
			}

			c, err := lldb.Collate(k, to, nil)
			if err != nil {
				return err
			}

			if c > 0 {
				return err
			}

			v, err := lldb.DecodeScalars(bv)
			if err != nil {
				return noEof(err)
			}

			doLeave = false
			if db.leave(&err) != nil {
				return err
			}

			if noVal && v != nil {
				v = []interface{}{0}
			}
			if more, err := f(k[len(s.prefix):], v); !more || err != nil {
				return noEof(err)
			}

			if err = db.enter(); err != nil {
				return err
			}

			doLeave = true
		}
	case s.from != nil && s.to == nil:
		bprefix, err := lldb.EncodeScalars(append(s.prefix, s.from...)...)
		if err != nil {
			return err
		}

		enum, _, err := tree.Seek(bprefix)
		if err != nil {
			return noEof(err)
		}

		for {
			bk, bv, err := enum.Next()
			if err != nil {
				return noEof(err)
			}

			k, err := lldb.DecodeScalars(bk)
			if err != nil {
				return noEof(err)
			}

			if n := len(s.prefix); n != 0 {
				if len(k) < len(s.prefix) {
					return nil
				}

				c, err := lldb.Collate(k[:n], s.prefix, nil)
				if err != nil {
					return err
				}

				if c > 0 {
					return nil
				}
			}

			v, err := lldb.DecodeScalars(bv)
			if err != nil {
				return err
			}

			doLeave = false
			if db.leave(&err) != nil {
				return err
			}

			if noVal && v != nil {
				v = []interface{}{0}
			}
			if more, err := f(k[len(s.prefix):], v); !more || err != nil {
				return noEof(err)
			}

			if err = db.enter(); err != nil {
				return err
			}

			doLeave = true
		}
	case s.from != nil && s.to != nil:
		bprefix, err := lldb.EncodeScalars(append(s.prefix, s.from...)...)
		if err != nil {
			return err
		}

		enum, _, err := tree.Seek(bprefix)
		if err != nil {
			return noEof(err)
		}

		to := append(append([]interface{}(nil), s.prefix...), s.to...)
		for {
			bk, bv, err := enum.Next()
			if err != nil {
				return noEof(err)
			}

			k, err := lldb.DecodeScalars(bk)
			if err != nil {
				return noEof(err)
			}

			c, err := lldb.Collate(k, to, nil)
			if err != nil {
				return err
			}

			if c > 0 {
				return err
			}

			v, err := lldb.DecodeScalars(bv)
			if err != nil {
				return err
			}

			doLeave = false
			if db.leave(&err) != nil {
				return err
			}

			if noVal && v != nil {
				v = []interface{}{0}
			}
			if more, err := f(k[len(s.prefix):], v); !more || err != nil {
				return noEof(err)
			}

			if err = db.enter(); err != nil {
				return err
			}

			doLeave = true
		}
	default:
		panic("slice.go: internal error")
	}
}
