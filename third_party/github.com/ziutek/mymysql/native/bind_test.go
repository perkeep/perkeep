package native

import (
	"bytes"
	"github.com/ziutek/mymysql/mysql"
	"math"
	"reflect"
	"testing"
	"time"
)

var (
	Bytes  = []byte("Ala ma Kota!")
	String = "ssss" //"A kot ma Alę!"
	blob   = mysql.Blob{1, 2, 3}
	dateT  = time.Date(2010, 12, 30, 17, 21, 01, 0, time.Local)
	tstamp = mysql.Timestamp{dateT.Add(1e9)}
	date   = mysql.Date{Year: 2011, Month: 2, Day: 3}
	tim    = -time.Duration((5*24*3600+4*3600+3*60+2)*1e9 + 1)
	bol    = true

	pBytes  *[]byte
	pString *string
	pBlob   *mysql.Blob
	pDateT  *time.Time
	pTstamp *mysql.Timestamp
	pDate   *mysql.Date
	pTim    *time.Duration
	pBol    *bool

	raw = mysql.Raw{MYSQL_TYPE_INT24, &[]byte{3, 2, 1}}

	Int8   = int8(1)
	Uint8  = uint8(2)
	Int16  = int16(3)
	Uint16 = uint16(4)
	Int32  = int32(5)
	Uint32 = uint32(6)
	Int64  = int64(0x7000100020003001)
	Uint64 = uint64(0xffff0000ffff0000)
	Int    = int(7)
	Uint   = uint(8)

	Float32 = float32(1e10)
	Float64 = 256e256

	pInt8    *int8
	pUint8   *uint8
	pInt16   *int16
	pUint16  *uint16
	pInt32   *int32
	pUint32  *uint32
	pInt64   *int64
	pUint64  *uint64
	pInt     *int
	pUint    *uint
	pFloat32 *float32
	pFloat64 *float64
)

type BindTest struct {
	val    interface{}
	typ    uint16
	length int
}

