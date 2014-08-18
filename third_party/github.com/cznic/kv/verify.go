// Copyright 2014 The kv Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kv

import (
	"io"
	"io/ioutil"
	"os"

	"camlistore.org/third_party/github.com/cznic/exp/lldb"
)

func verifyAllocator(a *lldb.Allocator) error {
	bits, err := ioutil.TempFile("", "kv-verify-")
	if err != nil {
		return err
	}

	defer func() {
		nm := bits.Name()
		bits.Close()
		os.Remove(nm)
	}()

	var lerr error
	if err = a.Verify(
		lldb.NewSimpleFileFiler(bits),
		func(err error) bool {
			lerr = err
			return false
		},
		nil,
	); err != nil {
		return err
	}

	if lerr != nil {
		return lerr
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
}

func verifyDbFile(fn string) error {
	f, err := os.Open(fn) // O_RDONLY
	if err != nil {
		return err
	}

	defer f.Close()

	a, err := lldb.NewAllocator(lldb.NewInnerFiler(lldb.NewSimpleFileFiler(f), 16), &lldb.Options{})
	if err != nil {
		return err
	}

	return verifyAllocator(a)
}
