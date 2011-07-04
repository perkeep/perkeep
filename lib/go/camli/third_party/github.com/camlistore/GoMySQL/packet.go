// GoMySQL - A MySQL client library for Go
//
// Copyright 2010-2011 Phil Bayfield. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package mysql

import (
	"bytes"
	"os"
)

// Packet type identifier
type packetType uint32

// Packet types
const (
	PACKET_INIT packetType = 1 << iota
	PACKET_AUTH
	PACKET_OK
	PACKET_ERROR
	PACKET_CMD
	PACKET_RESULT
	PACKET_FIELD
	PACKET_ROW
	PACKET_EOF
	PACKET_PREPARE_OK
	PACKET_PARAM
	PACKET_LONG_DATA
	PACKET_EXECUTE
	PACKET_ROW_BINARY
)

// Readable packet interface
type packetReadable interface {
	read(data []byte) (err os.Error)
}

// Writable packet interface
type packetWritable interface {
	write() (data []byte, err os.Error)
}

// Generic packet interface (read/writable)
type packet interface {
	packetReadable
	packetWritable
}

// Packet base struct
type packetBase struct {
	protocol uint8
	sequence uint8
}

// Read a slice from the data
func (p *packetBase) readSlice(data []byte, delim byte) (slice []byte, err os.Error) {
	pos := bytes.IndexByte(data, delim)
	if pos > -1 {
		slice = data[:pos]
	} else {
		slice = data
		err = os.EOF
	}
	return
}

// Read length coded string
func (p *packetBase) readLengthCodedString(data []byte) (s string, n int, err os.Error) {
	// Read bytes and convert to string
	b, n, err := p.readLengthCodedBytes(data)
	if err != nil {
		return
	}
	s = string(b)
	return
}

func (p *packetBase) readLengthCodedBytes(data []byte) (b []byte, n int, err os.Error) {
	// Get string length
	num, n, err := btolcb(data)
	if err != nil {
		return
	}
	// Check data length
	if len(data) < n+int(num) {
		err = os.EOF
		return
	}
	// Get bytes
	b = data[n : n+int(num)]
	n += int(num)
	return
}

// Prepend packet data with header info
func (p *packetBase) addHeader(data []byte) (pkt []byte) {
	pkt = ui24tob(uint32(len(data)))
	pkt = append(pkt, p.sequence)
	pkt = append(pkt, data...)
	return
}


// Init packet
type packetInit struct {
	packetBase
	protocolVersion uint8
	serverVersion   string
	threadId        uint32
	scrambleBuff    []byte
	serverCaps      uint16
	serverLanguage  uint8
	serverStatus    uint16
}

// Init packet reader
func (p *packetInit) read(data []byte) (err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Position
	pos := 0
	// Protocol version [8 bit uint]
	p.protocolVersion = data[pos]
	pos++
	// Server version [null terminated string]
	slice, err := p.readSlice(data[pos:], 0x00)
	if err != nil {
		return
	}
	p.serverVersion = string(slice)
	pos += len(slice) + 1
	// Thread id [32 bit uint]
	p.threadId = btoui32(data[pos : pos+4])
	pos += 4
	// First part of scramble buffer [8 bytes]
	p.scrambleBuff = make([]byte, 8)
	p.scrambleBuff = data[pos : pos+8]
	pos += 9
	// Server capabilities [16 bit uint]
	p.serverCaps = btoui16(data[pos : pos+2])
	pos += 2
	// Server language [8 bit uint]
	p.serverLanguage = data[pos]
	pos++
	// Server status [16 bit uint]
	p.serverStatus = btoui16(data[pos : pos+2])
	pos += 15
	// Second part of scramble buffer, if exists (4.1+) [13 bytes]
	if ClientFlag(p.serverCaps)&CLIENT_PROTOCOL_41 > 0 {
		p.scrambleBuff = append(p.scrambleBuff, data[pos:pos+12]...)
	}
	return
}

// Auth packet
type packetAuth struct {
	packetBase
	clientFlags   uint32
	maxPacketSize uint32
	charsetNumber uint8
	user          string
	scrambleBuff  []byte
	database      string
}

