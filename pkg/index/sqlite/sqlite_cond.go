// +build with_sqlite

package sqlite

import (
	_ "camlistore.org/third_party/github.com/mattn/go-sqlite3"
)

func init() {
	compiled = true
}
