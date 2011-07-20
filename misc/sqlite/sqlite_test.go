package sqlite

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func TestFoo(t *testing.T) {
	td, err := ioutil.TempDir("", "go-sqlite-test")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	dbName := filepath.Join(td, "foo.db")
	defer os.Remove(dbName)

	db, err := Open(dbName)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	err = db.Exec("CREATE TABLE IF NOT EXISTS foo (a INT, b VARCHAR(200))")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	err = db.Exec("INSERT INTO foo VALUES (1, ?)", "foo")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	err = db.Exec("INSERT INTO foo VALUES (2, DATETIME('now'))")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	err = db.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	fi, err := os.Stat(dbName)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Size == 0 {
		t.Fatalf("FileInfo.Size after writes was 0")
	}
	t.Logf("fi.Size = %d", fi.Size)
}
