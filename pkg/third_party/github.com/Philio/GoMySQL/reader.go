// GoMySQL - A MySQL client library for Go
//
// Copyright 2010-2011 Phil Bayfield. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package mysql

import (
	"io"
	"net"
)

// Packet reader struct
type reader struct {
	conn     io.ReadWriteCloser
	protocol uint8
}

// Create a new reader
func newReader(conn io.ReadWriteCloser) *reader {
	return &reader{
		conn:     conn,
		protocol: DEFAULT_PROTOCOL,
	}
}

// Read the next packet
func (r *reader) readPacket(types packetType) (p packetReadable, err error) {
	// Deferred error processing
	defer func() {
		if err != nil {
			// EOF errors
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				err = &ClientError{CR_SERVER_LOST, CR_SERVER_LOST_STR}
			}
			// OpError
			if _, ok := err.(*net.OpError); ok {
				err = &ClientError{CR_SERVER_LOST, CR_SERVER_LOST_STR}
			}
			// Not ClientError, unknown error
			if _, ok := err.(*ClientError); !ok {
				err = &ClientError{CR_UNKNOWN_ERROR, CR_UNKNOWN_ERROR_STR}
			}
		}
	}()
	// Read packet length
	pktLen, err := r.readNumber(3)
	if err != nil {
		return
	}
	// Read sequence
	pktSeq, err := r.readNumber(1)
	if err != nil {
		return
	}
	// Read rest of packet
	pktData := make([]byte, pktLen)
	nr, err := io.ReadFull(r.conn, pktData)
	if err != nil {
		return
	}
	if nr != int(pktLen) {
		err = &ClientError{CR_DATA_TRUNCATED, CR_DATA_TRUNCATED_STR}
	}
	// Work out packet type
	switch {
	// Unknown packet
	default:
		err = &ClientError{CR_UNKNOWN_ERROR, CR_UNKNOWN_ERROR_STR}
	// Initialisation / handshake packet, server > client
	case types&PACKET_INIT != 0:
		pk := new(packetInit)
		pk.sequence = uint8(pktSeq)
		return pk, pk.read(pktData)
	// Ok packet
	case types&PACKET_OK != 0 && pktData[0] == 0x0:
		pk := new(packetOK)
		pk.protocol = r.protocol
		pk.sequence = uint8(pktSeq)
		return pk, pk.read(pktData)
	// Error packet
	case types&PACKET_ERROR != 0 && pktData[0] == 0xff:
		pk := new(packetError)
		pk.protocol = r.protocol
		pk.sequence = uint8(pktSeq)
		return pk, pk.read(pktData)
	// EOF packet
	case types&PACKET_EOF != 0 && pktData[0] == 0xfe:
		pk := new(packetEOF)
		pk.protocol = r.protocol
		pk.sequence = uint8(pktSeq)
		return pk, pk.read(pktData)
	// Result set packet
	case types&PACKET_RESULT != 0 && pktData[0] > 0x0 && pktData[0] < 0xfe:
		pk := new(packetResultSet)
		pk.sequence = uint8(pktSeq)
		return pk, pk.read(pktData)
	// Field packet
	case types&PACKET_FIELD != 0 && pktData[0] < 0xfe:
		pk := new(packetField)
		pk.protocol = r.protocol
		pk.sequence = uint8(pktSeq)
		return pk, pk.read(pktData)
	// Row data packet
	case types&PACKET_ROW != 0 && pktData[0] < 0xfe:
		pk := new(packetRowData)
		pk.sequence = uint8(pktSeq)
		return pk, pk.read(pktData)
	// Prepare ok packet
	case types&PACKET_PREPARE_OK != 0 && pktData[0] == 0x0:
		pk := new(packetPrepareOK)
		pk.sequence = uint8(pktSeq)
		return pk, pk.read(pktData)
	// Param packet
	case types&PACKET_PARAM != 0 && pktData[0] < 0xfe:
		pk := new(packetParameter)
		pk.sequence = uint8(pktSeq)
		return pk, pk.read(pktData)
	// Binary row packet
	case types&PACKET_ROW_BINARY != 0 && pktData[0] < 0xfe:
		pk := new(packetRowBinary)
		pk.sequence = uint8(pktSeq)
		return pk, pk.read(pktData)
	}
	return
}

// Read n bytes long number
func (r *reader) readNumber(n uint8) (num uint64, err error) {
	// Read bytes into array
	buf := make([]byte, n)
	nr, err := io.ReadFull(r.conn, buf)
	if err != nil || nr != int(n) {
		return
	}
	// Convert to uint64
	num = 0
	for i := uint8(0); i < n; i++ {
		num |= uint64(buf[i]) << (i * 8)
	}
	return
}
