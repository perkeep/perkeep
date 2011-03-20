// GoMySQL - A MySQL client library for Go
//
// Copyright 2010-2011 Phil Bayfield. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package mysql

import (
	"os"
	"reflect"
	"strconv"
)

// Prepared statement struct
type Statement struct {
	// Client pointer
	c *Client

	// Statement status flags
	prepared      bool
	preparedSql   string
	paramsBound   bool
	paramsRebound bool

	// Statement id
	statementId uint32

	// Params
	paramCount uint16
	paramType  [][]byte
	paramData  [][]byte

	// Columns (fields)
	columnCount uint64

	// Result
	AffectedRows uint64
	LastInsertId uint64
	Warnings     uint16
	result       *Result
	resultParams []interface{}
}

// Prepare new statement
func (s *Statement) Prepare(sql string) (err os.Error) {
	// Auto reconnect
	defer func() {
		if err != nil && s.c.checkNet(err) && s.c.Reconnect {
			s.c.log(1, "!!! Lost connection to server !!!")
			s.c.connected = false
			err = s.c.reconnect()
			if err == nil {
				err = s.Prepare(sql)
			}
		}
	}()
	// Log prepare
	s.c.log(1, "=== Begin prepare '%s' ===", sql)
	// Pre-run checks
	if !s.c.checkConn() || s.checkResult() {
		return &ClientError{CR_COMMANDS_OUT_OF_SYNC, CR_COMMANDS_OUT_OF_SYNC_STR}
	}
	// Reset client
	s.reset()
	// Send close command
	err = s.c.command(COM_STMT_PREPARE, sql)
	if err != nil {
		return
	}
	// Read result from server
	s.c.sequence++
	_, err = s.getResult(PACKET_PREPARE_OK | PACKET_ERROR)
	if err != nil {
		return
	}
	// Read param packets
	if s.paramCount > 0 {
		for {
			s.c.sequence++
			eof, err := s.getResult(PACKET_PARAM | PACKET_EOF)
			if err != nil {
				return
			}
			if eof {
				break
			}
		}
	}
	// Read field packets
	if s.columnCount > 0 {
		err = s.getFields()
		if err != nil {
			return
		}
	}
	// Statement is preapred
	s.prepared = true
	s.preparedSql = sql
	return
}

// Get number of params
func (s *Statement) ParamCount() uint16 {
	return s.paramCount
}

// Bind params
func (s *Statement) BindParams(params ...interface{}) (err os.Error) {
	// Check prepared
	if !s.prepared {
		return &ClientError{CR_NO_PREPARE_STMT, CR_NO_PREPARE_STMT_STR}
	}
	// Check number of params is correct
	if len(params) != int(s.paramCount) {
		return &ClientError{CR_INVALID_PARAMETER_NO, CR_INVALID_PARAMETER_NO_STR}
	}
	// Reset params
	s.paramType = [][]byte{}
	s.paramData = [][]byte{}
	// Convert params into bytes
	for k, param := range params {
		// Temp vars
		var t FieldType
		var d []byte
		// Switch on type
		switch param.(type) {
		// Nil
		case nil:
			t = FIELD_TYPE_NULL
		// Int
		case int:
			if strconv.IntSize == 32 {
				t = FIELD_TYPE_LONG
			} else {
				t = FIELD_TYPE_LONGLONG
			}
			d = itob(param.(int))
		// Uint
		case uint:
			if strconv.IntSize == 32 {
				t = FIELD_TYPE_LONG
			} else {
				t = FIELD_TYPE_LONGLONG
			}
			d = uitob(param.(uint))
		// Int8
		case int8:
			t = FIELD_TYPE_TINY
			d = []byte{byte(param.(int8))}
		// Uint8
		case uint8:
			t = FIELD_TYPE_TINY
			d = []byte{param.(uint8)}
		// Int16
		case int16:
			t = FIELD_TYPE_SHORT
			d = i16tob(param.(int16))
		// Uint16
		case uint16:
			t = FIELD_TYPE_SHORT
			d = ui16tob(param.(uint16))
		// Int32
		case int32:
			t = FIELD_TYPE_LONG
			d = i32tob(param.(int32))
		// Uint32
		case uint32:
			t = FIELD_TYPE_LONG
			d = ui32tob(param.(uint32))
		// Int64
		case int64:
			t = FIELD_TYPE_LONGLONG
			d = i64tob(param.(int64))
		// Uint64
		case uint64:
			t = FIELD_TYPE_LONGLONG
			d = ui64tob(param.(uint64))
		// Float32
		case float32:
			t = FIELD_TYPE_FLOAT
			d = f32tob(param.(float32))
		// Float64
		case float64:
			t = FIELD_TYPE_DOUBLE
			d = f64tob(param.(float64))
		// String
		case string:
			t = FIELD_TYPE_STRING
			d = lcbtob(uint64(len(param.(string))))
			d = append(d, []byte(param.(string))...)
		// Byte array
		case []byte:
			t = FIELD_TYPE_BLOB
			d = lcbtob(uint64(len(param.([]byte))))
			d = append(d, param.([]byte)...)
		// Other types
		default:
			return &ClientError{CR_UNSUPPORTED_PARAM_TYPE, s.c.fmtError(CR_UNSUPPORTED_PARAM_TYPE_STR, reflect.NewValue(param).Type(), k)}
		}
		// Append values
		s.paramType = append(s.paramType, []byte{byte(t), 0x0})
		s.paramData = append(s.paramData, d)
	}
	// Flag params as bound
	s.paramsBound = true
	s.paramsRebound = true
	return
}