// Auth packet writer
func (p *packetAuth) write() (data []byte, err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// For MySQL 4.1+
	if p.protocol == PROTOCOL_41 {
		// Client flags
		data = ui32tob(p.clientFlags)
		// Max packet size
		data = append(data, ui32tob(p.maxPacketSize)...)
		// Charset
		data = append(data, p.charsetNumber)
		// Filler
		data = append(data, make([]byte, 23)...)
		// User
		if len(p.user) > 0 {
			data = append(data, []byte(p.user)...)
		}
		// Terminator
		data = append(data, 0x0)
		// Scramble buffer
		data = append(data, byte(len(p.scrambleBuff)))
		if len(p.scrambleBuff) > 0 {
			data = append(data, p.scrambleBuff...)
		}
		// Database name
		if len(p.database) > 0 {
			data = append(data, []byte(p.database)...)
			// Terminator
			data = append(data, 0x0)
		}
		// For MySQL < 4.1
	} else {
		// Client flags
		data = ui16tob(uint16(p.clientFlags))
		// Max packet size
		data = append(data, ui24tob(p.maxPacketSize)...)
		// User
		if len(p.user) > 0 {
			data = append(data, []byte(p.user)...)
		}
		// Terminator
		data = append(data, 0x0)
		// Scramble buffer
		if len(p.scrambleBuff) > 0 {
			data = append(data, p.scrambleBuff...)
		}
		// Padding
		data = append(data, 0x0)
	}
	// Add the packet header
	data = p.addHeader(data)
	return
}

// Ok packet struct
type packetOK struct {
	packetBase
	affectedRows uint64
	insertId     uint64
	serverStatus uint16
	warningCount uint16
	message      string
}

// OK packet reader
func (p *packetOK) read(data []byte) (err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Position (skip first byte/field count)
	pos := 1
	// Affected rows [length coded binary]
	num, n, err := btolcb(data[pos:])
	if err != nil {
		return
	}
	p.affectedRows = num
	pos += n
	// Insert id [length coded binary]
	num, n, err = btolcb(data[pos:])
	if err != nil {
		return
	}
	p.insertId = num
	pos += n
	// Server status [16 bit uint]
	p.serverStatus = btoui16(data[pos : pos+2])
	pos += 2
	// Warning (4.1 only) [16 bit uint]
	if p.protocol == PROTOCOL_41 {
		p.warningCount = btoui16(data[pos : pos+2])
		pos += 2
	}
	// Message (optional) [string]
	if pos < len(data) {
		p.message = string(data[pos:])
	}
	return
}

// Error packet struct
type packetError struct {
	packetBase
	errno uint16
	state string
	error string
}

// Error packet reader
func (p *packetError) read(data []byte) (err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Position
	pos := 1
	// Error number [16 bit uint]
	p.errno = btoui16(data[pos : pos+2])
	pos += 2
	// State (4.1 only) [string]
	if p.protocol == PROTOCOL_41 {
		pos++
		p.state = string(data[pos : pos+5])
		pos += 5
	}
	// Message [string]
	p.error = string(data[pos:])
	return
}

// EOF packet struct
type packetEOF struct {
	packetBase
	warningCount uint16
	useWarning   bool
	serverStatus uint16
	useStatus    bool
}

// EOF packet reader
func (p *packetEOF) read(data []byte) (err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Check for 4.1 protocol AND 2 available bytes
	if p.protocol == PROTOCOL_41 && len(data) >= 3 {
		// Warning count [16 bit uint]
		p.warningCount = btoui16(data[1:3])
		p.useWarning = true
	}
	// Check for 4.1 protocol AND 2 available bytes
	if p.protocol == PROTOCOL_41 && len(data) == 5 {
		// Server status [16 bit uint]
		p.serverStatus = btoui16(data[3:5])
		p.useStatus = true
	}
	return
}

// Password packet struct
type packetPassword struct {
	packetBase
	scrambleBuff []byte
}

// Password packet writer
func (p *packetPassword) write() (data []byte, err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Set scramble
	data = p.scrambleBuff
	// Add terminator
	data = append(data, 0x0)
	// Add the packet header
	data = p.addHeader(data)
	return
}

// Command packet struct
type packetCommand struct {
	packetBase
	command command
	args    []interface{}
}

