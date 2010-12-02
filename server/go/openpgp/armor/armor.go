// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This package implements OpenPGP ASCII Armor, see RFC 4880. OpenPGP Armor is
// very similar to PEM except that it has an additional CRC checksum.
package armor

import (
	"bytes"
	"encoding/base64"
)

// A Block represents an OpenPGP armored structure.
//
// The encoded form is:
//    -----BEGIN Type-----
//    Headers
//    base64-encoded Bytes
//    '=' base64 encoded checksum
//    -----END Type-----
// where Headers is a possibly empty sequence of Key: Value lines.
type Block struct {
	Type    string            // The type, taken from the preamble (i.e. "PGP SIGNATURE").
	Headers map[string]string // Optional headers.
	Bytes   []byte            // The decoded bytes of the contents.
}

// getLine results the first \r\n or \n delineated line from the given byte
// array. The line does not include the \r\n or \n. The remainder of the byte
// array (also not including the new line bytes) is also returned and this will
// always be smaller than the original argument.
func getLine(data []byte) (line, rest []byte) {
	i := bytes.Index(data, []byte{'\n'})
	var j int
	if i < 0 {
		i = len(data)
		j = i
	} else {
		j = i + 1
		if i > 0 && data[i-1] == '\r' {
			i--
		}
	}
	return data[0:i], data[j:]
}

// removeWhitespace returns a copy of its input with all spaces, tab and
// newline characters removed.
func removeWhitespace(data []byte) []byte {
	result := make([]byte, len(data))
	n := 0

	for _, b := range data {
		if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
			continue
		}
		result[n] = b
		n++
	}

	return result[0:n]
}

const crc24Init = 0xb704ce
const crc24Poly = 0x1864cfb

// crc24 calculates the OpenPGP checksum as specified in RFC 4880, section 6.1
func crc24(d []byte) uint32 {
	crc := uint64(crc24Init)
	for _, b := range d {
		crc ^= uint64(b) << 16
		for i := 0; i < 8; i++ {
			crc <<= 1
			if crc&0x1000000 != 0 {
				crc ^= crc24Poly
			}
		}
	}
	return uint32(crc & 0xffffff)
}

var armorStart = []byte("\n-----BEGIN ")
var armorEnd = []byte("\n-----END ")
var armorEndOfLine = []byte("-----")

// Decode will find the next armored block in the input. It returns that block
// and the remainder of the input. If no block is found, p is nil and the whole
// of the input is returned in rest.
func Decode(data []byte) (p *Block, rest []byte) {
	// armorStart begins with a newline. However, at the very beginning of
	// the byte array, we'll accept the start string without it.
	rest = data
	if bytes.HasPrefix(data, armorStart[1:]) {
		rest = rest[len(armorStart)-1 : len(data)]
	} else if i := bytes.Index(data, armorStart); i >= 0 {
		rest = rest[i+len(armorStart) : len(data)]
	} else {
		return nil, data
	}

	typeLine, rest := getLine(rest)
	if !bytes.HasSuffix(typeLine, armorEndOfLine) {
		goto Error
	}
	typeLine = typeLine[0 : len(typeLine)-len(armorEndOfLine)]

	p = &Block{
		Headers: make(map[string]string),
		Type:    string(typeLine),
	}

	for {
		// This loop terminates because getLine's second result is
		// always smaller than it's argument.
		if len(rest) == 0 {
			return nil, data
		}
		line, next := getLine(rest)

		i := bytes.Index(line, []byte{':'})
		if i == -1 {
			break
		}

		// TODO(agl): need to cope with values that spread across lines.
		key, val := line[0:i], line[i+1:]
		key = bytes.TrimSpace(key)
		val = bytes.TrimSpace(val)
		p.Headers[string(key)] = string(val)
		rest = next
	}

	i := bytes.Index(rest, armorEnd)
	if i < 4 {
		print("1\n")
		goto Error
	}
	encodedChecksumLength := 5
	if rest[i-1] == '\r' {
		print("2\n")
		encodedChecksumLength = 6
	}
	encodedChecksum := removeWhitespace(rest[i-encodedChecksumLength : i])
	if encodedChecksum[0] != '=' {
		goto Error
	}
	encodedChecksum = encodedChecksum[1:]
	checksumBytes := make([]byte, base64.StdEncoding.DecodedLen(len(encodedChecksum)))
	n, err := base64.StdEncoding.Decode(checksumBytes, encodedChecksum)
	if err != nil || n != 3 {
		goto Error
	}
	checksum := uint32(checksumBytes[0])<<16 |
		uint32(checksumBytes[1])<<8 |
		uint32(checksumBytes[2])

	base64Data := removeWhitespace(rest[0 : i-encodedChecksumLength])

	p.Bytes = make([]byte, base64.StdEncoding.DecodedLen(len(base64Data)))
	n, err = base64.StdEncoding.Decode(p.Bytes, base64Data)
	if err != nil {
		goto Error
	}
	p.Bytes = p.Bytes[0:n]

	calculatedChecksum := crc24(p.Bytes)
	if calculatedChecksum != checksum {
		print("foo ", calculatedChecksum, " ", checksum, "\n")
		goto Error
	}

	p.Bytes = p.Bytes[0:n]

	_, rest = getLine(rest[i+len(armorEnd):])

	return

Error:
	// If we get here then we have rejected a likely looking, but
	// ultimately invalid block. We need to start over from a new
	// position.  We have consumed the preamble line and will have consumed
	// any lines which could be header lines. However, a valid preamble
	// line is not a valid header line, therefore we cannot have consumed
	// the preamble line for the any subsequent block. Thus, we will always
	// find any valid block, no matter what bytes preceed it.
	//
	// For example, if the input is
	//
	//    -----BEGIN MALFORMED BLOCK-----
	//    junk that may look like header lines
	//   or data lines, but no END line
	//
	//    -----BEGIN ACTUAL BLOCK-----
	//    realdata
	//    -----END ACTUAL BLOCK-----
	//
	// we've failed to parse using the first BEGIN line
	// and now will try again, using the second BEGIN line.
	p, rest = Decode(rest)
	if p == nil {
		rest = data
	}
	return
}
