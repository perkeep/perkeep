// +build with_sqlite

package sqlite

import (
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	compiled = true
}
