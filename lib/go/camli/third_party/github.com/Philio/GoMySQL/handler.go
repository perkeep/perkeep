// GoMySQL - A MySQL client library for Go
//
// Copyright 2010-2011 Phil Bayfield. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package mysql

import (
	"os"
	"strconv"
)

// OK packet handler
func handleOK(p *packetOK, c *Client, a, i *uint64, w *uint16) (err os.Error) {
	// Log OK result
	c.log(1, "[%d] Received OK packet", p.sequence)
	// Check sequence
	err = c.checkSequence(p.sequence)
	if err != nil {
		return
	}
	// Store packet data
	*a = p.affectedRows
	*i = p.insertId
	*w = p.warningCount
	c.serverStatus = ServerStatus(p.serverStatus)
	// Full logging [level 3]
	if c.LogLevel > 2 {
		c.logStatus()
	}
	return
}

// Error packet handler
func handleError(p *packetError, c *Client) (err os.Error) {
	// Log error result
	c.log(1, "[%d] Received error packet", p.sequence)
	// Check sequence
	err = c.checkSequence(p.sequence)
	if err != nil {
		return
	}
	// Check and unset more results flag
	// @todo maybe serverStatus should just be zeroed?
	if c.MoreResults() {
		c.serverStatus ^= SERVER_MORE_RESULTS_EXISTS
	}
	// Return error
	return &ServerError{Errno(p.errno), Error(p.error)}
}

// EOF packet handler
func handleEOF(p *packetEOF, c *Client) (err os.Error) {
	// Log EOF result
	c.log(1, "[%d] Received EOF packet", p.sequence)
	// Check sequence
	err = c.checkSequence(p.sequence)
	if err != nil {
		return
	}
	// Store packet data
	if p.useStatus {
		c.serverStatus = ServerStatus(p.serverStatus)
		// Full logging [level 3]
		if c.LogLevel > 2 {
			c.logStatus()
		}
	}
	return
}

// Result set packet handler
func handleResultSet(p *packetResultSet, c *Client, r *Result) (err os.Error) {
	// Log error result
	c.log(1, "[%d] Received result set packet", p.sequence)
	// Check sequence
	err = c.checkSequence(p.sequence)
	if err != nil {
		return
	}
	// Assign field count
	r.fieldCount = p.fieldCount
	return
}

// Field packet handler
func handleField(p *packetField, c *Client, r *Result) (err os.Error) {
	// Log field result
	c.log(1, "[%d] Received field packet", p.sequence)
	// Check sequence
	err = c.checkSequence(p.sequence)
	if err != nil {
		return
	}
	// Check if packet needs to be stored
	if r == nil || r.mode == RESULT_FREE {
		return
	}
	// Apppend new field
	r.fields = append(r.fields, &Field{
		Database: p.database,
		Table:    p.table,
		Name:     p.name,
		Length:   p.length,
		Type:     FieldType(p.fieldType),
		Flags:    FieldFlag(p.flags),
		Decimals: p.decimals,
	})
	return
}

