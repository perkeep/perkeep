package native

import (
	"io"
	"runtime"
)

var tab8s = "        "

func readFull(rd io.Reader, buf []byte) {
	for nn := 0; nn < len(buf); {
		kk, err := rd.Read(buf[nn:])
		nn += kk
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			panic(err)
		}
	}
}

func read(rd io.Reader, nn int) (buf []byte) {
	buf = make([]byte, nn)
	readFull(rd, buf)
	return
}

func readByte(rd io.Reader) byte {
	buf := make([]byte, 1)
	if _, err := rd.Read(buf); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		panic(err)
	}
	return buf[0]
}

func write(wr io.Writer, buf []byte) {
	if _, err := wr.Write(buf); err != nil {
		panic(err)
	}
}

func writeByte(wr io.Writer, ch byte) {
	write(wr, []byte{ch})
}

func writeString(wr io.Writer, str string) {
	write(wr, []byte(str))
}

func writeBS(wr io.Writer, bs interface{}) {
	switch buf := bs.(type) {
	case string:
		writeString(wr, buf)
	case []byte:
		write(wr, buf)
	default:
		panic("Can't write: argument isn't a string nor []byte")
	}
}

func lenBS(bs interface{}) int {
	switch buf := bs.(type) {
	case string:
		return len(buf)
	case []byte:
		return len(buf)
	}
	panic("Can't get length: argument isn't a string nor []byte")
}

func catchError(err *error) {
	if pv := recover(); pv != nil {
		switch e := pv.(type) {
		case runtime.Error:
			panic(pv)
		case error:
			*err = e
		default:
			panic(pv)
		}
	}
}