// Send long data
func (s *Statement) SendLongData(num int, data []byte) (err os.Error) {
	// Auto reconnect
	defer func() {
		err = s.c.simpleReconnect(err)
	}()
	// Log send long data
	s.c.log(1, "=== Begin send long data ===")
	// Check prepared
	if !s.prepared {
		return &ClientError{CR_NO_PREPARE_STMT, CR_NO_PREPARE_STMT_STR}
	}
	// Pre-run checks
	if !s.c.checkConn() || s.checkResult() {
		return &ClientError{CR_COMMANDS_OUT_OF_SYNC, CR_COMMANDS_OUT_OF_SYNC_STR}
	}
	// Reset client
	s.reset()
	// Data position (if data is longer than max packet length
	pos := 0
	// Send data
	for {
		// Construct packet
		p := &packetLongData{
			command:     uint8(COM_STMT_SEND_LONG_DATA),
			statementId: s.statementId,
			paramNumber: uint16(num),
		}
		// Add protocol and sequence
		p.protocol = s.c.protocol
		p.sequence = s.c.sequence
		// Add data
		if len(data[pos:]) > MAX_PACKET_SIZE-12 {
			p.data = data[pos : MAX_PACKET_SIZE-12]
			pos += MAX_PACKET_SIZE - 12
		} else {
			p.data = data[pos:]
			pos += len(data[pos:])
		}
		// Write packet
		err = s.c.w.writePacket(p)
		if err != nil {
			return
		}
		// Log write success
		s.c.log(1, "[%d] Sent long data packet", p.sequence)
		// Check if all data sent
		if pos == len(data) {
			break
		}
		// Increment sequence
		s.c.sequence++
	}
	return
}

// Execute
func (s *Statement) Execute() (err os.Error) {
	// Auto reconnect
	defer func() {
		if err != nil && s.c.checkNet(err) && s.c.Reconnect {
			s.c.log(1, "!!! Lost connection to server !!!")
			s.c.connected = false
			err = s.c.reconnect()
			if err == nil {
				err = s.Prepare(s.preparedSql)
				if err == nil {
					err = s.Execute()
				}
			}
		}
	}()
	// Log execute
	s.c.log(1, "=== Begin execute ===")
	// Check prepared
	if !s.prepared {
		return &ClientError{CR_NO_PREPARE_STMT, CR_NO_PREPARE_STMT_STR}
	}
	// Check params bound
	if s.paramCount > 0 && !s.paramsBound {
		return &ClientError{CR_PARAMS_NOT_BOUND, CR_PARAMS_NOT_BOUND_STR}
	}
	// Pre-run checks
	if !s.c.checkConn() || s.checkResult() {
		return &ClientError{CR_COMMANDS_OUT_OF_SYNC, CR_COMMANDS_OUT_OF_SYNC_STR}
	}
	// Reset client
	s.reset()
	// Construct packet
	p := &packetExecute{
		command:        byte(COM_STMT_EXECUTE),
		statementId:    s.statementId,
		flags:          byte(CURSOR_TYPE_NO_CURSOR),
		iterationCount: 1,
		nullBitMap:     s.getNullBitMap(),
		paramType:      s.paramType,
		paramData:      s.paramData,
	}
	// Add protocol and sequence
	p.protocol = s.c.protocol
	p.sequence = s.c.sequence
	// Add rebound flag
	if s.paramsRebound {
		p.newParamsBound = byte(1)
	}
	// Write packet
	err = s.c.w.writePacket(p)
	if err != nil {
		return
	}
	// Log write success
	s.c.log(1, "[%d] Sent execute packet", p.sequence)
	// Read result from server
	s.c.sequence++
	_, err = s.getResult(PACKET_OK | PACKET_ERROR | PACKET_RESULT)
	if err != nil || s.result == nil {
		return
	}
	// Store fields
	err = s.getFields()
	// Unflag params rebound
	s.paramsRebound = false
	return
}

