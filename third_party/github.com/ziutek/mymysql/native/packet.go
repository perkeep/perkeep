package native

import (
	"bufio"
	"errors"
	"io"
)

type pktReader struct {
	rd     *bufio.Reader
	seq    *byte
	remain int
	last   bool
}

func (my *Conn) newPktReader() *pktReader {
	return &pktReader{rd: my.rd, seq: &my.seq}
}

func (pr *pktReader) Read(buf []byte) (num int, err error) {
	if len(buf) == 0 {
		return 0, nil
	}
	defer catchError(&err)

	if pr.remain == 0 {
		// No data to read from current packet
		if pr.last {
			// No more packets
			return 0, io.EOF
		}
		// Read next packet header
		pr.remain = int(readU24(pr.rd))
		seq := readByte(pr.rd)
		// Chceck sequence number
		if *pr.seq != seq {
			return 0, SEQ_ERROR
		}
		*pr.seq++
		// Last packet?
		pr.last = (pr.remain != 0xffffff)
	}
	// Reading data
	if len(buf) <= pr.remain {
		num, err = pr.rd.Read(buf)
	} else {
		num, err = pr.rd.Read(buf[0:pr.remain])
	}
	pr.remain -= num
	return
}

func (pr *pktReader) readAll() (buf []byte) {
	buf = make([]byte, pr.remain)
	nn := 0
	for {
		readFull(pr, buf[nn:])
		if pr.last {
			break
		}
		// There is next packet to read
		new_buf := make([]byte, len(buf)+pr.remain)
		copy(new_buf[nn:], buf)
		nn += len(buf)
		buf = new_buf
	}
	return
}

func (pr *pktReader) unreadByte() {
	if err := pr.rd.UnreadByte(); err != nil {
		panic(err)
	}
	pr.remain++
}

func (pr *pktReader) eof() bool {
	return pr.remain == 0 && pr.last
}

func (pr *pktReader) checkEof() {
	if !pr.eof() {
		panic(PKT_LONG_ERROR)
	}
}

type pktWriter struct {
	wr       *bufio.Writer
	seq      *byte
	remain   int
	to_write int
	last     bool
}

func (my *Conn) newPktWriter(to_write int) *pktWriter {
	return &pktWriter{wr: my.wr, seq: &my.seq, to_write: to_write}
}

/*func writePktHeader(wr io.Writer, seq byte, pay_len int) {
    writeU24(wr, uint32(pay_len))
    writeByte(wr, seq)
}*/

func (pw *pktWriter) Write(buf []byte) (num int, err error) {
	if len(buf) == 0 {
		return
	}
	defer catchError(&err)

	var nn int
	for len(buf) != 0 {
		if pw.remain == 0 {
			if pw.to_write == 0 {
				err = errors.New("too many data for write as packet")
				return
			}
			if pw.to_write >= 0xffffff {
				pw.remain = 0xffffff
			} else {
				pw.remain = pw.to_write
				pw.last = true
			}
			pw.to_write -= pw.remain
			// Write packet header
			writeU24(pw.wr, uint32(pw.remain))
			writeByte(pw.wr, *pw.seq)
			// Update sequence number
			*pw.seq++
		}
		nn = len(buf)
		if nn > pw.remain {
			nn = pw.remain
		}
		nn, err = pw.wr.Write(buf[0:nn])
		num += nn
		pw.remain -= nn
		if err != nil {
			return
		}
		buf = buf[nn:]
	}
	if pw.remain+pw.to_write == 0 {
		if !pw.last {
			// Write  header for empty packet
			writeU24(pw.wr, 0)
			writeByte(pw.wr, *pw.seq)
			// Update sequence number
			*pw.seq++
		}
		// Flush bufio buffers
		err = pw.wr.Flush()
	}
	return
}
