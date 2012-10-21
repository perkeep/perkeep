//MySQL driver for Go sql package
package godrv

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"camlistore.org/third_party/github.com/ziutek/mymysql/mysql"
	"camlistore.org/third_party/github.com/ziutek/mymysql/native"
	"io"
	"math"
	"net"
	"reflect"
	"strings"
	"time"
	"unsafe"
)

type conn struct {
	my mysql.Conn
}

func errFilter(err error) error {
	if err == io.ErrUnexpectedEOF {
		return driver.ErrBadConn
	} else if _, ok := err.(net.Error); ok {
		return driver.ErrBadConn
	}
	return err
}

func (c conn) Prepare(query string) (driver.Stmt, error) {
	st, err := c.my.Prepare(query)
	if err != nil {
		return nil, errFilter(err)
	}
	return stmt{st}, nil
}

func (c conn) Close() error {
	err := c.my.Close()
	c.my = nil
	return errFilter(err)
}

func (c conn) Begin() (driver.Tx, error) {
	t, err := c.my.Begin()
	if err != nil {
		return tx{nil}, errFilter(err)
	}
	return tx{t}, nil
}

type tx struct {
	my mysql.Transaction
}

func (t tx) Commit() error {
	return t.my.Commit()
}

func (t tx) Rollback() error {
	return t.my.Rollback()
}

type stmt struct {
	my mysql.Stmt
}

func (s stmt) Close() error {
	err := s.my.Delete()
	s.my = nil
	return errFilter(err)
}

func (s stmt) NumInput() int {
	return s.my.NumParam()
}

func (s stmt) run(args []driver.Value) (*rowsRes, error) {
	a := (*[]interface{})(unsafe.Pointer(&args))
	res, err := s.my.Run(*a...)
	if err != nil {
		return nil, errFilter(err)
	}
	return &rowsRes{res, res.MakeRow()}, nil
}

func (s stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.run(args)
}

func (s stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.run(args)
}

type rowsRes struct {
	my  mysql.Result
	row mysql.Row
}

func (r rowsRes) LastInsertId() (int64, error) {
	return int64(r.my.InsertId()), nil
}

func (r rowsRes) RowsAffected() (int64, error) {
	return int64(r.my.AffectedRows()), nil
}

func (r rowsRes) Columns() []string {
	flds := r.my.Fields()
	cls := make([]string, len(flds))
	for i, f := range flds {
		cls[i] = f.Name
	}
	return cls
}

func (r rowsRes) Close() error {
	err := r.my.End()
	r.my = nil
	r.row = nil
	if err != native.READ_AFTER_EOR_ERROR {
		return errFilter(err)
	}
	return nil
}

// DATE, DATETIME, TIMESTAMP are treated as they are in Local time zone
func (r rowsRes) Next(dest []driver.Value) error {
	err := r.my.ScanRow(r.row)
	if err != nil {
		return errFilter(err)
	}
	for i, col := range r.row {
		if col == nil {
			dest[i] = nil
			continue
		}
		switch c := col.(type) {
		case time.Time:
			dest[i] = c
			continue
		case mysql.Timestamp:
			dest[i] = c.Time
			continue
		case mysql.Date:
			dest[i] = c.Localtime()
			continue
		}
		v := reflect.ValueOf(col)
		switch v.Kind() {
		case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			// this contains time.Duration to
			dest[i] = v.Int()
		case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			u := v.Uint()
			if u > math.MaxInt64 {
				panic("Value to large for int64 type")
			}
			dest[i] = int64(u)
		case reflect.Float32, reflect.Float64:
			dest[i] = v.Float()
		case reflect.Slice:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				dest[i] = v.Interface().([]byte)
				break
			}
			fallthrough
		default:
			panic(fmt.Sprint("Unknown type of column: ", v.Type()))
		}
	}
	return nil
}

type Driver struct {
	// Defaults
	proto, laddr, raddr, user, passwd, db string

	initCmds []string
}

// Open new connection. The uri need to have the following syntax:
//
//   [PROTOCOL_SPECFIIC*]DBNAME/USER/PASSWD
//
// where protocol spercific part may be empty (this means connection to
// local server using default protocol). Currently possible forms:
//   DBNAME/USER/PASSWD
//   unix:SOCKPATH*DBNAME/USER/PASSWD
//   tcp:ADDR*DBNAME/USER/PASSWD
func (d *Driver) Open(uri string) (driver.Conn, error) {
	pd := strings.SplitN(uri, "*", 2)
	if len(pd) == 2 {
		// Parse protocol part of URI
		p := strings.SplitN(pd[0], ":", 2)
		if len(p) != 2 {
			return nil, errors.New("Wrong protocol part of URI")
		}
		d.proto = p[0]
		d.raddr = p[1]
		// Remove protocol part
		pd = pd[1:]
	}
	// Parse database part of URI
	dup := strings.SplitN(pd[0], "/", 3)
	if len(dup) != 3 {
		return nil, errors.New("Wrong database part of URI")
	}
	d.db = dup[0]
	d.user = dup[1]
	d.passwd = dup[2]

	// Establish the connection
	c := conn{mysql.New(d.proto, d.laddr, d.raddr, d.user, d.passwd, d.db)}
	for _, q := range d.initCmds {
		c.my.Register(q) // Register initialisation commands
	}
	if err := c.my.Connect(); err != nil {
		return nil, errFilter(err)
	}
	return &c, nil
}

// Driver automatically registered in database/sql
var d = Driver{proto: "tcp", raddr: "127.0.0.1:3306"}

// Registers initialisation commands.
// This is workaround, see http://codereview.appspot.com/5706047
func Register(query string) {
	d.initCmds = append(d.initCmds, query)
}

func init() {
	Register("SET NAMES utf8")
	sql.Register("mymysql", &d)
}
