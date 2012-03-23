package native

import (
	"github.com/ziutek/mymysql/mysql"
	"log"
)

type Stmt struct {
	my *Conn

	id  uint32
	sql string // For reprepare during reconnect

	params []*paramValue // Parameters binding
	rebind bool

	fields []*mysql.Field
	fc_map map[string]int // Maps field name to column number

	field_count   int
	param_count   int
	warning_count int
	status        uint16
}

// Returns index for given name or -1 if field of that name doesn't exist
func (res *Stmt) Map(field_name string) int {
	if fi, ok := res.fc_map[field_name]; ok {
		return fi
	}
	return -1
}

func (stmt *Stmt) NumField() int {
	return stmt.field_count
}

func (stmt *Stmt) NumParam() int {
	return stmt.param_count
}

func (stmt *Stmt) WarnCount() int {
	return stmt.warning_count
}

func (stmt *Stmt) sendCmdExec() {
	// Calculate packet length and NULL bitmap
	null_bitmap := make([]byte, (stmt.param_count+7)>>3)
	pkt_len := 1 + 4 + 1 + 4 + 1 + len(null_bitmap)
	for ii, param := range stmt.params {
		par_len := param.Len()
		pkt_len += par_len
		if par_len == 0 {
			null_byte := ii >> 3
			null_mask := byte(1) << uint(ii-(null_byte<<3))
			null_bitmap[null_byte] |= null_mask
		}
	}
	if stmt.rebind {
		pkt_len += stmt.param_count * 2
	}
	// Reset sequence number
	stmt.my.seq = 0
	// Packet sending
	pw := stmt.my.newPktWriter(pkt_len)
	writeByte(pw, _COM_STMT_EXECUTE)
	writeU32(pw, stmt.id)
	writeByte(pw, 0) // flags = CURSOR_TYPE_NO_CURSOR
	writeU32(pw, 1)  // iteration_count
	write(pw, null_bitmap)
	if stmt.rebind {
		writeByte(pw, 1)
		// Types
		for _, param := range stmt.params {
			writeU16(pw, param.typ)
		}
	} else {
		writeByte(pw, 0)
	}
	// Values
	for _, param := range stmt.params {
		writeValue(pw, param)
	}

	if stmt.my.Debug {
		log.Printf("[%2d <-] Exec command packet: len=%d, null_bitmap=%v, rebind=%t",
			stmt.my.seq-1, pkt_len, null_bitmap, stmt.rebind)
	}

	// Mark that we sended information about binded types
	stmt.rebind = false
}

func (my *Conn) getPrepareResult(stmt *Stmt) interface{} {
loop:
	pr := my.newPktReader() // New reader for next packet
	pkt0 := readByte(pr)

	//log.Println("pkt0:", pkt0, "stmt:", stmt)

	if pkt0 == 255 {
		// Error packet
		my.getErrorPacket(pr)
	}

	if stmt == nil {
		if pkt0 == 0 {
			// OK packet
			return my.getPrepareOkPacket(pr)
		}
	} else {
		unreaded_params := (stmt.param_count < len(stmt.params))
		switch {
		case pkt0 == 254:
			// EOF packet
			stmt.warning_count, stmt.status = my.getEofPacket(pr)
			stmt.my.status = stmt.status
			return stmt

		case pkt0 > 0 && pkt0 < 251 && (stmt.field_count < len(stmt.fields) ||
			unreaded_params):
			// Field packet
			if unreaded_params {
				// Read and ignore parameter field. Sentence from MySQL source:
				/* skip parameters data: we don't support it yet */
				my.getFieldPacket(pr)
				// Increment field count
				stmt.param_count++
			} else {
				field := my.getFieldPacket(pr)
				stmt.fields[stmt.field_count] = field
				stmt.fc_map[field.Name] = stmt.field_count
				// Increment field count
				stmt.field_count++
			}
			// Read next packet
			goto loop
		}
	}
	panic(UNK_RESULT_PKT_ERROR)
}

func (my *Conn) getPrepareOkPacket(pr *pktReader) (stmt *Stmt) {
	if my.Debug {
		log.Printf("[%2d ->] Perpared OK packet:", my.seq-1)
	}

	stmt = new(Stmt)
	stmt.my = my
	// First byte was readed by getPrepRes
	stmt.id = readU32(pr)
	stmt.fields = make([]*mysql.Field, int(readU16(pr))) // FieldCount
	stmt.params = make([]*paramValue, int(readU16(pr)))  // ParamCount
	read(pr, 1)
	stmt.warning_count = int(readU16(pr))
	pr.checkEof()

	// Make field map if fields exists.
	if len(stmt.fields) > 0 {
		stmt.fc_map = make(map[string]int)
	}
	if my.Debug {
		log.Printf(tab8s+"ID=0x%x ParamCount=%d FieldsCount=%d WarnCount=%d",
			stmt.id, len(stmt.params), len(stmt.fields), stmt.warning_count,
		)
	}
	return
}
