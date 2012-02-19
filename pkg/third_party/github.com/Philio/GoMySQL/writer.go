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

// Packet writer struct
type writer struct {
	conn io.ReadWriteCloser
}

// Create a new reader
func newWriter(conn io.ReadWriteCloser) *writer {
	return &writer{
		conn: conn,
	}
}

// Write packet to the server
func (w *writer) writePacket(p packetWritable) (err error) {
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
	// Get data in binary format
	pktData, err := p.write()
	if err != nil {
		return
	}
	// Write packet
	nw, err := w.conn.Write(pktData)
	if err != nil {
		return
	}
	if nw != len(pktData) {
		err = &ClientError{CR_DATA_TRUNCATED, CR_DATA_TRUNCATED_STR}
	}
	return
}
