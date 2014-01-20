// Copyright 2013 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package id3

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

type ErrFormat struct {
	Format string
	Err    error
}

func (e ErrFormat) Error() string {
	return fmt.Sprintf("gotaglib: error parsing format %q: %v", e.Format, e.Err)
}

func parseBase128Int(bytes []byte) uint64 {
	var result uint64
	for _, b := range bytes {
		result = result << 7
		result |= uint64(b)
	}
	return result
}

func parseLeadingInt(s string) (int, error) {
	var intEnd int
	for intEnd < len(s) && '0' <= s[intEnd] && s[intEnd] <= '9' {
		intEnd++
	}
	return strconv.Atoi(s[0:intEnd])
}

func getTextIdentificationFrame(content []byte) ([]string, error) {
	normalized, err := parseText(content)
	if err != nil {
		return nil, err
	}
	return strings.Split(normalized, string([]byte{0})), nil
}

// Parses a string from frame data. The first byte represents the encoding:
//   0x01  ISO-8859-1
//   0x02  UTF-16 w/ BOM
//   0x03  UTF-16BE w/o BOM
//   0x04  UTF-8
//
// Refer to section 4 of http://id3.org/id3v2.4.0-structure
func parseText(strBytes []byte) (string, error) {
	encoding, strBytes := strBytes[0], strBytes[1:]

	switch encoding {
	case 0: // ISO-8859-1 text.
		return parseIso8859(strBytes), nil

	case 1: // UTF-16 with BOM.
		return parseUtf16WithBOM(strBytes)

	case 2: // UTF-16BE without BOM.
		return parseUtf16(strBytes, binary.BigEndian)

	case 3: // UTF-8 text.
		return parseUtf8(strBytes)

	default:
		return "", id3v24Err("invalid encoding byte %x", encoding)
	}
}

func parseIso8859(strBytes []byte) string {
	runes := make([]rune, len(strBytes))
	for i, b := range strBytes {
		runes[i] = rune(b)
	}
	return string(runes)
}

func parseUtf16WithBOM(strBytes []byte) (string, error) {
	if strBytes[0] == 0xFE && strBytes[1] == 0xFF {
		return parseUtf16(strBytes[2:], binary.BigEndian)
	}
	if strBytes[0] == 0xFF && strBytes[1] == 0xFE {
		return parseUtf16(strBytes[2:], binary.LittleEndian)
	}
	return "", id3v24Err("invalid byte order marker %x %x", strBytes[0], strBytes[1])
}

func parseUtf16(strBytes []byte, bo binary.ByteOrder) (string, error) {
	shorts := make([]uint16, 0, len(strBytes)/2)
	for i := 0; i < len(strBytes); i += 2 {
		short := bo.Uint16(strBytes[i : i+2])
		shorts = append(shorts, short)
	}

	return string(utf16.Decode(shorts)), nil
}

func parseUtf8(strBytes []byte) (string, error) {
	return string(strBytes), nil
}
