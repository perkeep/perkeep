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

func id3v23Err(format string, args ...interface{}) error {
	return &ErrFormat{
		Format: "ID3 version 2.3",
		Err:    fmt.Errorf(format, args...),
	}
}

type Id3v23Tag struct {
	Header         Id3v23Header
	ExtendedHeader Id3v23ExtendedHeader
	Frames         map[string][]*Id3v23Frame
}

func getSimpleId3v23TextFrame(frames []*Id3v23Frame) string {
	if len(frames) == 0 {
		return ""
	}
	fields, err := GetId3v23TextIdentificationFrame(frames[0])
	if err != nil {
		return ""
	}
	return strings.Join(fields, " ")
}

func (t *Id3v23Tag) Title() string {
	return getSimpleId3v23TextFrame(t.Frames["TIT2"])
}

func (t *Id3v23Tag) Artist() string {
	return getSimpleId3v23TextFrame(t.Frames["TPE1"])
}

func (t *Id3v23Tag) Album() string {
	return getSimpleId3v23TextFrame(t.Frames["TALB"])
}

func (t *Id3v23Tag) Comment() string {
	return ""
}

func (t *Id3v23Tag) Genre() string {
	return getSimpleId3v23TextFrame(t.Frames["TCON"])
}

func (t *Id3v23Tag) Year() time.Time {
	yearStr := getSimpleId3v23TextFrame(t.Frames["TYER"])
	if len(yearStr) != 4 {
		return time.Time{}
	}

	yearInt, err := strconv.Atoi(yearStr)
	if err != nil {
		return time.Time{}
	}

	return time.Date(yearInt, time.January, 1, 0, 0, 0, 0, time.UTC)
}

func (t *Id3v23Tag) Track() uint32 {
	track, err := parseLeadingInt(getSimpleId3v23TextFrame(t.Frames["TRCK"]))
	if err != nil {
		return 0
	}
	return uint32(track)
}

func (t *Id3v23Tag) Disc() uint32 {
	disc, err := parseLeadingInt(getSimpleId3v23TextFrame(t.Frames["TPOS"]))
	if err != nil {
		return 0
	}
	return uint32(disc)
}

func (t *Id3v23Tag) CustomFrames() map[string]string {
	info := make(map[string]string)
	for _, frame := range t.Frames["TXXX"] {
		// See http://id3.org/id3v2.3.0#User_defined_text_information_frame.
		// TXXX frames contain NUL-separated descriptions and values.
		parts, err := GetId3v23TextIdentificationFrame(frame)
		if err == nil && len(parts) == 2 {
			info[parts[0]] = parts[1]
		}
	}
	return info
}

func (t *Id3v23Tag) TagSize() uint32 {
	return 10 + t.Header.Size
}

type Id3v23Header struct {
	MinorVersion byte
	Flags        Id3v23HeaderFlags
	Size         uint32
}

type Id3v23HeaderFlags struct {
	Unsynchronization     bool
	ExtendedHeader        bool
	ExperimentalIndicator bool
}

type Id3v23ExtendedHeader struct {
	Size        uint32
	Flags       Id3v23ExtendedHeaderFlags
	PaddingSize uint32
}

type Id3v23ExtendedHeaderFlags struct {
	CrcDataPresent bool
}

type Id3v23Frame struct {
	Header  Id3v23FrameHeader
	Content []byte
}

type Id3v23FrameHeader struct {
	Id    string
	Size  uint32
	Flags Id3v23FrameHeaderFlags
}

type Id3v23FrameHeaderFlags struct {
	TagAlterPreservation  bool
	FileAlterPreservation bool
	ReadOnly              bool

	Compression      bool
	Encryption       bool
	GroupingIdentity bool
}

