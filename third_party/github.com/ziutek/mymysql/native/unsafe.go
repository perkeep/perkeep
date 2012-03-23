package native

import (
	"github.com/ziutek/mymysql/mysql"
	"io"
	"time"
	"unsafe"
)

type paramValue struct {
	typ    uint16
	addr   unsafe.Pointer
	raw    bool
	length int // >=0 - length of value, <0 - unknown length
}

func (pv *paramValue) SetAddr(addr uintptr) {
	pv.addr = unsafe.Pointer(addr)
}

func (val *paramValue) Len() int {
	if val.addr == nil {
		// Invalid Value was binded
		return 0
	}
	// val.addr always points to the pointer - lets dereference it
	ptr := *(*unsafe.Pointer)(val.addr)
	if ptr == nil {
		// Binded Ptr Value is nil
		return 0
	}

	if val.length >= 0 {
		return val.length
	}

	switch val.typ {
	case MYSQL_TYPE_STRING:
		return lenStr(*(*string)(ptr))

	case MYSQL_TYPE_DATE:
		return lenDate(*(*mysql.Date)(ptr))

	case MYSQL_TYPE_TIMESTAMP, MYSQL_TYPE_DATETIME:
		return lenTime(*(*time.Time)(ptr))

	case MYSQL_TYPE_TIME:
		return lenDuration(*(*time.Duration)(ptr))

	case MYSQL_TYPE_TINY: // val.length < 0 so this is bool
		return 1
	}
	// MYSQL_TYPE_VAR_STRING, MYSQL_TYPE_BLOB and type of Raw value
	return lenBin(*(*[]byte)(ptr))
}

func writeValue(wr io.Writer, val *paramValue) {
	if val.addr == nil {
		// Invalid Value was binded
		return
	}
	// val.addr always points to the pointer - lets dereference it
	ptr := *(*unsafe.Pointer)(val.addr)
	if ptr == nil {
		// Binded Ptr Value is nil
		return
	}

	if val.raw || val.typ == MYSQL_TYPE_VAR_STRING ||
		val.typ == MYSQL_TYPE_BLOB {
		writeBin(wr, *(*[]byte)(ptr))
		return
	}
	// We don't need unsigned bit to check type
	switch val.typ & ^MYSQL_UNSIGNED_MASK {
	case MYSQL_TYPE_NULL:
		// Don't write null values

	case MYSQL_TYPE_STRING:
		writeStr(wr, *(*string)(ptr))

	case MYSQL_TYPE_LONG, MYSQL_TYPE_FLOAT:
		writeU32(wr, *(*uint32)(ptr))

	case MYSQL_TYPE_SHORT:
		writeU16(wr, *(*uint16)(ptr))

	case MYSQL_TYPE_TINY:
		if val.length == -1 {
			// Translate bool value to MySQL tiny
			if *(*bool)(ptr) {
				writeByte(wr, 1)
			} else {
				writeByte(wr, 0)
			}
		} else {
			writeByte(wr, *(*byte)(ptr))
		}

	case MYSQL_TYPE_LONGLONG, MYSQL_TYPE_DOUBLE:
		writeU64(wr, *(*uint64)(ptr))

	case MYSQL_TYPE_DATE:
		writeDate(wr, *(*mysql.Date)(ptr))

	case MYSQL_TYPE_TIMESTAMP, MYSQL_TYPE_DATETIME:
		writeTime(wr, *(*time.Time)(ptr))

	case MYSQL_TYPE_TIME:
		writeDuration(wr, *(*time.Duration)(ptr))

	default:
		panic(BIND_UNK_TYPE)
	}
	return
}
