// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"camlistore.org/third_party/github.com/cznic/zappy"
	"testing"
)

func Test(t *testing.T) {

	if n := len(value100); n != 100 {
		t.Fatal(n)
	}

	c, err := zappy.Encode(nil, value100)
	if err != nil {
		t.Fatal(err)
	}

	if n := len(c); n != 50 {
		t.Fatal(n)
	}
}