// Command packet writer
func (p *packetCommand) write() (data []byte, err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Make slice from command byte
	data = []byte{byte(p.command)}
	// Add args to requests
	switch p.command {
	// Commands with 1 arg unterminated string
	case COM_INIT_DB, COM_QUERY, COM_STMT_PREPARE:
		data = append(data, []byte(p.args[0].(string))...)
	// Commands with 1 arg 32 bit uint
	case COM_PROCESS_KILL, COM_STMT_CLOSE, COM_STMT_RESET:
		data = append(data, ui32tob(p.args[0].(uint32))...)
	// Field list command
	case COM_FIELD_LIST:
		// Table name
		data = append(data, []byte(p.args[0].(string))...)
		// Terminator
		data = append(data, 0x00)
		// Column name
		if len(p.args) > 1 {
			data = append(data, []byte(p.args[1].(string))...)
		}
	// Refresh command
	case COM_REFRESH:
		data = append(data, byte(p.args[0].(Refresh)))
	// Shutdown command
	case COM_SHUTDOWN:
		data = append(data, byte(p.args[0].(Shutdown)))
	// Change user command
	case COM_CHANGE_USER:
		// User
		data = append(data, []byte(p.args[0].(string))...)
		// Terminator
		data = append(data, 0x00)
		// Scramble length for 4.1
		if p.protocol == PROTOCOL_41 {
			data = append(data, byte(len(p.args[1].([]byte))))
		}
		// Scramble buffer
		if len(p.args[1].([]byte)) > 0 {
			data = append(data, p.args[1].([]byte)...)
		}
		// Temrminator for 3.23
		if p.protocol == PROTOCOL_40 {
			data = append(data, 0x00)
		}
		// Database name
		if len(p.args[2].(string)) > 0 {
			data = append(data, []byte(p.args[2].(string))...)
		}
		// Terminator
		data = append(data, 0x00)
		// Character set number (5.1.23+ needs testing with earlier versions)
		data = append(data, ui16tob(p.args[3].(uint16))...)
	// Fetch statement command
	case COM_STMT_FETCH:
		// Statement id
		data = append(data, ui32tob(p.args[0].(uint32))...)
		// Number of rows
		data = append(data, ui32tob(p.args[1].(uint32))...)
	}
	// Add the packet header
	data = p.addHeader(data)
	return
}

// Result set packet struct
type packetResultSet struct {
	packetBase
	fieldCount uint64
	extra      uint64
}

// Result set packet reader
func (p *packetResultSet) read(data []byte) (err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Position and bytes read
	var pos, n int
	// Field count [length coded binary]
	p.fieldCount, n, err = btolcb(data[pos:])
	if err != nil {
		return
	}
	pos += n
	// Extra [length coded binary]
	if pos < len(data) {
		p.extra, n, err = btolcb(data[pos:])
		if err != nil {
			return
		}
	}
	return
}

// Field packet struct
type packetField struct {
	packetBase
	catalog       string
	database      string
	table         string
	orgTable      string
	name          string
	orgName       string
	charsetNumber uint16
	length        uint32
	fieldType     uint8
	flags         uint16
	decimals      uint8
	defaultVal    uint64
}

// Field packet reader
func (p *packetField) read(data []byte) (err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Position and bytes read
	var pos, n int
	// 4.1 protocol
	if p.protocol == PROTOCOL_41 {
		// Catalog [len coded string]
		p.catalog, n, err = p.readLengthCodedString(data)
		if err != nil {
			return
		}
		pos += n
		// Database [len coded string]
		p.database, n, err = p.readLengthCodedString(data[pos:])
		if err != nil {
			return
		}
		pos += n
		// Table [len coded string]
		p.table, n, err = p.readLengthCodedString(data[pos:])
		if err != nil {
			return
		}
		pos += n
		// Original table [len coded string]
		p.orgTable, n, err = p.readLengthCodedString(data[pos:])
		if err != nil {
			return
		}
		pos += n
		// Name [len coded string]
		p.name, n, err = p.readLengthCodedString(data[pos:])
		if err != nil {
			return
		}
		pos += n
		// Original name [len coded string]
		p.orgName, n, err = p.readLengthCodedString(data[pos:])
		if err != nil {
			return
		}
		pos += n
		// Filler
		pos++
		// Charset [16 bit uint]
		p.charsetNumber = btoui16(data[pos : pos+2])
		pos += 2
		// Length [32 bit uint]
		p.length = btoui32(data[pos : pos+4])
		pos += 4
		// Field type [byte]
		p.fieldType = data[pos]
		pos++
		// Flags [16 bit uint]
		p.flags = btoui16(data[pos : pos+2])
		pos += 2
		// Decimals [8 bit uint]
		p.decimals = data[pos]
		pos++
		// Default value [len coded binary]
		if pos < len(data) {
			p.defaultVal, _, err = btolcb(data[pos:])
		}
	} else {
		// Table [len coded string]
		p.table, n, err = p.readLengthCodedString(data[pos:])
		if err != nil {
			return
		}
		pos += n
		// Name [len coded string]
		p.name, n, err = p.readLengthCodedString(data[pos:])
		if err != nil {
			return
		}
		pos += n
		// Length [weird len coded binary]
		p.length = btoui32(data[pos+1 : pos+4])
		pos += 4
		// Type [weird len coded binary]
		p.fieldType = data[pos+1]
		pos += 2
		// Flags [weird len coded binary]
		p.flags = btoui16(data[pos+1 : pos+3])
		pos += 3
		// Decimals [8 bit uint]
		p.decimals = data[pos]
		pos++
		// Default value [unknown len coded binary]
		// @todo
	}
	return
}

