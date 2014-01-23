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
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type Id3v24Tag struct {
	Header         Id3v24Header
	ExtendedHeader Id3v24ExtendedHeader
	Frames         map[string][]*Id3v24Frame
}

func id3v24Err(format string, args ...interface{}) error {
	return &ErrFormat{
		Format: "ID3 version 2.4",
		Err:    fmt.Errorf(format, args...),
	}
}

func getSimpleId3v24TextFrame(frames []*Id3v24Frame) string {
	if len(frames) == 0 {
		return ""
	}
	fields, err := GetId3v24TextIdentificationFrame(frames[0])
	if err != nil {
		return ""
	}
	return strings.Join(fields, " ")
}

func (t *Id3v24Tag) Title() string {
	return getSimpleId3v24TextFrame(t.Frames["TIT2"])
}

func (t *Id3v24Tag) Artist() string {
	return getSimpleId3v24TextFrame(t.Frames["TPE1"])
}

func (t *Id3v24Tag) Album() string {
	return getSimpleId3v24TextFrame(t.Frames["TALB"])
}

func (t *Id3v24Tag) Comment() string {
	return ""
}

func (t *Id3v24Tag) Genre() string {
	return getSimpleId3v24TextFrame(t.Frames["TCON"])
}

func (t *Id3v24Tag) Year() time.Time {
	yearStr := getSimpleId3v24TextFrame(t.Frames["TDRC"])
	if len(yearStr) < 4 {
		return time.Time{}
	}

	yearInt, err := strconv.Atoi(yearStr[0:4])
	if err != nil {
		return time.Time{}
	}

	return time.Date(yearInt, time.January, 1, 0, 0, 0, 0, time.UTC)
}

func (t *Id3v24Tag) Track() uint32 {
	track, err := parseLeadingInt(getSimpleId3v24TextFrame(t.Frames["TRCK"]))
	if err != nil {
		return 0
	}
	return uint32(track)
}

func (t *Id3v24Tag) Disc() uint32 {
	disc, err := parseLeadingInt(getSimpleId3v24TextFrame(t.Frames["TPOS"]))
	if err != nil {
		return 0
	}
	return uint32(disc)
}

func (t *Id3v24Tag) CustomFrames() map[string]string {
	info := make(map[string]string)
	for _, frame := range t.Frames["TXXX"] {
		// See "4.2.6. User defined text information frame" at
		// http://id3.org/id3v2.4.0-frames. TXXX frames contain
		// NUL-separated descriptions and values.
		parts, err := GetId3v24TextIdentificationFrame(frame)
		if err == nil && len(parts) == 2 {
			info[parts[0]] = parts[1]
		}
	}
	return info
}

func (t *Id3v24Tag) TagSize() uint32 {
	return 10 + t.Header.Size
}

type Id3v24Header struct {
	MinorVersion byte
	Flags        Id3v24HeaderFlags
	Size         uint32
}

type Id3v24HeaderFlags struct {
	Unsynchronization     bool
	ExtendedHeader        bool
	ExperimentalIndicator bool
	FooterPresent         bool
}

type Id3v24ExtendedHeader struct {
	Size  uint32
	Flags Id3v24ExtendedHeaderFlags
}

type Id3v24ExtendedHeaderFlags struct {
	Update          bool
	CrcDataPresent  bool
	TagRestrictions bool
}

type Id3v24Frame struct {
	Header  Id3v24FrameHeader
	Content []byte
}

type Id3v24FrameHeader struct {
	Id    string
	Size  uint32
	Flags Id3v24FrameHeaderFlags
}

type Id3v24FrameHeaderFlags struct {
	TagAlterPreservation  bool
	FileAlterPreservation bool
	ReadOnly              bool

	GroupingIdentity    bool
	Compression         bool
	Encryption          bool
	Unsynchronization   bool
	DataLengthIndicator bool
}