// Get field count
func (s *Statement) FieldCount() uint64 {
	if s.checkResult() {
		return s.result.fieldCount
	}
	return 0
}

// Fetch the next field
func (s *Statement) FetchColumn() *Field {
	if s.checkResult() {
		// Check if all fields have been fetched
		if s.result.fieldPos < uint64(len(s.result.fields)) {
			// Increment and return current field
			s.result.fieldPos++
			return s.result.fields[s.result.fieldPos-1]
		}
	}
	return nil
}

// Fetch all fields
func (s *Statement) FetchColumns() []*Field {
	if s.checkResult() {
		return s.result.fields
	}
	return nil
}

// Bind result
func (s *Statement) BindResult(params ...interface{}) (err os.Error) {
	s.resultParams = params
	return
}

// Get row count
func (s *Statement) RowCount() uint64 {
	// Stored mode
	if s.checkResult() && s.result.mode == RESULT_STORED {
		return uint64(len(s.result.rows))
	}
	return 0
}

// Fetch next row 
func (s *Statement) Fetch() (eof bool, err os.Error) {
	// Auto reconnect
	defer func() {
		err = s.c.simpleReconnect(err)
	}()
	// Log fetch
	s.c.log(1, "=== Begin fetch ===")
	// Check prepared
	if !s.prepared {
		return false, &ClientError{CR_NO_PREPARE_STMT, CR_NO_PREPARE_STMT_STR}
	}
	// Check result
	if !s.checkResult() {
		return false, &ClientError{CR_NO_RESULT_SET, CR_NO_RESULT_SET_STR}
	}
	var row Row
	// Check result mode
	switch s.result.mode {
	// Used or unused result (needs fetching)
	case RESULT_UNUSED, RESULT_USED:
		s.result.mode = RESULT_USED
		if s.result.allRead == true {
			return true, nil
		}
		eof, err := s.getRow()
		if err != nil {
			return false, err
		}
		if eof {
			s.result.allRead = true
			return true, nil
		}
		row = s.result.rows[0]
	// Stored result
	case RESULT_STORED:
		if s.result.rowPos >= uint64(len(s.result.rows)) {
			return true, nil
		}
		row = s.result.rows[s.result.rowPos]
		s.result.rowPos++
	}
	// Recover possible errors from type conversion
	defer func() {
		if e := recover(); e != nil {
			err = &ClientError{CR_UNKNOWN_ERROR, CR_UNKNOWN_ERROR_STR}
			return
		}
	}()
	// Iterate bound params and assign from row (partial set quicker this way)
	for k, v := range s.resultParams {
		switch t := v.(type) {
		// Integer types
		case *int:
			*t = int(atoui64(row[k]))
		case *uint:
			*t = uint(atoui64(row[k]))
		case *int8:
			*t = int8(atoui64(row[k]))
		case *uint8:
			*t = uint8(atoui64(row[k]))
		case *int16:
			*t = int16(atoui64(row[k]))
		case *uint16:
			*t = uint16(atoui64(row[k]))
		case *int32:
			*t = int32(atoui64(row[k]))
		case *uint32:
			*t = uint32(atoui64(row[k]))
		case *int64:
			*t = int64(atoui64(row[k]))
		case *uint64:
			*t = atoui64(row[k])
		// Floating point types
		case *float32:
			*t = float32(atof64(row[k]))
		case *float64:
			*t = atof64(row[k])
		// Byte slice, assertion
		case *[]byte:
			*t = row[k].([]byte)
		// Strings
		case *string:
			*t = atos(row[k])
		// Date/time, assertion
		case *Date:
			*t = row[k].(Date)
		case *Time:
			*t = row[k].(Time)
		case *DateTime:
			*t = row[k].(DateTime)
		}
	}
	return
}

