package sqlite

/*
#cgo CFLAGS: -D_GNU_SOURCE -D_XOPEN_SOURCE=500
#cgo LDFLAGS: -ldl -lpthread

#include <stdlib.h>
#define SKIP_SQLITE_VERSION 1
#include "sqlite3.h"

 static int my_bind_text(sqlite3_stmt *stmt, int n, char *p, int np) {
 return sqlite3_bind_text(stmt, n, p, np, SQLITE_TRANSIENT);
 }
 static int my_bind_blob(sqlite3_stmt *stmt, int n, void *p, int np) {
 return sqlite3_bind_blob(stmt, n, p, np, SQLITE_TRANSIENT);
 }

*/
import "C"

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"unsafe"
)

type Errno int

func (e Errno) String() string {
	s := errText[e]
	if s == "" {
		return fmt.Sprintf("errno %d", e)
	}
	return s
}

var (
	ErrError      os.Error = Errno(1)   //    /* SQL error or missing database */
	ErrInternal   os.Error = Errno(2)   //    /* Internal logic error in SQLite */
	ErrPerm       os.Error = Errno(3)   //    /* Access permission denied */
	ErrAbort      os.Error = Errno(4)   //    /* Callback routine requested an abort */
	ErrBusy       os.Error = Errno(5)   //    /* The database file is locked */
	ErrLocked     os.Error = Errno(6)   //    /* A table in the database is locked */
	ErrNoMem      os.Error = Errno(7)   //    /* A malloc() failed */
	ErrReadOnly   os.Error = Errno(8)   //    /* Attempt to write a readonly database */
	ErrInterrupt  os.Error = Errno(9)   //    /* Operation terminated by sqlite3_interrupt()*/
	ErrIOErr      os.Error = Errno(10)  //    /* Some kind of disk I/O error occurred */
	ErrCorrupt    os.Error = Errno(11)  //    /* The database disk image is malformed */
	ErrFull       os.Error = Errno(13)  //    /* Insertion failed because database is full */
	ErrCantOpen   os.Error = Errno(14)  //    /* Unable to open the database file */
	ErrEmpty      os.Error = Errno(16)  //    /* Database is empty */
	ErrSchema     os.Error = Errno(17)  //    /* The database schema changed */
	ErrTooBig     os.Error = Errno(18)  //    /* String or BLOB exceeds size limit */
	ErrConstraint os.Error = Errno(19)  //    /* Abort due to constraint violation */
	ErrMismatch   os.Error = Errno(20)  //    /* Data type mismatch */
	ErrMisuse     os.Error = Errno(21)  //    /* Library used incorrectly */
	ErrNolfs      os.Error = Errno(22)  //    /* Uses OS features not supported on host */
	ErrAuth       os.Error = Errno(23)  //    /* Authorization denied */
	ErrFormat     os.Error = Errno(24)  //    /* Auxiliary database format error */
	ErrRange      os.Error = Errno(25)  //    /* 2nd parameter to sqlite3_bind out of range */
	ErrNotDB      os.Error = Errno(26)  //    /* File opened that is not a database file */
	Row                    = Errno(100) //   /* sqlite3_step() has another row ready */
	Done                   = Errno(101) //   /* sqlite3_step() has finished executing */
)

var errText = map[Errno]string{
	1:   "SQL error or missing database",
	2:   "Internal logic error in SQLite",
	3:   "Access permission denied",
	4:   "Callback routine requested an abort",
	5:   "The database file is locked",
	6:   "A table in the database is locked",
	7:   "A malloc() failed",
	8:   "Attempt to write a readonly database",
	9:   "Operation terminated by sqlite3_interrupt()*/",
	10:  "Some kind of disk I/O error occurred",
	11:  "The database disk image is malformed",
	12:  "NOT USED. Table or record not found",
	13:  "Insertion failed because database is full",
	14:  "Unable to open the database file",
	15:  "NOT USED. Database lock protocol error",
	16:  "Database is empty",
	17:  "The database schema changed",
	18:  "String or BLOB exceeds size limit",
	19:  "Abort due to constraint violation",
	20:  "Data type mismatch",
	21:  "Library used incorrectly",
	22:  "Uses OS features not supported on host",
	23:  "Authorization denied",
	24:  "Auxiliary database format error",
	25:  "2nd parameter to sqlite3_bind out of range",
	26:  "File opened that is not a database file",
	100: "sqlite3_step() has another row ready",
	101: "sqlite3_step() has finished executing",
}

func (c *Conn) error(rv C.int) os.Error {
	if rv == 21 { // misuse
		return Errno(rv)
	}
	return os.NewError(Errno(rv).String() + ": " + C.GoString(C.sqlite3_errmsg(c.db)))
}

type Conn struct {
	db *C.sqlite3
}

