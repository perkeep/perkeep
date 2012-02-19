/*
Copyright 2011 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mysqlindexer

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	mysql "camli/third_party/github.com/camlistore/GoMySQL"
)

var _ = log.Printf

type MySQLWrapper struct {
	// Host may optionally end in ":port".
	Host, User, Password, Database string

	clientLock    sync.Mutex
	cachedClients []*mysql.Client
}

// ResultSet is a cursor. It starts before the first row.
type ResultSet interface {
	// Move cursor to next row, returning true if there's a next
	// row.
	Next() bool

	Scan(ptrs ...interface{}) error
	Close() error
}

type emptyResultSet struct{}

func (emptyResultSet) Next() bool                     { return false }
func (emptyResultSet) Scan(ptrs ...interface{}) error { return errors.New("bogus") }
func (emptyResultSet) Close() error                   { return nil }

type myRes struct {
	mw  *MySQLWrapper
	c   *mysql.Client
	sql string
	s   *mysql.Statement
	res *mysql.Result

	row    mysql.Row // type Row []interface{} (or nil on EOF)
	closed bool
}

func (r *myRes) Next() bool {
	r.row = r.res.FetchRow()
	return r.row != nil
}

func scanAssign(idx int, field *mysql.Field, fromi, destPtr interface{}) error {
	switch v := fromi.(type) {
	case string:
		if strPtr, ok := destPtr.(*string); ok {
			*strPtr = v
			return nil
		}
	case int64:
		if p, ok := destPtr.(*int64); ok {
			*p = v
			return nil
		}
	}
	return fmt.Errorf("Scan index %d: invalid conversion from %T -> %T", idx, fromi, destPtr)
}

func decodeColumn(idx int, field *mysql.Field, val interface{}) (interface{}, error) {
	var dec interface{}
	var err error
	switch v := val.(type) {
	case int64:
		return v, nil
	case []byte:
		switch field.Type {
		case mysql.FIELD_TYPE_TINY, mysql.FIELD_TYPE_SHORT, mysql.FIELD_TYPE_YEAR, mysql.FIELD_TYPE_INT24, mysql.FIELD_TYPE_LONG, mysql.FIELD_TYPE_LONGLONG:
			if field.Flags&mysql.FLAG_UNSIGNED != 0 {
				dec, err = strconv.ParseUint(string(v), 10, 64)
			} else {
				dec, err = strconv.ParseInt(string(v), 10, 64)
			}
			if err != nil {
				return nil, fmt.Errorf("mysql: strconv.Atoi64 error on field %d: %v", idx, err)
			}
		case mysql.FIELD_TYPE_FLOAT, mysql.FIELD_TYPE_DOUBLE:
			dec, err = strconv.ParseFloat(string(v), 64)
			if err != nil {
				return nil, fmt.Errorf("mysql: strconv.Atof64 error on field %d: %v", idx, err)
			}
		case mysql.FIELD_TYPE_DECIMAL, mysql.FIELD_TYPE_NEWDECIMAL, mysql.FIELD_TYPE_VARCHAR, mysql.FIELD_TYPE_VAR_STRING, mysql.FIELD_TYPE_STRING:
			dec = string(v)
		default:
			return nil, fmt.Errorf("row[%d] was a []byte but unexpected field type %d", idx, field.Type)
		}
		return dec, nil
	}
	return nil, fmt.Errorf("expected row[%d] contents to be a []byte, got %T for field type %d", idx, val, field.Type)
}

func (r *myRes) Scan(ptrs ...interface{}) (outerr error) {
	defer func() {
		if outerr != nil {
			log.Printf("Scan error on %q: %v", r.sql, outerr)
		}
	}()
	if r.row == nil {
		return errors.New("mysql: Scan called but cursor isn't on a valid row. (Next must return true before calling Scan)")
	}
	if uint64(len(ptrs)) != r.res.FieldCount() {
		return fmt.Errorf("mysql: result set has %d fields doesn't match %d arguments to Scan",
			r.res.FieldCount(), len(ptrs))
	}
	if len(r.row) != len(ptrs) {
		panic(fmt.Sprintf("GoMySQL library is confused. row size is %d, expect %d", len(r.row), len(ptrs)))
	}
	fields := r.res.FetchFields() // just an accessor, doesn't fetch anything

	for i, ptr := range ptrs {
		field := fields[i]
		dec, err := decodeColumn(i, field, r.row[i])
		if err != nil {
			return err
		}

		if err := scanAssign(i, field, dec, ptr); err != nil {
			return err
		}

	}
	return nil
}

func (r *myRes) Close() error {
	if r.closed {
		return errors.New("mysqlwrapper: ResultSet already closed")
	}
	r.closed = true
	if err := r.s.Close(); err != nil {
		return err
	}
	if r.res != nil {
		r.res.Free()
	}
	r.mw.releaseConnection(r.c)
	r.c = nil
	r.s = nil
	r.res = nil
	return nil
}

func (mw *MySQLWrapper) Execute(sql string, params ...interface{}) error {
	rs, err := mw.Query(sql, params...)
	if rs != nil {
		rs.Close()
	}
	return err
}

func (mw *MySQLWrapper) Query(sql string, params ...interface{}) (ResultSet, error) {
	c, err := mw.getConnection()
	if err != nil {
		return nil, err
	}
	s, err := c.Prepare(sql)
	if err != nil {
		c.Close() // defensive. TODO: figure out when safe not to.
		return nil, err
	}
	if len(params) > 0 {
		for i, pv := range params {
			// TODO: check that they're all supported.
			// fallback: if a Stringer, use that.
			_ = i
			_ = pv
		}
		if err := s.BindParams(params...); err != nil {
			if strings.Contains(err.Error(), "Invalid parameter number") {
				println("Invalid parameters for query: ", sql)
			}
			c.Close()
			return nil, err
		}
	}
	if err := s.Execute(); err != nil {
		c.Close() // defensive. TODO: figure out when safe not to.
		return nil, err
	}
	res, err := s.UseResult()
	if err != nil {
		if ce, ok := err.(*mysql.ClientError); ok && ce.Errno == mysql.CR_NO_RESULT_SET {
			mw.releaseConnection(c)
			return emptyResultSet{}, nil
		}
		c.Close() // defensive. TODO: figure out when safe not to.
		return nil, err
	}
	return &myRes{mw: mw, c: c, s: s, sql: sql, res: res}, nil
}

func testClient(client *mysql.Client) error {
	err := client.Query("SELECT 1 + 1")
	if err != nil {
		return err
	}
	_, err = client.UseResult()
	if err != nil {
		return err
	}
	client.FreeResult()
	return nil
}

func (mw *MySQLWrapper) Ping() error {
	client, err := mw.getConnection()
	if err != nil {
		return err
	}
	defer mw.releaseConnection(client)
	return testClient(client)
}

// Get a free cached connection or allocate a new one.
func (mw *MySQLWrapper) getConnection() (client *mysql.Client, err error) {
	mw.clientLock.Lock()
	if len(mw.cachedClients) > 0 {
		defer mw.clientLock.Unlock()
		client = mw.cachedClients[len(mw.cachedClients)-1]
		mw.cachedClients = mw.cachedClients[:len(mw.cachedClients)-1]
		// TODO: Outside the mutex, double check that the client is still good.
		return
	}
	mw.clientLock.Unlock()

	client, err = mysql.DialTCP(mw.Host, mw.User, mw.Password, mw.Database)
	return
}

// Release a client to the cached client pool.
func (mw *MySQLWrapper) releaseConnection(client *mysql.Client) {
	// Test the client before returning it.
	// TODO: this is overkill probably.
	if err := testClient(client); err != nil {
		return
	}
	mw.clientLock.Lock()
	defer mw.clientLock.Unlock()
	mw.cachedClients = append(mw.cachedClients, client)
}
