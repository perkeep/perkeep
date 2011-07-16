package sqlite

import (
	"testing"
)

func TestFoo(t *testing.T) {
	db, err := Open("foo.db")
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
}
