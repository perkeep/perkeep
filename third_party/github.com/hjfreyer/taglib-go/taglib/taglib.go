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

// Package taglib provides utilities for parsing audio tags in
// various formats.
package taglib

import (
	"bytes"
	"errors"
	"io"
	"time"

	"camlistore.org/third_party/github.com/hjfreyer/taglib-go/taglib/id3"
)

var (
	ErrUnrecognizedFormat = errors.New("taglib: format not recognized")
)

// GenericTag is implemented by all the tag types in this project. It
// gives an incomplete view of the information in each tag type, but
// is good enough for most purposes.
type GenericTag interface {
	Title() string
	Artist() string
	Album() string
	Comment() string
	Genre() string
	Year() time.Time
	Track() uint32
	Disc() uint32

	// CustomFrames returns non-standard, user-defined frames as a map from
	// descriptions (e.g. "PERFORMER", "MusicBrainz Album Id", etc.) to
	// values.
	CustomFrames() map[string]string

	// TagSize returns the total size of the tag's header and frames,
	// i.e. the position at which audio data starts.
	TagSize() uint32
}

// Decode reads r and determines which tag format the data is in, if
// any, and calls the decoding function for that format. size
// indicates the total number of bytes accessible through r.
func Decode(r io.ReaderAt, size int64) (GenericTag, error) {
	magic := make([]byte, 4)
	if _, err := r.ReadAt(magic, 0); err != nil {
		return nil, err
	}

	if !bytes.Equal(magic[:3], []byte("ID3")) {
		return nil, ErrUnrecognizedFormat
	}

	switch magic[3] {
	case 3:
		return id3.Decode23(r)
	case 4:
		return id3.Decode24(r)
	default:
		return nil, ErrUnrecognizedFormat
	}
}