var bindTests = []BindTest{
	BindTest{nil, MYSQL_TYPE_NULL, 0},

	BindTest{Bytes, MYSQL_TYPE_VAR_STRING, -1},
	BindTest{String, MYSQL_TYPE_STRING, -1},
	BindTest{blob, MYSQL_TYPE_BLOB, -1},
	BindTest{dateT, MYSQL_TYPE_DATETIME, -1},
	BindTest{tstamp, MYSQL_TYPE_TIMESTAMP, -1},
	BindTest{date, MYSQL_TYPE_DATE, -1},
	BindTest{tim, MYSQL_TYPE_TIME, -1},
	BindTest{bol, MYSQL_TYPE_TINY, -1},

	BindTest{&Bytes, MYSQL_TYPE_VAR_STRING, -1},
	BindTest{&String, MYSQL_TYPE_STRING, -1},
	BindTest{&blob, MYSQL_TYPE_BLOB, -1},
	BindTest{&dateT, MYSQL_TYPE_DATETIME, -1},
	BindTest{&tstamp, MYSQL_TYPE_TIMESTAMP, -1},
	BindTest{&date, MYSQL_TYPE_DATE, -1},
	BindTest{&tim, MYSQL_TYPE_TIME, -1},

	BindTest{pBytes, MYSQL_TYPE_VAR_STRING, -1},
	BindTest{pString, MYSQL_TYPE_STRING, -1},
	BindTest{pBlob, MYSQL_TYPE_BLOB, -1},
	BindTest{pDateT, MYSQL_TYPE_DATETIME, -1},
	BindTest{pTstamp, MYSQL_TYPE_TIMESTAMP, -1},
	BindTest{pDate, MYSQL_TYPE_DATE, -1},
	BindTest{pTim, MYSQL_TYPE_TIME, -1},
	BindTest{pBol, MYSQL_TYPE_TINY, -1},

	BindTest{raw, MYSQL_TYPE_INT24, -1},

	BindTest{Int8, MYSQL_TYPE_TINY, 1},
	BindTest{Int16, MYSQL_TYPE_SHORT, 2},
	BindTest{Int32, MYSQL_TYPE_LONG, 4},
	BindTest{Int64, MYSQL_TYPE_LONGLONG, 8},
	BindTest{Int, MYSQL_TYPE_LONG, 4}, // Hack

	BindTest{&Int8, MYSQL_TYPE_TINY, 1},
	BindTest{&Int16, MYSQL_TYPE_SHORT, 2},
	BindTest{&Int32, MYSQL_TYPE_LONG, 4},
	BindTest{&Int64, MYSQL_TYPE_LONGLONG, 8},
	BindTest{&Int, MYSQL_TYPE_LONG, 4}, // Hack

	BindTest{pInt8, MYSQL_TYPE_TINY, 1},
	BindTest{pInt16, MYSQL_TYPE_SHORT, 2},
	BindTest{pInt32, MYSQL_TYPE_LONG, 4},
	BindTest{pInt64, MYSQL_TYPE_LONGLONG, 8},
	BindTest{pInt, MYSQL_TYPE_LONG, 4}, // Hack

	BindTest{Uint8, MYSQL_TYPE_TINY | MYSQL_UNSIGNED_MASK, 1},
	BindTest{Uint16, MYSQL_TYPE_SHORT | MYSQL_UNSIGNED_MASK, 2},
	BindTest{Uint32, MYSQL_TYPE_LONG | MYSQL_UNSIGNED_MASK, 4},
	BindTest{Uint64, MYSQL_TYPE_LONGLONG | MYSQL_UNSIGNED_MASK, 8},
	BindTest{Uint, MYSQL_TYPE_LONG | MYSQL_UNSIGNED_MASK, 4}, //Hack

	BindTest{&Uint8, MYSQL_TYPE_TINY | MYSQL_UNSIGNED_MASK, 1},
	BindTest{&Uint16, MYSQL_TYPE_SHORT | MYSQL_UNSIGNED_MASK, 2},
	BindTest{&Uint32, MYSQL_TYPE_LONG | MYSQL_UNSIGNED_MASK, 4},
	BindTest{&Uint64, MYSQL_TYPE_LONGLONG | MYSQL_UNSIGNED_MASK, 8},
	BindTest{&Uint, MYSQL_TYPE_LONG | MYSQL_UNSIGNED_MASK, 4}, //Hack

	BindTest{pUint8, MYSQL_TYPE_TINY | MYSQL_UNSIGNED_MASK, 1},
	BindTest{pUint16, MYSQL_TYPE_SHORT | MYSQL_UNSIGNED_MASK, 2},
	BindTest{pUint32, MYSQL_TYPE_LONG | MYSQL_UNSIGNED_MASK, 4},
	BindTest{pUint64, MYSQL_TYPE_LONGLONG | MYSQL_UNSIGNED_MASK, 8},
	BindTest{pUint, MYSQL_TYPE_LONG | MYSQL_UNSIGNED_MASK, 4}, //Hack

	BindTest{Float32, MYSQL_TYPE_FLOAT, 4},
	BindTest{Float64, MYSQL_TYPE_DOUBLE, 8},

	BindTest{&Float32, MYSQL_TYPE_FLOAT, 4},
	BindTest{&Float64, MYSQL_TYPE_DOUBLE, 8},
}

func makeAddressable(v reflect.Value) reflect.Value {
	if v.IsValid() {
		// Make an addresable value
		av := reflect.New(v.Type()).Elem()
		av.Set(v)
		v = av
	}
	return v
}

func TestBind(t *testing.T) {
	for _, test := range bindTests {
		v := makeAddressable(reflect.ValueOf(test.val))
		val := bindValue(v)
		if val.typ != test.typ || val.length != test.length {
			t.Errorf(
				"Type: %s exp=0x%x res=0x%x Len: exp=%d res=%d",
				reflect.TypeOf(test.val), test.typ, val.typ, test.length,
				val.length,
			)
		}
	}
}

type WriteTest struct {
	val interface{}
	exp []byte
}

var writeTest []WriteTest

