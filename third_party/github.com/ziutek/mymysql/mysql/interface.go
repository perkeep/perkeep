// MySQL Client API written entirely in Go without any external dependences.
package mysql

import (
	"time"
)

type ConnCommon interface {
	Start(sql string, params ...interface{}) (Result, error)
	Prepare(sql string) (Stmt, error)

	Ping() error
	ThreadId() uint32
	Escape(txt string) string

	Query(sql string, params ...interface{}) ([]Row, Result, error)
	QueryFirst(sql string, params ...interface{}) (Row, Result, error)
	QueryLast(sql string, params ...interface{}) (Row, Result, error)
}

type Conn interface {
	ConnCommon

	Clone() Conn
	SetTimeout(time.Duration)
	Connect() error
	Close() error
	IsConnected() bool
	Reconnect() error
	Use(dbname string) error
	Register(sql string)
	SetMaxPktSize(new_size int) int
	NarrowTypeSet(narrow bool)
	FullFieldInfo(full bool)

	Begin() (Transaction, error)
}

type Transaction interface {
	ConnCommon

	Commit() error
	Rollback() error
	Do(st Stmt) Stmt
	IsValid() bool
}

type Stmt interface {
	Bind(params ...interface{})
	Run(params ...interface{}) (Result, error)
	Delete() error
	Reset() error
	SendLongData(pnum int, data interface{}, pkt_size int) error

	Fields() []*Field
	NumParam() int
	WarnCount() int

	Exec(params ...interface{}) ([]Row, Result, error)
	ExecFirst(params ...interface{}) (Row, Result, error)
	ExecLast(params ...interface{}) (Row, Result, error)
}

type Result interface {
	StatusOnly() bool
	ScanRow(Row) error
	GetRow() (Row, error)

	MoreResults() bool
	NextResult() (Result, error)

	Fields() []*Field
	Map(string) int
	Message() string
	AffectedRows() uint64
	InsertId() uint64
	WarnCount() int

	MakeRow() Row
	GetRows() ([]Row, error)
	End() error
	GetFirstRow() (Row, error)
	GetLastRow() (Row, error)
}

var New func(proto, laddr, raddr, user, passwd string, db ...string) Conn
