// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kv

import (
	"io"
	"io/ioutil"
	"os"

	"camlistore.org/third_party/github.com/cznic/exp/lldb"
)

func verify(a *lldb.Allocator, log func(error) bool) (err error) {
	bits, err := ioutil.TempFile("", "kv-verify-")
	if err != nil {
		return err
	}

	defer bits.Close()
	if err = a.Verify(lldb.NewSimpleFileFiler(bits), log, nil); err != nil {
		return
	}

	t, err := lldb.OpenBTree(a, nil, 1)
	if err != nil {
		return err
	}

	e, err := t.SeekFirst()
	if err != nil {
		if err == io.EOF {
			err = nil
		}
		return err
	}

	for {
		_, _, err := e.Next()
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return err
		}
	}
	return
}

func verifyDbFile(fn string) (err error) {
	f, err := os.Open(fn) // O_RDONLY
	if err != nil {
		return err
	}

	defer f.Close()

	a, err := lldb.NewAllocator(lldb.NewInnerFiler(lldb.NewSimpleFileFiler(f), 16), &lldb.Options{})
	if err != nil {
		return err
	}

	return verify(a, func(e error) bool {
		err = e
		return false
	})
}

func verifyAllocator(a *lldb.Allocator) (err error) {
	return verify(a, func(e error) bool {
		err = e
		return false
	})
}