// Row data struct
type packetRowData struct {
	packetBase
	row []interface{}
}

// Row data packet reader
func (p *packetRowData) read(data []byte) (err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Position
	pos := 0
	// Loop until end of packet
	for {
		// Read string
		b, n, err := p.readLengthCodedBytes(data[pos:])
		if err != nil {
			return
		}
		// Add to slice
		p.row = append(p.row, b)
		// Increment position and check for end of packet
		pos += n
		if pos == len(data) {
			break
		}
	}
	return
}

// Prepare ok struct
type packetPrepareOK struct {
	packetBase
	statementId  uint32
	columnCount  uint16
	paramCount   uint16
	warningCount uint16
}

// Prepare ok packet reader
func (p *packetPrepareOK) read(data []byte) (err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Position (skip first byte/field count)
	pos := 1
	// Statement id [32 bit uint]
	p.statementId = btoui32(data[pos : pos+4])
	pos += 4
	// Column count [16 bit uint]
	p.columnCount = btoui16(data[pos : pos+2])
	pos += 2
	// Param count [16 bit uint]
	p.paramCount = btoui16(data[pos : pos+2])
	pos += 2
	// Warning count [16 bit uint]
	p.warningCount = btoui16(data[pos : pos+2])
	return
}

// Parameter struct
type packetParameter struct {
	packetBase
	paramType []byte
	flags     uint16
	decimals  uint8
	length    uint32
}

// Parameter packet reader
func (p *packetParameter) read(data []byte) (err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Ignore packet for now
	return
}

// Long data struct
type packetLongData struct {
	packetBase
	command     byte
	statementId uint32
	paramNumber uint16
	data        []byte
}

// Long data packet writer
func (p *packetLongData) write() (data []byte, err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Make slice from command byte
	data = []byte{byte(p.command)}
	// Statement id
	data = append(data, ui32tob(p.statementId)...)
	// Param number
	data = append(data, ui16tob(p.paramNumber)...)
	// Data
	data = append(data, p.data...)
	// Add the packet header
	data = p.addHeader(data)
	return
}

// Execute struct
type packetExecute struct {
	packetBase
	command        byte
	statementId    uint32
	flags          uint8
	iterationCount uint32
	nullBitMap     []byte
	newParamsBound uint8
	paramType      [][]byte
	paramData      [][]byte
}

// Execute packet writer
func (p *packetExecute) write() (data []byte, err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Make slice from command byte
	data = []byte{byte(p.command)}
	// Statement id
	data = append(data, ui32tob(p.statementId)...)
	// Flags
	data = append(data, p.flags)
	// IterationCount
	data = append(data, ui32tob(p.iterationCount)...)
	// Null bit map
	data = append(data, p.nullBitMap...)
	// New params bound
	data = append(data, p.newParamsBound)
	// Param types
	if p.newParamsBound == 1 && len(p.paramType) > 0 {
		for _, v := range p.paramType {
			data = append(data, v...)
		}
	}
	// Param data
	if len(p.paramData) > 0 {
		for _, v := range p.paramData {
			if len(v) > 0 {
				data = append(data, v...)
			}
		}
	}
	// Add the packet header
	data = p.addHeader(data)
	return
}

// Binary row struct
type packetRowBinary struct {
	packetBase
	data []byte
}

// Row binary packet reader
func (p *packetRowBinary) read(data []byte) (err os.Error) {
	// Recover errors
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_MALFORMED_PACKET, CR_MALFORMED_PACKET_STR}
		}
	}()
	// Simply store the row
	p.data = data
	return
}
