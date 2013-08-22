// Copyright (c) 2011 CZ.NIC z.s.p.o. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// blame: jnml, labs.nic.cz

package storage

import (
	"io/ioutil"
	"os"
	"testing"
)

func newfile(t *testing.T) Accessor {
	f, err := NewFile(*fFlag, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
	if err != nil {
		t.Fatal("newfile", err)
	}

	return f
}

func openfile(t *testing.T) Accessor {
	f, err := OpenFile(*fFlag, os.O_RDONLY, 0666)
	if err != nil {
		t.Fatal("openfile", err)
	}

	return f
}

func readfile(t *testing.T) (b []byte) {
	var err error
	if b, err = ioutil.ReadFile(*fFlag); err != nil {
		t.Fatal("readfile")
	}

	return
}

func newcache(t *testing.T) (c *Cache) {
	f := newfile(t)
	var err error
	c, err = NewCache(f, 1<<20, nil)
	if err != nil {
		t.Fatal("newCache", err)
	}

	return
}

func TestCache0(t *testing.T) {
	c := newcache(t)
	if err := c.Close(); err != nil {
		t.Fatal(10, err)
	}

	if b := readfile(t); len(b) != 0 {
		t.Fatal(20, len(b), 0)
	}
}

func TestCache1(t *testing.T) {
	c := newcache(t)
	if n, err := c.WriteAt([]byte{0xa5}, 0); n != 1 {
		t.Fatal(20, n, err)
	}

	if err := c.Close(); err != nil {
		t.Fatal(10, err)
	}

	b := readfile(t)
	if len(b) != 1 {
		t.Fatal(30, len(b), 1)
	}

	if b[0] != 0xa5 {
		t.Fatal(40, b[0], 0xa5)
	}
}
