package autorc

import (
	_ "github.com/ziutek/mymysql/thrsafe"
	"testing"
)

var (
	conn   = []string{"tcp", "", "127.0.0.1:3306"}
	user   = "testuser"
	passwd = "TestPasswd9"
	dbname = "test"
)

func checkErr(t *testing.T, err error, exp_err error) {
	if err != exp_err {
		if exp_err == nil {
			t.Fatalf("Error: %v", err)
		} else {
			t.Fatalf("Error: %v\nExpected error: %v", err, exp_err)
		}
	}
}

func TestAutoConnectReconnect(t *testing.T) {
	c := New(conn[0], conn[1], conn[2], user, passwd)
	c.Debug = false

	// Register initialisation commands
	c.Raw.Register("set names utf8")

	// my is in unconnected state
	checkErr(t, c.Use(dbname), nil)

	// Disconnect
	c.Raw.Close()

	// Drop test table if exists
	c.Query("drop table R")

	// Disconnect
	c.Raw.Close()

	// Create table
	_, _, err := c.Query(
		"create table R (id int primary key, name varchar(20))",
	)
	checkErr(t, err, nil)

	// Kill the connection
	_, _, err = c.Query("kill %d", c.Raw.ThreadId())
	checkErr(t, err, nil)

	// Prepare insert statement
	ins, err := c.Prepare("insert R values (?,  ?)")
	checkErr(t, err, nil)

	// Kill the connection
	_, _, err = c.Query("kill %d", c.Raw.ThreadId())
	checkErr(t, err, nil)

	// Bind insert parameters
	ins.Raw.Bind(1, "jeden")
	// Insert into table
	_, _, err = ins.Exec()
	checkErr(t, err, nil)

	// Kill the connection
	_, _, err = c.Query("kill %d", c.Raw.ThreadId())
	checkErr(t, err, nil)

	// Bind insert parameters
	ins.Raw.Bind(2, "dwa")
	// Insert into table
	_, _, err = ins.Exec()
	checkErr(t, err, nil)

	// Kill the connection
	_, _, err = c.Query("kill %d", c.Raw.ThreadId())
	checkErr(t, err, nil)

	// Select from table
	rows, res, err := c.Query("select * from R")
	checkErr(t, err, nil)
	id := res.Map("id")
	name := res.Map("name")
	if len(rows) != 2 ||
		rows[0].Int(id) != 1 || rows[0].Str(name) != "jeden" ||
		rows[1].Int(id) != 2 || rows[1].Str(name) != "dwa" {
		t.Fatal("Bad result")
	}

	// Kill the connection
	_, _, err = c.Query("kill %d", c.Raw.ThreadId())
	checkErr(t, err, nil)

	// Drop table
	_, _, err = c.Query("drop table R")
	checkErr(t, err, nil)

	// Disconnect
	c.Raw.Close()
}