// Row packet hander
func handleRow(p *packetRowData, c *Client, r *Result) (err os.Error) {
	// Log field result
	c.log(1, "[%d] Received row packet", p.sequence)
	// Check sequence
	err = c.checkSequence(p.sequence)
	if err != nil {
		return
	}
	// Check if there is a result set
	if r == nil || r.mode == RESULT_FREE {
		return
	}
	// Basic type conversion
	var row []interface{}
	var field interface{}
	// Iterate fields to get types
	for i, f := range r.fields {
		// Check null
		if len(p.row[i].([]byte)) ==0 {
			field = nil
		} else {
			switch f.Type {
			// Signed/unsigned ints
			case FIELD_TYPE_TINY, FIELD_TYPE_SHORT, FIELD_TYPE_YEAR, FIELD_TYPE_INT24, FIELD_TYPE_LONG, FIELD_TYPE_LONGLONG:
				if f.Flags&FLAG_UNSIGNED > 0 {
					field, err = strconv.Atoui64(string(p.row[i].([]byte)))
				} else {
					field, err = strconv.Atoi64(string(p.row[i].([]byte)))
				}
				if err != nil {
					return
				}
			// Floats and doubles
			case FIELD_TYPE_FLOAT, FIELD_TYPE_DOUBLE:
				field, err = strconv.Atof64(string(p.row[i].([]byte)))
				if err != nil {
					return
				}
			// Strings
			case FIELD_TYPE_DECIMAL, FIELD_TYPE_NEWDECIMAL, FIELD_TYPE_VARCHAR, FIELD_TYPE_VAR_STRING, FIELD_TYPE_STRING:
				field = string(p.row[i].([]byte))
			// Anything else
			default:
				field = p.row[i]
			}
		}
		// Add to row
		row = append(row, field)
	}
	// Stored result
	if r.mode == RESULT_STORED {
		// Cast and append the row
		r.rows = append(r.rows, Row(row))
	}
	// Used result
	if r.mode == RESULT_USED {
		// Only save 1 row, overwrite previous
		r.rows = []Row{Row(row)}
	}
	return
}

// Prepare OK packet handler
func handlePrepareOK(p *packetPrepareOK, c *Client, s *Statement) (err os.Error) {
	// Log result
	c.log(1, "[%d] Received prepare OK packet", p.sequence)
	// Check sequence
	err = c.checkSequence(p.sequence)
	if err != nil {
		return
	}
	// Store packet data
	s.statementId = p.statementId
	s.paramCount = p.paramCount
	s.columnCount = uint64(p.columnCount)
	s.Warnings = p.warningCount
	return
}

// Parameter packet handler
func handleParam(p *packetParameter, c *Client) (err os.Error) {
	// Log result
	c.log(1, "[%d] Received parameter packet", p.sequence)
	// Check sequence
	err = c.checkSequence(p.sequence)
	if err != nil {
		return
	}
	// @todo at some point implement this properly if any versions of MySQL are doing so
	return
}