func Decode23(r io.ReaderAt) (*Id3v23Tag, error) {
	headerBytes := make([]byte, 10)
	if _, err := r.ReadAt(headerBytes, 0); err != nil {
		return nil, err
	}

	header, err := parseId3v23Header(headerBytes)
	if err != nil {
		return nil, err
	}

	br := bufio.NewReader(io.NewSectionReader(r, 10, int64(header.Size)))

	var extendedHeader Id3v23ExtendedHeader
	if header.Flags.ExtendedHeader {
		var err error
		if extendedHeader, err = parseId3v23ExtendedHeader(br); err != nil {
			return nil, err
		}
	}

	result := &Id3v23Tag{
		Header:         header,
		ExtendedHeader: extendedHeader,
		Frames:         make(map[string][]*Id3v23Frame),
	}

	var totalSize uint32
	totalSize += extendedHeader.Size

	for totalSize < header.Size {
		hasFrame, err := hasId3v23Frame(br)
		if err != nil {
			return nil, err
		}

		if !hasFrame {
			break
		}

		frame, err := parseId3v23Frame(br)
		if err != nil {
			return nil, err
		}

		// 10 bytes for the frame header, and the body.
		totalSize += 10 + frame.Header.Size

		result.Frames[frame.Header.Id] = append(result.Frames[frame.Header.Id], frame)
	}
	return result, nil
}

func parseId3v23Header(headerBytes []byte) (result Id3v23Header, err error) {
	if !bytes.Equal(headerBytes[0:4], []byte{'I', 'D', '3', 3}) {
		err = id3v23Err("invalid magic numbers")
		return
	}

	result.MinorVersion = headerBytes[4]

	flags := headerBytes[5]

	result.Flags.Unsynchronization = (flags & (1 << 7)) != 0
	result.Flags.ExtendedHeader = (flags & (1 << 6)) != 0
	result.Flags.ExperimentalIndicator = (flags & (1 << 5)) != 0

	result.Size = uint32(parseBase128Int(headerBytes[6:10]))
	return
}

func parseId3v23ExtendedHeader(br *bufio.Reader) (result Id3v23ExtendedHeader, err error) {
	sizeBytes := make([]byte, 4)
	if _, err = io.ReadFull(br, sizeBytes); err != nil {
		return
	}

	result.Size = uint32(parseBase128Int(sizeBytes))

	headerBytes := make([]byte, result.Size)
	if _, err = io.ReadFull(br, headerBytes); err != nil {
		return
	}

	// Store the flags bytes and the size of the padding.
	flags, paddingSize, headerBytes := headerBytes[0:2], headerBytes[2:6], headerBytes[6:]

	result.Flags.CrcDataPresent = (flags[0] & (1 << 7)) != 0

	result.PaddingSize = uint32(parseBase128Int(paddingSize))
	// Don't do anything with the rest of the extended header for now.

	return
}

func hasId3v23Frame(br *bufio.Reader) (bool, error) {
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

func parseId3v23Frame(br *bufio.Reader) (*Id3v23Frame, error) {
	header, err := parseId3v23FrameHeader(br)
	if err != nil {
		return nil, err
	}

	content := make([]byte, header.Size)
	if _, err := io.ReadFull(br, content); err != nil {
		return nil, err
	}

	return &Id3v23Frame{
		Header:  header,
		Content: content,
	}, nil
}

func parseId3v23FrameHeader(br *bufio.Reader) (result Id3v23FrameHeader, err error) {
	headerBytes := make([]byte, 10)
	if _, err = io.ReadFull(br, headerBytes); err != nil {
		return
	}

	idBytes, sizeBytes, flags := headerBytes[0:4], headerBytes[4:8], headerBytes[8:10]
	result.Id = string(idBytes)

	// Read the size as 4 base128 bytes.
	result.Size = uint32(parseBase128Int(sizeBytes))

	result.Flags.TagAlterPreservation = (flags[0] & (1 << 7)) != 0
	result.Flags.FileAlterPreservation = (flags[0] & (1 << 6)) != 0
	result.Flags.ReadOnly = (flags[0] & (1 << 5)) != 0

	result.Flags.Compression = (flags[1] & (1 << 7)) != 0
	result.Flags.Encryption = (flags[1] & (1 << 6)) != 0
	result.Flags.GroupingIdentity = (flags[1] & (1 << 5)) != 0

	return result, nil
}