func Decode24(r io.ReaderAt) (*Id3v24Tag, error) {
	headerBytes := make([]byte, 10)
	if _, err := r.ReadAt(headerBytes, 0); err != nil {
		return nil, err
	}

	header, err := parseId3v24Header(headerBytes)
	if err != nil {
		return nil, err
	}

	br := bufio.NewReader(io.NewSectionReader(r, 10, int64(header.Size)))

	var extendedHeader Id3v24ExtendedHeader
	if header.Flags.ExtendedHeader {
		var err error
		if extendedHeader, err = parseId3v24ExtendedHeader(br); err != nil {
			return nil, err
		}
	}

	result := &Id3v24Tag{
		Header:         header,
		ExtendedHeader: extendedHeader,
		Frames:         make(map[string][]*Id3v24Frame),
	}

	var totalSize uint32
	totalSize += extendedHeader.Size

	for totalSize < header.Size {
		hasFrame, err := hasId3v24Frame(br)
		if err != nil {
			return nil, err
		}

		if !hasFrame {
			break
		}

		frame, err := parseId3v24Frame(br)
		if err != nil {
			return nil, err
		}

		// 10 bytes for the frame header, and the body.
		totalSize += 10 + frame.Header.Size

		result.Frames[frame.Header.Id] = append(result.Frames[frame.Header.Id], frame)
	}
	return result, nil
}

func parseId3v24Header(headerBytes []byte) (result Id3v24Header, err error) {
	if !bytes.Equal(headerBytes[0:4], []byte{'I', 'D', '3', 4}) {
		err = id3v24Err("invalid magic numbers")
		return
	}

	result.MinorVersion = headerBytes[4]

	flags := headerBytes[5]

	result.Flags.Unsynchronization = (flags & (1 << 7)) != 0
	result.Flags.ExtendedHeader = (flags & (1 << 6)) != 0
	result.Flags.ExperimentalIndicator = (flags & (1 << 5)) != 0
	result.Flags.FooterPresent = (flags & (1 << 4)) != 0

	result.Size = uint32(parseBase128Int(headerBytes[6:10]))
	return
}

func parseId3v24ExtendedHeader(br *bufio.Reader) (result Id3v24ExtendedHeader, err error) {
	sizeBytes, err := br.Peek(4)
	if err != nil {
		return
	}

	result.Size = uint32(parseBase128Int(sizeBytes))

	headerBytes := make([]byte, result.Size)
	if _, err = io.ReadFull(br, headerBytes); err != nil {
		return
	}

	// Discard size and number of flags bytes, and store flags.
	_, _, flags, headerBytes := headerBytes[:4], headerBytes[4], headerBytes[5], headerBytes[5:]

	result.Flags.Update = (flags & (1 << 6)) != 0
	result.Flags.CrcDataPresent = (flags & (1 << 5)) != 0
	result.Flags.TagRestrictions = (flags & (1 << 4)) != 0

	// Don't do anything with the rest of the extended header for now.

	return
}

func hasId3v24Frame(br *bufio.Reader) (bool, error) {
	data, err := br.Peek(4)
	if err != nil {
		return false, err
	}

	for _, c := range data {
		if (c < 'A' || 'Z' < c) && (c < '0' || '9' < c) {
			return false, nil
		}
	}
	return true, nil
}

func parseId3v24Frame(br *bufio.Reader) (*Id3v24Frame, error) {
	header, err := parseId3v24FrameHeader(br)
	if err != nil {
		return nil, err
	}

	content := make([]byte, header.Size)
	if _, err := io.ReadFull(br, content); err != nil {
		return nil, err
	}

	return &Id3v24Frame{
		Header:  header,
		Content: content,
	}, nil
}

func parseId3v24FrameHeader(br *bufio.Reader) (result Id3v24FrameHeader, err error) {
	headerBytes := make([]byte, 10)
	if _, err = io.ReadFull(br, headerBytes); err != nil {
		return
	}

	idBytes, sizeBytes, flags := headerBytes[0:4], headerBytes[4:8], headerBytes[8:10]
	result.Id = string(idBytes)

	// Read the size as 4 base128 bytes.
	result.Size = uint32(parseBase128Int(sizeBytes))

	result.Flags.TagAlterPreservation = (flags[0] & (1 << 6)) != 0
	result.Flags.FileAlterPreservation = (flags[0] & (1 << 5)) != 0
	result.Flags.ReadOnly = (flags[0] & (1 << 4)) != 0

	result.Flags.GroupingIdentity = (flags[1] & (1 << 6)) != 0
	result.Flags.Compression = (flags[1] & (1 << 3)) != 0
	result.Flags.Encryption = (flags[1] & (1 << 2)) != 0
	result.Flags.Unsynchronization = (flags[1] & (1 << 1)) != 0
	result.Flags.DataLengthIndicator = (flags[1] & (1 << 0)) != 0

	return result, nil
}
