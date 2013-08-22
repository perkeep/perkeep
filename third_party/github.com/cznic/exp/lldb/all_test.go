// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lldb

import (
	"encoding/hex"
	"os"
	"time"
)

const (
	testDbName = "_test.db"
	walName    = "_wal"
)

func now() time.Time { return time.Now() }

func hdump(b []byte) string {
	return hex.Dump(b)
}

func die() {
	os.Exit(1)
}
