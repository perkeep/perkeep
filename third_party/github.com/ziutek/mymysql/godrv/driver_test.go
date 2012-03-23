package godrv

import (
	"database/sql"
	"testing"
)

func init() {
	Register("set names utf8")
}

func checkErr(t *testing.T, err error) {
	if err != nil {
		t.Fatalf("Error: %v", err)
	}
}
func checkErrId(t *testing.T, err error, rid, eid int64) {
	checkErr(t, err)
	if rid != eid {
		t.Fatal("res.LastInsertId() ==", rid, "but should be", eid)
	}
}

func TestAll(t *testing.T) {
	data := []string{"jeden", "dwa"}

	db, err := sql.Open("mymysql", "test/testuser/TestPasswd9")
	checkErr(t, err)
	defer db.Close()

	db.Exec("DROP TABLE go")

	_, err = db.Exec(
		`CREATE TABLE go (
			id  INT PRIMARY KEY AUTO_INCREMENT,
			txt TEXT
		) ENGINE=InnoDB`)
	checkErr(t, err)

	ins, err := db.Prepare("INSERT go SET txt=?")
	checkErr(t, err)

	tx, err := db.Begin()
	checkErr(t, err)

	res, err := ins.Exec(data[0])
	checkErr(t, err)
	id, err := res.LastInsertId()
	checkErrId(t, err, id, 1)

	res, err = ins.Exec(data[1])
	checkErr(t, err)
	id, err = res.LastInsertId()
	checkErrId(t, err, id, 2)

	checkErr(t, tx.Commit())

	tx, err = db.Begin()
	checkErr(t, err)

	res, err = tx.Exec("INSERT go SET txt=?", "trzy")
	checkErr(t, err)
	id, err = res.LastInsertId()
	checkErrId(t, err, id, 3)

	checkErr(t, tx.Rollback())

	rows, err := db.Query("SELECT * FROM go")
	checkErr(t, err)
	for rows.Next() {
		var id int
		var txt string
		checkErr(t, rows.Scan(&id, &txt))
		if id > len(data) {
			t.Fatal("To many rows in table")
		}
		if data[id-1] != txt {
			t.Fatalf("txt[%d] == '%s' != '%s'", id, txt, data[id-1])
		}
	}

	sql := "select sum(41) as test"
	row := db.QueryRow(sql)
	var vi int64
	checkErr(t, row.Scan(&vi))
	if vi != 41 {
		t.Fatal(sql)
	}
	sql = "select sum(4123232323232) as test"
	row = db.QueryRow(sql)
	var vf float64
	checkErr(t, row.Scan(&vf))
	if vf != 4123232323232 {
		t.Fatal(sql)
	}
}
