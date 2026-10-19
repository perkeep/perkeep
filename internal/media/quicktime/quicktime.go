/*
Copyright 2026 The Perkeep Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package quicktime reads metadata from QuickTime (.mov) and MP4 files,
// such as the movie duration and the "mdta" metadata items written by
// Apple devices.
package quicktime // import "perkeep.org/internal/media/quicktime"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go4.org/media/heif/bmff"
)

// Well-known metadata item keys written by Apple devices.
const (
	KeyContentIdentifier = "com.apple.quicktime.content.identifier"
	KeyLocationISO6709   = "com.apple.quicktime.location.ISO6709"
	KeyMake              = "com.apple.quicktime.make"
	KeyModel             = "com.apple.quicktime.model"
	KeySoftware          = "com.apple.quicktime.software"
	KeyCreationDate      = "com.apple.quicktime.creationdate"
	KeyLivePhotoAuto     = "com.apple.quicktime.live-photo.auto"
	KeyLivePhotoVitality = "com.apple.quicktime.live-photo.vitality-score"
)

// Metadata is the metadata of a movie.
type Metadata struct {
	// Duration is the movie's duration, or zero if unknown.
	Duration time.Duration

	// Items are the movie's "mdta" metadata items, keyed by name
	// (e.g. KeyContentIdentifier). Numeric values are formatted in
	// decimal. Items of other types, such as images, are omitted.
	Items map[string]string
}

// ErrNoMovie is returned by ReadMetadata when the input has no movie
// ("moov") box.
var ErrNoMovie = errors.New("quicktime: no moov box")

// maxMetaSize is the largest metadata box that ReadMetadata reads.
// Apple's are a few kilobytes.
const maxMetaSize = 1 << 20

// ReadMetadata reads the metadata of the QuickTime or MP4 file r of the
// given size. It seeks past the media data rather than reading it.
func ReadMetadata(r io.ReaderAt, size int64) (*Metadata, error) {
	br := bmff.NewReader(io.NewSectionReader(r, 0, size))
	for {
		b, err := br.ReadBox()
		if err == io.EOF {
			return nil, ErrNoMovie
		}
		if err != nil {
			return nil, fmt.Errorf("quicktime: %w", err)
		}
		if b.Type() != bmff.TypeMoov {
			continue
		}
		start := b.Offset() + b.HeaderSize()
		n := b.Size() - b.HeaderSize()
		if b.Size() == 0 { // extends to end of file
			n = size - start
		}
		return readMovie(io.NewSectionReader(r, start, n))
	}
}

func readMovie(moov io.Reader) (*Metadata, error) {
	md := &Metadata{}
	br := bmff.NewReader(moov)
	for {
		b, err := br.ReadBox()
		if err == io.EOF {
			return md, nil
		}
		if err != nil {
			return nil, fmt.Errorf("quicktime: reading moov: %w", err)
		}
		switch {
		case b.Type().EqualString("mvhd"):
			pb, err := b.Parse()
			if err != nil {
				return nil, fmt.Errorf("quicktime: %w", err)
			}
			md.Duration = duration(pb.(*bmff.MovieHeaderBox))
		case b.Type() == bmff.TypeMeta && b.Size() > 0 && b.Size() <= maxMetaSize:
			pb, err := b.Parse()
			if err != nil {
				return nil, fmt.Errorf("quicktime: %w", err)
			}
			if items := metaItems(pb.(*bmff.MetaBox)); items != nil && md.Items == nil {
				md.Items = items
			}
		}
	}
}

func duration(mh *bmff.MovieHeaderBox) time.Duration {
	if mh.TimeScale == 0 {
		return 0
	}
	// All ones means unknown.
	if mh.Version == 0 && mh.Duration == math.MaxUint32 || mh.Duration == math.MaxUint64 {
		return 0
	}
	secs := float64(mh.Duration) / float64(mh.TimeScale)
	if secs >= math.MaxInt64/float64(time.Second) {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

// metaItems returns the items of an "mdta" metadata box, or nil if mb
// has another handler type or no items.
func metaItems(mb *bmff.MetaBox) map[string]string {
	var (
		hdlr *bmff.HandlerBox
		keys *bmff.KeysBox
		ilst *bmff.ItemListBox
	)
	for _, c := range mb.Children {
		pb, err := c.Parse()
		if err != nil {
			continue
		}
		switch pb := pb.(type) {
		case *bmff.HandlerBox:
			hdlr = pb
		case *bmff.KeysBox:
			keys = pb
		case *bmff.ItemListBox:
			ilst = pb
		}
	}
	if hdlr == nil || hdlr.HandlerType != "mdta" || keys == nil || ilst == nil {
		return nil
	}
	items := make(map[string]string)
	for _, it := range ilst.Items {
		if it.KeyIndex == 0 || int64(it.KeyIndex) > int64(len(keys.Keys)) || len(it.Data) == 0 {
			continue
		}
		key := keys.Keys[it.KeyIndex-1]
		if key.Namespace != "mdta" {
			continue
		}
		if _, dup := items[key.Name]; dup {
			continue
		}
		if v, ok := formatValue(it.Data[0]); ok {
			items[key.Name] = v
		}
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

// Well-known data types. See
// https://developer.apple.com/documentation/quicktime-file-format/well-known_types
const (
	typeUTF8     = 1
	typeSignedBE = 21
	typeUintBE   = 22
	typeFloat32  = 23
	typeFloat64  = 24
)

func formatValue(d *bmff.DataBox) (string, bool) {
	v := d.Value
	switch d.DataType {
	case typeUTF8:
		s := string(v)
		if !utf8.ValidString(s) {
			s = strings.ToValidUTF8(s, "�")
		}
		return s, true
	case typeSignedBE, typeUintBE:
		var u uint64
		switch len(v) {
		case 1, 2, 3, 4, 8:
			for _, b := range v {
				u = u<<8 | uint64(b)
			}
		default:
			return "", false
		}
		if d.DataType == typeUintBE {
			return strconv.FormatUint(u, 10), true
		}
		shift := 64 - 8*len(v) // sign-extend
		return strconv.FormatInt(int64(u<<shift)>>shift, 10), true
	case typeFloat32:
		if len(v) != 4 {
			return "", false
		}
		return strconv.FormatFloat(float64(math.Float32frombits(binary.BigEndian.Uint32(v))), 'g', -1, 32), true
	case typeFloat64:
		if len(v) != 8 {
			return "", false
		}
		return strconv.FormatFloat(math.Float64frombits(binary.BigEndian.Uint64(v)), 'g', -1, 64), true
	}
	return "", false
}
