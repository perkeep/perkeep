package native

import (
	"bytes"
	"crypto/sha1"
	"github.com/ziutek/mymysql/mysql"
	"io"
	"time"
)

// Integers

func DecodeU16(buf []byte) uint16 {
	return uint16(buf[1])<<8 | uint16(buf[0])
}
func readU16(rd io.Reader) uint16 {
	buf := make([]byte, 2)
	readFull(rd, buf)
	return DecodeU16(buf)
}

func DecodeU24(buf []byte) uint32 {
	return (uint32(buf[2])<<8|uint32(buf[1]))<<8 | uint32(buf[0])
}
func readU24(rd io.Reader) uint32 {
	buf := make([]byte, 3)
	readFull(rd, buf)
	return DecodeU24(buf)
}

func DecodeU32(buf []byte) uint32 {
	return ((uint32(buf[3])<<8|uint32(buf[2]))<<8|
		uint32(buf[1]))<<8 | uint32(buf[0])
}
func readU32(rd io.Reader) uint32 {
	buf := make([]byte, 4)
	readFull(rd, buf)
	return DecodeU32(buf)
}

func DecodeU64(buf []byte) (rv uint64) {
	for ii, vv := range buf {
		rv |= uint64(vv) << uint(ii*8)
	}
	return
}
func readU64(rd io.Reader) (rv uint64) {
	buf := make([]byte, 8)
	readFull(rd, buf)
	return DecodeU64(buf)
}

func EncodeU16(val uint16) []byte {
	return []byte{byte(val), byte(val >> 8)}
}
func writeU16(wr io.Writer, val uint16) {
	write(wr, EncodeU16(val))
}

func EncodeU24(val uint32) []byte {
	return []byte{byte(val), byte(val >> 8), byte(val >> 16)}
}
func writeU24(wr io.Writer, val uint32) {
	write(wr, EncodeU24(val))
}

func EncodeU32(val uint32) []byte {
	return []byte{byte(val), byte(val >> 8), byte(val >> 16), byte(val >> 24)}
}
func writeU32(wr io.Writer, val uint32) {
	write(wr, EncodeU32(val))
}

func EncodeU64(val uint64) []byte {
	buf := make([]byte, 8)
	for ii := range buf {
		buf[ii] = byte(val >> uint(ii*8))
	}
	return buf
}
func writeU64(wr io.Writer, val uint64) {
	write(wr, EncodeU64(val))
}

// Variable length values

func readNullLCB(rd io.Reader) (lcb uint64, null bool) {
	bb := readByte(rd)
	switch bb {
	case 251:
		null = true
	case 252:
		lcb = uint64(readU16(rd))
	case 253:
		lcb = uint64(readU24(rd))
	case 254:
		lcb = readU64(rd)
	default:
		lcb = uint64(bb)
	}
	return
}

func readLCB(rd io.Reader) uint64 {
	lcb, null := readNullLCB(rd)
	if null {
		panic(UNEXP_NULL_LCB_ERROR)
	}
	return lcb
}

func writeLCB(wr io.Writer, val uint64) {
	switch {
	case val <= 250:
		writeByte(wr, byte(val))

	case val <= 0xffff:
		writeByte(wr, 252)
		writeU16(wr, uint16(val))

	case val <= 0xffffff:
		writeByte(wr, 253)
		writeU24(wr, uint32(val))

	default:
		writeByte(wr, 254)
		writeU64(wr, val)
	}
}

func lenLCB(val uint64) int {
	switch {
	case val <= 250:
		return 1

	case val <= 0xffff:
		return 3

	case val <= 0xffffff:
		return 4
	}
	return 9
}

func readNullBin(rd io.Reader) (buf []byte, null bool) {
	var l uint64
	l, null = readNullLCB(rd)
	if null {
		return
	}
	buf = make([]byte, l)
	readFull(rd, buf)
	return
}

func readBin(rd io.Reader) []byte {
	buf, null := readNullBin(rd)
	if null {
		panic(UNEXP_NULL_LCS_ERROR)
	}
	return buf
}

func writeBin(wr io.Writer, buf []byte) {
	writeLCB(wr, uint64(len(buf)))
	write(wr, buf)
}

func lenBin(buf []byte) int {
	return lenLCB(uint64(len(buf))) + len(buf)
}

func readStr(rd io.Reader) (str string) {
	buf := readBin(rd)
	str = string(buf)
	return
}

func writeStr(wr io.Writer, str string) {
	writeLCB(wr, uint64(len(str)))
	writeString(wr, str)
}

