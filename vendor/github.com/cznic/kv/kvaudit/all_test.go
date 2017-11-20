// Copyright 2014 The kv Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cznic/kv"
)

var mk = flag.Bool("mk", false, "(dev) make dump.db")

const testdata = "_testdata"

func TestBad(t *testing.T) {
	if err := main0(filepath.Join(testdata, "bad.db"), 0, null, false, "", "", false, false); err == nil {
		t.Fatal("unexpected success")
	}
}

func TestGood(t *testing.T) {
	if err := main0(filepath.Join(testdata, "good.db"), 0, null, false, "", "", false, false); err != nil {
		t.Fatal(err)
	}
}

func TestMakeTestData(t *testing.T) {
	if !*mk {
		return
	}

	db, err := kv.Create(filepath.Join(testdata, "dump.db"), &kv.Options{})
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	for i := 0; i < 10; i++ {
		c := 'a' + i
		if err = db.Set(
			[]byte(fmt.Sprintf("abc%c", c)),
			[]byte(fmt.Sprintf("%d", 1e6+i)),
		); err != nil {
			t.Error(err)
			return
		}
	}
}