// Store result
func (s *Statement) StoreResult() (err os.Error) {
	// Auto reconnect
	defer func() {
		err = s.c.simpleReconnect(err)
	}()
	// Log store result
	s.c.log(1, "=== Begin store result ===")
	// Check prepared
	if !s.prepared {
		return &ClientError{CR_NO_PREPARE_STMT, CR_NO_PREPARE_STMT_STR}
	}
	// Check if result already used/stored
	if s.result.mode != RESULT_UNUSED {
		return &ClientError{CR_COMMANDS_OUT_OF_SYNC, CR_COMMANDS_OUT_OF_SYNC_STR}
	}
	// Set storage mode
	s.result.mode = RESULT_STORED
	// Store all rows
	err = s.getAllRows()
	if err != nil {
		return
	}
	s.result.allRead = true
	return
}

// Free result
func (s *Statement) FreeResult() (err os.Error) {
	// Auto reconnect
	defer func() {
		err = s.c.simpleReconnect(err)
	}()
	// Log free result
	s.c.log(1, "=== Begin free result ===")
	// Check prepared
	if !s.prepared {
		return &ClientError{CR_NO_PREPARE_STMT, CR_NO_PREPARE_STMT_STR}
	}
	// Check result
	if !s.checkResult() {
		return &ClientError{CR_NO_RESULT_SET, CR_NO_RESULT_SET_STR}
	}
	// Free the current result set
	s.freeAll(false)
	return
}

// More results
func (s *Statement) MoreResults() bool {
	return s.c.MoreResults()
}

// Next result
func (s *Statement) NextResult() (more bool, err os.Error) {
	// Auto reconnect
	defer func() {
		err = s.c.simpleReconnect(err)
	}()
	// Log next result
	s.c.log(1, "=== Begin next result ===")
	// Check prepared
	if !s.prepared {
		return false, &ClientError{CR_NO_PREPARE_STMT, CR_NO_PREPARE_STMT_STR}
	}
	// Pre-run checks
	if !s.c.checkConn() || s.checkResult() {
		return false, &ClientError{CR_COMMANDS_OUT_OF_SYNC, CR_COMMANDS_OUT_OF_SYNC_STR}
	}
	// Check for more results
	more = s.MoreResults()
	if !more {
		return
	}
	// Read result from server
	s.c.sequence++
	_, err = s.getResult(PACKET_OK | PACKET_ERROR | PACKET_RESULT)
	if err != nil || s.result == nil {
		return
	}
	// Store fields
	err = s.getFields()
	return
}

// Reset statement
func (s *Statement) Reset() (err os.Error) {
	// Auto reconnect
	defer func() {
		err = s.c.simpleReconnect(err)
	}()
	// Log next result
	s.c.log(1, "=== Begin reset statement ===")
	// Check prepared
	if !s.prepared {
		return &ClientError{CR_NO_PREPARE_STMT, CR_NO_PREPARE_STMT_STR}
	}
	// Pre-run checks
	if !s.c.checkConn() {
		return &ClientError{CR_COMMANDS_OUT_OF_SYNC, CR_COMMANDS_OUT_OF_SYNC_STR}
	}
	// Free any results
	if s.checkResult() {
		err = s.freeAll(true)
	}
	// Reset client
	s.reset()
	// Send command
	err = s.c.command(COM_STMT_RESET, s.statementId)
	if err != nil {
		return
	}
	// Read result from server
	s.c.sequence++
	_, err = s.getResult(PACKET_OK | PACKET_ERROR)
	return
}

// Close statement
func (s *Statement) Close() (err os.Error) {
	// Auto reconnect
	defer func() {
		err = s.c.simpleReconnect(err)
	}()
	// Log next result
	s.c.log(1, "=== Begin close statement ===")
	// Check prepared
	if !s.prepared {
		return &ClientError{CR_NO_PREPARE_STMT, CR_NO_PREPARE_STMT_STR}
	}
	// Pre-run checks
	if !s.c.checkConn() || s.checkResult() {
		return &ClientError{CR_COMMANDS_OUT_OF_SYNC, CR_COMMANDS_OUT_OF_SYNC_STR}
	}
	// Reset client
	s.reset()
	// Send command
	err = s.c.command(COM_STMT_RESET, s.statementId)
	return
}