func lenStr(str string) int {
	return lenLCB(uint64(len(str))) + len(str)
}

func writeLC(wr io.Writer, v interface{}) {
	switch val := v.(type) {
	case []byte:
		writeBin(wr, val)
	case *[]byte:
		writeBin(wr, *val)
	case string:
		writeStr(wr, val)
	case *string:
		writeStr(wr, *val)
	default:
		panic("Unknown data type for write as lenght coded string")
	}
}

func lenLC(v interface{}) int {
	switch val := v.(type) {
	case []byte:
		return lenBin(val)
	case *[]byte:
		return lenBin(*val)
	case string:
		return lenStr(val)
	case *string:
		return lenStr(*val)
	}
	panic("Unknown data type for write as lenght coded string")
}

func readNTB(rd io.Reader) (buf []byte) {
	bb := new(bytes.Buffer)
	for {
		ch := readByte(rd)
		if ch == 0 {
			return bb.Bytes()
		}
		bb.WriteByte(ch)
	}
	return
}

func writeNTB(wr io.Writer, buf []byte) {
	write(wr, buf)
	writeByte(wr, 0)
}

func readNTS(rd io.Reader) (str string) {
	buf := readNTB(rd)
	str = string(buf)
	return
}

func writeNTS(wr io.Writer, str string) {
	writeNTB(wr, []byte(str))
}

func writeNT(wr io.Writer, v interface{}) {
	switch val := v.(type) {
	case []byte:
		writeNTB(wr, val)
	case string:
		writeNTS(wr, val)
	default:
		panic("Unknown type for write as null terminated data")
	}
}

// Date and time

func readDuration(rd io.Reader) time.Duration {
	dlen := readByte(rd)
	switch dlen {
	case 251:
		// Null
		panic(UNEXP_NULL_TIME_ERROR)
	case 0:
		// 00:00:00
		return 0
	case 5, 8, 12:
		// Properly time length
	default:
		panic(WRONG_DATE_LEN_ERROR)
	}
	buf := make([]byte, dlen)
	readFull(rd, buf)
	tt := int64(0)
	switch dlen {
	case 12:
		// Nanosecond part
		tt += int64(DecodeU32(buf[8:]))
		fallthrough
	case 8:
		// HH:MM:SS part
		tt += int64(int(buf[5])*3600+int(buf[6])*60+int(buf[7])) * 1e9
		fallthrough
	case 5:
		// Day part
		tt += int64(DecodeU32(buf[1:5])) * (24 * 3600 * 1e9)
		fallthrough
	}
	if buf[0] != 0 {
		tt = -tt
	}
	return time.Duration(tt)
}

func EncodeDuration(d time.Duration) []byte {
	buf := make([]byte, 13)
	if d < 0 {
		buf[1] = 1
		d = -d
	}
	if ns := uint32(d % 1e9); ns != 0 {
		copy(buf[9:13], EncodeU32(ns)) // nanosecond
		buf[0] += 4
	}
	d /= 1e9
	if hms := int(d % (24 * 3600)); buf[0] != 0 || hms != 0 {
		buf[8] = byte(hms % 60) // second
		hms /= 60
		buf[7] = byte(hms % 60) // minute
		buf[6] = byte(hms / 60) // hour
		buf[0] += 3
	}
	if day := uint32(d / (24 * 3600)); buf[0] != 0 || day != 0 {
		copy(buf[2:6], EncodeU32(day)) // day
		buf[0] += 4
	}
	buf[0]++ // For sign byte
	buf = buf[0 : buf[0]+1]
	return buf
}

func writeDuration(wr io.Writer, d time.Duration) {
	write(wr, EncodeDuration(d))
}

func lenDuration(d time.Duration) int {
	if d == 0 {
		return 2
	}
	if d%1e9 != 0 {
		return 13
	}
	d /= 1e9
	if d%(24*3600) != 0 {
		return 9
	}
	return 6
}

func readTime(rd io.Reader) time.Time {
	dlen := readByte(rd)
	switch dlen {
	case 251:
		// Null
		panic(UNEXP_NULL_DATE_ERROR)
	case 0:
		// return 0000-00-00 converted to time.Time zero
		return time.Time{}
	case 4, 7, 11:
		// Properly datetime length
	default:
		panic(WRONG_DATE_LEN_ERROR)
	}

	buf := make([]byte, dlen)
	readFull(rd, buf)
	var y, mon, d, h, m, s, n int
	switch dlen {
	case 11:
		// 2006-01-02 15:04:05.001004005
		n = int(DecodeU32(buf[7:]))
		fallthrough
	case 7:
		// 2006-01-02 15:04:05
		h = int(buf[4])
		m = int(buf[5])
		s = int(buf[6])
		fallthrough
	case 4:
		// 2006-01-02
		y = int(DecodeU16(buf[0:2]))
		mon = int(buf[2])
		d = int(buf[3])
	}
	return time.Date(y, time.Month(mon), d, h, m, s, n, time.Local)
}

