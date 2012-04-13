// +build with_sqlite

// If the "with_sqlite" build tag is specified, the sqlite index driver
// is also built & loaded:
//
//    go install -tags=with_sqlite camlistore.org/server/camlistored
//
// This is an option because the sqlite3 SQL driver requires cgo & the
// SQLite3 C library available. We want it to still be possible to
// have a pure Go server too.

package main

import (
	_ "camlistore.org/pkg/index/sqlite"
)
