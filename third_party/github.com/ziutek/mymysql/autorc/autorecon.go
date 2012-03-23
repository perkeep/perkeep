// Auto reconnect interface for MyMySQL
package autorc

import (
	"github.com/ziutek/mymysql/mysql"
	"io"
	"log"
	"net"
	"time"
)

// Return true if error is network error or UnexpectedEOF.
func IsNetErr(err error) bool {
	if err == io.ErrUnexpectedEOF {
		return true
	} else if e, ok := err.(net.Error); ok && e.Temporary() {
		return true
	}
	return false
}

type Conn struct {
	Raw mysql.Conn
	// Maximum reconnect retries.
	// Default is 7 which means 1+2+3+4+5+6+7 = 28 seconds before return error.
	MaxRetries int

	// Debug logging. You may change it at any time.
	Debug bool
}

func New(proto, laddr, raddr, user, passwd string, db ...string) *Conn {
	return &Conn{mysql.New(proto, laddr, raddr, user, passwd, db...), 7, false}
}

func (c *Conn) reconnectIfNetErr(nn *int, err *error) {
	for *err != nil && IsNetErr(*err) && *nn <= c.MaxRetries {
		if c.Debug {
			log.Printf("Error: '%s' - reconnecting...", *err)
		}
		time.Sleep(1e9 * time.Duration(*nn))
		*err = c.Raw.Reconnect()
		if c.Debug && *err != nil {
			log.Println("Can't reconnect:", *err)
		}
		*nn++
	}
}

func (c *Conn) connectIfNotConnected() (err error) {
	if c.Raw.IsConnected() {
		return
	}
	err = c.Raw.Connect()
	nn := 0
	c.reconnectIfNetErr(&nn, &err)
	return
}

// Automatic connect/reconnect/repeat version of Use
func (c *Conn) Use(dbname string) (err error) {
	if err = c.connectIfNotConnected(); err != nil {
		return
	}
	nn := 0
	for {
		if err = c.Raw.Use(dbname); err == nil {
			return
		}
		if c.reconnectIfNetErr(&nn, &err); err != nil {
			return
		}
	}
	panic(nil)
}

// Automatic connect/reconnect/repeat version of Query
func (c *Conn) Query(sql string, params ...interface{}) (rows []mysql.Row, res mysql.Result, err error) {

	if err = c.connectIfNotConnected(); err != nil {
		return
	}
	nn := 0
	for {
		if rows, res, err = c.Raw.Query(sql, params...); err == nil {
			return
		}
		if c.reconnectIfNetErr(&nn, &err); err != nil {
			return
		}
	}
	panic(nil)
}

type Stmt struct {
	Raw mysql.Stmt
	con *Conn
}

// Automatic connect/reconnect/repeat version of Prepare
func (c *Conn) Prepare(sql string) (*Stmt, error) {
	if err := c.connectIfNotConnected(); err != nil {
		return nil, err
	}
	nn := 0
	for {
		var (
			err error
			s   mysql.Stmt
		)
		if s, err = c.Raw.Prepare(sql); err == nil {
			return &Stmt{s, c}, nil
		}
		if c.reconnectIfNetErr(&nn, &err); err != nil {
			return nil, err
		}
	}
	panic(nil)
}

// Automatic connect/reconnect/repeat version of Exec
func (s *Stmt) Exec(params ...interface{}) (rows []mysql.Row, res mysql.Result, err error) {

	if err = s.con.connectIfNotConnected(); err != nil {
		return
	}
	nn := 0
	for {
		if rows, res, err = s.Raw.Exec(params...); err == nil {
			return
		}
		if s.con.reconnectIfNetErr(&nn, &err); err != nil {
			return
		}
	}
	panic(nil)
}