func encodeNonzeroTime(y int16, mon, d, h, m, s byte, n uint32) []byte {
	buf := make([]byte, 12)
	switch {
	case n != 0:
		copy(buf[7:12], EncodeU32(n))
		buf[0] += 4
		fallthrough
	case s != 0 || m != 0 || h != 0:
		buf[7] = s
		buf[6] = m
		buf[5] = h
		buf[0] += 3
	}
	buf[4] = d
	buf[3] = mon
	copy(buf[1:3], EncodeU16(uint16(y)))
	buf[0] += 4
	buf = buf[0 : buf[0]+1]
	return buf
}

func EncodeTime(t time.Time) []byte {
	if t.IsZero() {
		return []byte{0} // MySQL zero
	}
	y, mon, d := t.Date()
	h, m, s := t.Clock()
	n := t.Nanosecond()
	return encodeNonzeroTime(
		int16(y), byte(mon), byte(d),
		byte(h), byte(m), byte(s), uint32(n),
	)
}

func writeTime(wr io.Writer, t time.Time) {
	write(wr, EncodeTime(t))
}

func lenTime(t time.Time) int {
	switch {
	case t.IsZero():
		return 1
	case t.Nanosecond() != 0:
		return 12
	case t.Second() != 0 || t.Minute() != 0 || t.Hour() != 0:
		return 8
	}
	return 5
}

func readDate(rd io.Reader) mysql.Date {
	y, m, d := readTime(rd).Date()
	return mysql.Date{int16(y), byte(m), byte(d)}
}

func EncodeDate(d mysql.Date) []byte {
	if d.IsZero() {
		return []byte{0} // MySQL zero
	}
	return encodeNonzeroTime(d.Year, d.Month, d.Day, 0, 0, 0, 0)
}

func writeDate(wr io.Writer, d mysql.Date) {
	write(wr, EncodeDate(d))
}

func lenDate(d mysql.Date) int {
	if d.IsZero() {
		return 1
	}
	return 5
}

// Borrowed from GoMySQL
// SHA1(SHA1(SHA1(password)), scramble) XOR SHA1(password)
func (my *Conn) encryptedPasswd() (out []byte) {
	// Convert password to byte array
	passbytes := []byte(my.passwd)
	// stage1_hash = SHA1(password)
	// SHA1 encode
	crypt := sha1.New()
	crypt.Write(passbytes)
	stg1Hash := crypt.Sum(nil)
	// token = SHA1(SHA1(stage1_hash), scramble) XOR stage1_hash
	// SHA1 encode again
	crypt.Reset()
	crypt.Write(stg1Hash)
	stg2Hash := crypt.Sum(nil)
	// SHA1 2nd hash and scramble
	crypt.Reset()
	crypt.Write(my.info.scramble)
	crypt.Write(stg2Hash)
	stg3Hash := crypt.Sum(nil)
	// XOR with first hash
	out = make([]byte, len(my.info.scramble))
	for ii := range my.info.scramble {
		out[ii] = stg3Hash[ii] ^ stg1Hash[ii]
	}
	return
}

func escapeString(txt string) string {
	var (
		esc string
		buf bytes.Buffer
	)
	last := 0
	for ii, bb := range txt {
		switch bb {
		case 0:
			esc = `\0`
		case '\n':
			esc = `\n`
		case '\r':
			esc = `\r`
		case '\\':
			esc = `\\`
		case '\'':
			esc = `\'`
		case '"':
			esc = `\"`
		case '\032':
			esc = `\Z`
		default:
			continue
		}
		io.WriteString(&buf, txt[last:ii])
		io.WriteString(&buf, esc)
		last = ii + 1
	}
	io.WriteString(&buf, txt[last:])
	return buf.String()
}

func escapeQuotes(txt string) string {
	var buf bytes.Buffer
	last := 0
	for ii, bb := range txt {
		if bb == '\'' {
			io.WriteString(&buf, txt[last:ii])
			io.WriteString(&buf, `''`)
			last = ii + 1
		}
	}
	io.WriteString(&buf, txt[last:])
	return buf.String()
}