// Binary row packet handler
func handleBinaryRow(p *packetRowBinary, c *Client, r *Result) (err os.Error) {
	// Log binary row result
	c.log(1, "[%d] Received binary row packet", p.sequence)
	// Check sequence
	err = c.checkSequence(p.sequence)
	if err != nil {
		return
	}
	// Check if there is a result set
	if r == nil || r.mode == RESULT_FREE {
		return
	}
	// Read data into fields
	var row []interface{}
	var field interface{}
	// Get null bit map
	nc := (r.fieldCount + 9) / 8
	nbm := p.data[1 : nc+1]
	pos := nc + 1
	for i, f := range r.fields {
		// Check if field is null
		posByte := (i + 2) / 8
		posBit := i - (posByte * 8) + 2
		if nbm[posByte]&(1<<uint8(posBit)) != 0 {
			field = nil
			continue
		}
		// Otherwise use field type
		switch f.Type {
		// Tiny int (8 bit int unsigned or signed)
		case FIELD_TYPE_TINY:
			if f.Flags&FLAG_UNSIGNED > 0 {
				field = uint64(p.data[pos])
			} else {
				field = int64(p.data[pos])
			}
			pos++
		// Small int (16 bit int unsigned or signed)
		case FIELD_TYPE_SHORT, FIELD_TYPE_YEAR:
			if f.Flags&FLAG_UNSIGNED > 0 {
				field = uint64(btoui16(p.data[pos : pos+2]))
			} else {
				field = int64(btoi16(p.data[pos : pos+2]))
			}
			pos += 2
		// Int (32 bit int unsigned or signed) and medium int which is actually in int32 format
		case FIELD_TYPE_LONG, FIELD_TYPE_INT24:
			if f.Flags&FLAG_UNSIGNED > 0 {
				field = uint64(btoui32(p.data[pos : pos+4]))
			} else {
				field = int64(btoi32(p.data[pos : pos+4]))
			}
			pos += 4
		// Big int (64 bit int unsigned or signed)
		case FIELD_TYPE_LONGLONG:
			if f.Flags&FLAG_UNSIGNED > 0 {
				field = btoui64(p.data[pos : pos+8])
			} else {
				field = btoi64(p.data[pos : pos+8])
			}
			pos += 8
		// Floats (Single precision floating point, 32 bit signed)
		case FIELD_TYPE_FLOAT:
			field = btof32(p.data[pos : pos+4])
			pos += 4
		// Double (Double precision floating point, 64 bit signed)
		case FIELD_TYPE_DOUBLE:
			field = btof64(p.data[pos : pos+8])
			pos += 8
		// Bit, decimal, strings, blobs etc, all length coded binary strings
		case FIELD_TYPE_BIT, FIELD_TYPE_DECIMAL, FIELD_TYPE_NEWDECIMAL, FIELD_TYPE_VARCHAR,
			FIELD_TYPE_TINY_BLOB, FIELD_TYPE_MEDIUM_BLOB, FIELD_TYPE_LONG_BLOB, FIELD_TYPE_BLOB,
			FIELD_TYPE_VAR_STRING, FIELD_TYPE_STRING, FIELD_TYPE_GEOMETRY:
			num, n, err := btolcb(p.data[pos:])
			if err != nil {
				return
			}
			field = p.data[pos+uint64(n) : pos+uint64(n)+num]
			pos += uint64(n) + num
		// Date (From libmysql/libmysql.c read_binary_datetime)
		case FIELD_TYPE_DATE:
			num, n, err := btolcb(p.data[pos:])
			if err != nil {
				return
			}
			// New date
			d := Date{}
			// Check zero
			if num == 0 {
				field = d
				pos++
				break
			}
			// Year 2 bytes
			d.Year = btoui16(p.data[pos+uint64(n) : pos+uint64(n)+2])
			// Month 1 byte
			d.Month = p.data[pos+uint64(n)+2]
			// Day 1 byte
			d.Day = p.data[pos+uint64(n)+3]
			field = d
			pos += uint64(n) + num
		// Time  (From libmysql/libmysql.c read_binary_time)
		case FIELD_TYPE_TIME:
			num, n, err := btolcb(p.data[pos:])
			if err != nil {
				return
			}
			// New time
			t := Time{}
			// Default zero values
			if num == 0 {
				field = t
				pos++
				break
			}
			// Hour 1 byte
			t.Hour = p.data[pos+6]
			// Minute 1 byte
			t.Minute = p.data[pos+7]
			// Second 1 byte
			t.Second = p.data[pos+8]
			field = t
			pos += uint64(n) + num
		// Datetime/Timestamp (From libmysql/libmysql.c read_binary_datetime)
		case FIELD_TYPE_TIMESTAMP, FIELD_TYPE_DATETIME:
			num, n, err := btolcb(p.data[pos:])
			if err != nil {
				return
			}
			// New datetime
			d := DateTime{}
			// Check zero
			if num == 0 {
				field = d
				pos++
				break
			}
			// Year 2 bytes
			d.Year = btoui16(p.data[pos+uint64(n) : pos+uint64(n)+2])
			// Month 1 byte
			d.Month = p.data[pos+uint64(n)+2]
			// Day 1 byte
			d.Day = p.data[pos+uint64(n)+3]
			// Hour 1 byte
			d.Hour = p.data[pos+uint64(n)+4]
			// Minute 1 byte
			d.Minute = p.data[pos+uint64(n)+5]
			// Second 1 byte
			d.Second = p.data[pos+uint64(n)+6]
			field = d
			pos += uint64(n) + num
		}
		// Add to row
		row = append(row, field)
	}
	// Stored result
	if r.mode == RESULT_STORED {
		// Cast and append the row
		r.rows = append(r.rows, Row(row))
	}
	// Used result
	if r.mode == RESULT_USED {
		// Only save 1 row, overwrite previous
		r.rows = []Row{Row(row)}
	}
	return
}