func Version() string {
	p := C.sqlite3_libversion()
	return C.GoString(p)
}

func Open(filename string) (*Conn, os.Error) {
	var db *C.sqlite3
	name := C.CString(filename)
	defer C.free(unsafe.Pointer(name))
	rv := C.sqlite3_open(name, &db)
	if rv != 0 {
		return nil, Errno(rv)
	}
	if db == nil {
		return nil, os.NewError("sqlite succeeded without returning a database")
	}
	return &Conn{db}, nil
}

func (c *Conn) Exec(cmd string, args ...interface{}) os.Error {
	s, err := c.Prepare(cmd)
	if err != nil {
		return err
	}
	defer s.Finalize()
	err = s.Exec(args...)
	if err != nil {
		return err
	}
	rv := C.sqlite3_step(s.stmt)
	errno := Errno(rv)
	if errno != Done {
		return errno
	}
	return nil
}

type Stmt struct {
	c    *Conn
	stmt *C.sqlite3_stmt
	err  os.Error
}

func (c *Conn) Prepare(cmd string) (*Stmt, os.Error) {
	cmdstr := C.CString(cmd)
	defer C.free(unsafe.Pointer(cmdstr))
	var stmt *C.sqlite3_stmt
	var tail *C.char
	rv := C.sqlite3_prepare_v2(c.db, cmdstr, C.int(len(cmd)+1), &stmt, &tail)
	if rv != 0 {
		return nil, c.error(rv)
	}
	return &Stmt{c: c, stmt: stmt}, nil
}

func (s *Stmt) Exec(args ...interface{}) os.Error {
	rv := C.sqlite3_reset(s.stmt)
	if rv != 0 {
		return s.c.error(rv)
	}

	n := int(C.sqlite3_bind_parameter_count(s.stmt))
	if n != len(args) {
		return os.NewError(fmt.Sprintf("incorrect argument count: have %d want %d", len(args), n))
	}

	for i, v := range args {
		var str string
		switch v := v.(type) {
		case []byte:
			var p *byte
			if len(v) > 0 {
				p = &v[0]
			}
			if rv := C.my_bind_blob(s.stmt, C.int(i+1), unsafe.Pointer(p), C.int(len(v))); rv != 0 {
				return s.c.error(rv)
			}
			continue

		case bool:
			if v {
				str = "1"
			} else {
				str = "0"
			}

		default:
			str = fmt.Sprint(v)
		}

		cstr := C.CString(str)
		rv := C.my_bind_text(s.stmt, C.int(i+1), cstr, C.int(len(str)))
		C.free(unsafe.Pointer(cstr))
		if rv != 0 {
			return s.c.error(rv)
		}
	}
	return nil
}

func (s *Stmt) Error() os.Error {
	return s.err
}

func (s *Stmt) Next() bool {
	rv := C.sqlite3_step(s.stmt)
	err := Errno(rv)
	if err == Row {
		return true
	}
	if err != Done {
		s.err = s.c.error(rv)
	}
	return false
}

func (s *Stmt) Scan(args ...interface{}) os.Error {
	n := int(C.sqlite3_column_count(s.stmt))
	if n != len(args) {
		return os.NewError("incorrect argument count")
	}

	for i, v := range args {
		n := C.sqlite3_column_bytes(s.stmt, C.int(i))
		p := C.sqlite3_column_blob(s.stmt, C.int(i))
		if p == nil && n > 0 {
			return os.NewError("got nil blob")
		}
		var data []byte
		if n > 0 {
			data = (*[1 << 30]byte)(unsafe.Pointer(p))[0:n]
		}
		switch v := v.(type) {
		case *[]byte:
			*v = data
		case *string:
			*v = string(data)
		case *bool:
			*v = string(data) == "1"
		case *int:
			x, err := strconv.Atoi(string(data))
			if err != nil {
				return os.NewError("arg " + strconv.Itoa(i) + " as int: " + err.String())
			}
			*v = x
		case *int64:
			x, err := strconv.Atoi64(string(data))
			if err != nil {
				return os.NewError("arg " + strconv.Itoa(i) + " as int64: " + err.String())
			}
			*v = x
		case *float64:
			x, err := strconv.Atof64(string(data))
			if err != nil {
				return os.NewError("arg " + strconv.Itoa(i) + " as float64: " + err.String())
			}
			*v = x
		default:
			return os.NewError("unsupported type in Scan: " + reflect.TypeOf(v).String())
		}
	}
	return nil
}

func (s *Stmt) Finalize() os.Error {
	rv := C.sqlite3_finalize(s.stmt)
	if rv != 0 {
		return s.c.error(rv)
	}
	return nil
}

func (c *Conn) Close() os.Error {
	rv := C.sqlite3_close(c.db)
	if rv != 0 {
		return c.error(rv)
	}
	return nil
}