func init() {
	b := make([]byte, 64*1024)
	for ii := range b {
		b[ii] = byte(ii)
	}
	blob = mysql.Blob(b)

	writeTest = []WriteTest{
		WriteTest{Bytes, append([]byte{byte(len(Bytes))}, Bytes...)},
		WriteTest{String, append([]byte{byte(len(String))}, []byte(String)...)},
		WriteTest{pBytes, nil},
		WriteTest{pString, nil},
		WriteTest{
			blob,
			append(append([]byte{253}, EncodeU24(uint32(len(blob)))...),
				[]byte(blob)...),
		},
		WriteTest{
			dateT,
			[]byte{
				7, byte(dateT.Year()), byte(dateT.Year() >> 8),
				byte(dateT.Month()),
				byte(dateT.Day()), byte(dateT.Hour()), byte(dateT.Minute()),
				byte(dateT.Second()),
			},
		},
		WriteTest{
			&dateT,
			[]byte{
				7, byte(dateT.Year()), byte(dateT.Year() >> 8),
				byte(dateT.Month()),
				byte(dateT.Day()), byte(dateT.Hour()), byte(dateT.Minute()),
				byte(dateT.Second()),
			},
		},
		WriteTest{
			date,
			[]byte{
				4, byte(date.Year), byte(date.Year >> 8), byte(date.Month),
				byte(date.Day),
			},
		},
		WriteTest{
			&date,
			[]byte{
				4, byte(date.Year), byte(date.Year >> 8), byte(date.Month),
				byte(date.Day),
			},
		},
		WriteTest{
			tim,
			[]byte{12, 1, 5, 0, 0, 0, 4, 3, 2, 1, 0, 0, 0},
		},
		WriteTest{
			&tim,
			[]byte{12, 1, 5, 0, 0, 0, 4, 3, 2, 1, 0, 0, 0},
		},
		WriteTest{bol, []byte{1}},
		WriteTest{&bol, []byte{1}},
		WriteTest{pBol, nil},

		WriteTest{dateT, EncodeTime(dateT)},
		WriteTest{&dateT, EncodeTime(dateT)},
		WriteTest{pDateT, nil},

		WriteTest{tstamp, EncodeTime(tstamp.Time)},
		WriteTest{&tstamp, EncodeTime(tstamp.Time)},
		WriteTest{pTstamp, nil},

		WriteTest{date, EncodeDate(date)},
		WriteTest{&date, EncodeDate(date)},
		WriteTest{pDate, nil},

		WriteTest{tim, EncodeDuration(tim)},
		WriteTest{&tim, EncodeDuration(tim)},
		WriteTest{pTim, nil},

		WriteTest{Int, EncodeU32(uint32(Int))}, // Hack
		WriteTest{Int16, EncodeU16(uint16(Int16))},
		WriteTest{Int32, EncodeU32(uint32(Int32))},
		WriteTest{Int64, EncodeU64(uint64(Int64))},

		WriteTest{Int, EncodeU32(uint32(Int))}, // Hack
		WriteTest{Uint16, EncodeU16(Uint16)},
		WriteTest{Uint32, EncodeU32(Uint32)},
		WriteTest{Uint64, EncodeU64(Uint64)},

		WriteTest{&Int, EncodeU32(uint32(Int))}, // Hack
		WriteTest{&Int16, EncodeU16(uint16(Int16))},
		WriteTest{&Int32, EncodeU32(uint32(Int32))},
		WriteTest{&Int64, EncodeU64(uint64(Int64))},

		WriteTest{&Uint, EncodeU32(uint32(Uint))}, // Hack
		WriteTest{&Uint16, EncodeU16(Uint16)},
		WriteTest{&Uint32, EncodeU32(Uint32)},
		WriteTest{&Uint64, EncodeU64(Uint64)},

		WriteTest{pInt, nil},
		WriteTest{pInt16, nil},
		WriteTest{pInt32, nil},
		WriteTest{pInt64, nil},

		WriteTest{Float32, EncodeU32(math.Float32bits(Float32))},
		WriteTest{Float64, EncodeU64(math.Float64bits(Float64))},

		WriteTest{&Float32, EncodeU32(math.Float32bits(Float32))},
		WriteTest{&Float64, EncodeU64(math.Float64bits(Float64))},

		WriteTest{pFloat32, nil},
		WriteTest{pFloat64, nil},
	}
}

func TestWrite(t *testing.T) {
	buf := new(bytes.Buffer)
	for _, test := range writeTest {
		buf.Reset()
		v := makeAddressable(reflect.ValueOf(test.val))
		val := bindValue(v)
		writeValue(buf, val)
		if !bytes.Equal(buf.Bytes(), test.exp) || val.Len() != len(test.exp) {
			t.Errorf("%s - exp_len=%d res_len=%d exp: %v res: %v",
				reflect.TypeOf(test.val), len(test.exp), val.Len(),
				test.exp, buf.Bytes(),
			)
		}
	}
}

func TestEscapeString(t *testing.T) {
	txt := " \000 \n \r \\ ' \" \032 "
	exp := ` \0 \n \r \\ \' \" \Z `
	out := escapeString(txt)
	if out != exp {
		t.Fatalf("escapeString: ret='%s' exp='%s'", out, exp)
	}
}
func TestEscapeQuotes(t *testing.T) {
	txt := " '' '' ' ' ' "
	exp := ` '''' '''' '' '' '' `
	out := escapeQuotes(txt)
	if out != exp {
		t.Fatalf("escapeString: ret='%s' exp='%s'", out, exp)
	}
}