// Reset the statement
func (s *Statement) reset() {
	s.AffectedRows = 0
	s.LastInsertId = 0
	s.Warnings = 0
	s.result = nil
	s.c.reset()
}

// Check if a result exists
func (s *Statement) checkResult() bool {
	if s.result != nil {
		return true
	}
	return false
}

// Get null bit map
func (s *Statement) getNullBitMap() (nbm []byte) {
	nbm = make([]byte, (s.paramCount+7)/8)
	bm := uint64(0)
	// Check if params are null (nil)
	for i := uint16(0); i < s.paramCount; i++ {
		if s.paramType[i][0] == byte(FIELD_TYPE_NULL) {
			bm += 1 << uint(i)
		}
	}
	// Convert the uint64 value into bytes
	for i := 0; i < len(nbm); i++ {
		nbm[i] = byte(bm >> uint(i*8))
	}
	return
}

// Get all result fields
func (s *Statement) getFields() (err os.Error) {
	// Loop till EOF
	for {
		s.c.sequence++
		eof, err := s.getResult(PACKET_FIELD | PACKET_EOF)
		if err != nil {
			return
		}
		if eof {
			break
		}
	}
	return
}

// Get next row for a result
func (s *Statement) getRow() (eof bool, err os.Error) {
	// Check for a valid result
	if s.result == nil {
		return false, &ClientError{CR_NO_RESULT_SET, CR_NO_RESULT_SET_STR}
	}
	// Read next row packet or EOF
	s.c.sequence++
	eof, err = s.getResult(PACKET_ROW_BINARY | PACKET_EOF)
	return
}

// Get all rows for the result
func (s *Statement) getAllRows() (err os.Error) {
	for {
		eof, err := s.getRow()
		if err != nil {
			return
		}
		if eof {
			break
		}
	}
	return
}

// Get result
func (s *Statement) getResult(types packetType) (eof bool, err os.Error) {
	// Log read result
	s.c.log(1, "Reading result packet from server")
	// Get result packet
	p, err := s.c.r.readPacket(types)
	if err != nil {
		return
	}
	// Process result packet
	switch p.(type) {
	default:
		err = &ClientError{CR_UNKNOWN_ERROR, CR_UNKNOWN_ERROR_STR}
	case *packetOK:
		err = handleOK(p.(*packetOK), s.c, &s.AffectedRows, &s.LastInsertId, &s.Warnings)
	case *packetError:
		err = handleError(p.(*packetError), s.c)
	case *packetEOF:
		eof = true
		err = handleEOF(p.(*packetEOF), s.c)
	case *packetPrepareOK:
		err = handlePrepareOK(p.(*packetPrepareOK), s.c, s)
	case *packetParameter:
		err = handleParam(p.(*packetParameter), s.c)
	case *packetField:
		err = handleField(p.(*packetField), s.c, s.result)
	case *packetResultSet:
		s.result = &Result{c: s.c}
		err = handleResultSet(p.(*packetResultSet), s.c, s.result)
	case *packetRowBinary:
		err = handleBinaryRow(p.(*packetRowBinary), s.c, s.result)
	}
	return
}

// Free any result sets waiting to be read
func (s *Statement) freeAll(next bool) (err os.Error) {
	// Check for unread rows
	if !s.result.allRead {
		// Read all rows
		err = s.getAllRows()
		if err != nil {
			return
		}
	}
	// Unset the result
	s.result = nil
	// Check for next result
	if next {
		for {
			// Check if more results exist
			if !s.c.MoreResults() {
				break
			}
			// Get next result
			s.c.sequence++
			_, err = s.getResult(PACKET_OK | PACKET_ERROR | PACKET_RESULT)
			if err != nil {
				return
			}
			if s.result == nil {
				continue
			}
			// Set result mode to RESULT_FREE
			s.result.mode = RESULT_FREE
			// Read fields
			err = s.getFields()
			if err != nil {
				return
			}
			// Read rows
			err = s.getAllRows()
			if err != nil {
				return
			}
		}
	}
	return
}
