/*
Copyright 2011 The Perkeep Authors

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

// Package magic implements MIME type sniffing of data based on the
// well-known "magic" number prefixes in the file.
package magic // import "perkeep.org/internal/magic"

import (
	"bytes"
	"encoding/binary"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"go4.org/legal"
)

// A matchEntry contains rules for matching byte prefix (typically 1KB)
// and, on a match, contains the resulting MIME type.
// A matcher is either a function or an (offset+prefix).
type matchEntry struct {
	// fn specifies a matching function. If set, offset & prefix
	// are not used.
	fn func(prefix []byte) bool

	// offset is how many bytes of the input 1KB to ignore before
	// matching the prefix.
	offset int

	// prefix is the prefix to look for at offset. (admittedly, if
	// offset is non-zero, it's more of a substring than a prefix)
	prefix []byte

	// mtype is the resulting MIME type, on a match.
	mtype string
}

// matchTable is a list of matchers to match prefixes against. The
// first matching one wins.
//
// usable source: http://www.garykessler.net/library/file_sigs.html
// mime types: http://www.iana.org/assignments/media-types/media-types.xhtml
var matchTable = []matchEntry{
	{prefix: []byte("GIF87a"), mtype: "image/gif"},
	{prefix: []byte("GIF89a"), mtype: "image/gif"}, // TODO: Others?
	{prefix: []byte("\xff\xd8\xff\xe2"), mtype: "image/jpeg"},
	{prefix: []byte("\xff\xd8\xff\xe1"), mtype: "image/jpeg"},
	{prefix: []byte("\xff\xd8\xff\xe0"), mtype: "image/jpeg"},
	{prefix: []byte("\xff\xd8\xff\xdb"), mtype: "image/jpeg"},
	{prefix: []byte("\x49\x49\x2a\x00\x10\x00\x00\x00\x43\x52\x02"), mtype: "image/cr2"},
	{prefix: []byte{137, 'P', 'N', 'G', '\r', '\n', 26, 10}, mtype: "image/png"},
	{prefix: []byte{0x49, 0x20, 0x49}, mtype: "image/tiff"},
	{prefix: []byte{0x49, 0x49, 0x2A, 0}, mtype: "image/tiff"},
	{prefix: []byte{0x4D, 0x4D, 0, 0x2A}, mtype: "image/tiff"},
	{prefix: []byte{0x4D, 0x4D, 0, 0x2B}, mtype: "image/tiff"},
	{prefix: []byte("8BPS"), mtype: "image/vnd.adobe.photoshop"},
	{prefix: []byte("gimp xcf "), mtype: "image/x-xcf"},
	{prefix: []byte("-----BEGIN PGP PUBLIC KEY BLOCK---"), mtype: "text/x-openpgp-public-key"},
	{prefix: []byte("fLaC\x00\x00\x00"), mtype: "audio/x-flac"},
	{prefix: []byte{'I', 'D', '3'}, mtype: "audio/mpeg"},
	{prefix: []byte{0, 0, 1, 0xB7}, mtype: "video/mpeg"},
	{prefix: []byte{0, 0, 0, 0x14, 0x66, 0x74, 0x79, 0x70, 0x71, 0x74, 0x20, 0x20}, mtype: "video/quicktime"},
	{prefix: []byte{0, 0x6E, 0x1E, 0xF0}, mtype: "application/vnd.ms-powerpoint"},
	{prefix: []byte{0x1A, 0x45, 0xDF, 0xA3}, mtype: "video/webm"},
	{prefix: []byte("FLV\x01"), mtype: "application/vnd.adobe.flash.video"},
	{prefix: []byte{0x1F, 0x8B, 0x08}, mtype: "application/x-gzip"},
	{prefix: []byte{0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C}, mtype: "application/x-7z-compressed"},
	{prefix: []byte("BZh"), mtype: "application/x-bzip2"},
	{prefix: []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0}, mtype: "application/x-xz"},
	{prefix: []byte{'P', 'K', 3, 4, 0x0A, 0, 2, 0}, mtype: "application/epub+zip"},
	{prefix: []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, mtype: "application/vnd.ms-word"},
	{prefix: []byte{'P', 'K', 3, 4, 0x0A, 0x14, 0, 6, 0}, mtype: "application/vnd.openxmlformats-officedocument.custom-properties+xml"},
	{prefix: []byte{'P', 'K', 3, 4}, mtype: "application/zip"},
	{prefix: []byte("%PDF"), mtype: "application/pdf"},
	{prefix: []byte("{rtf"), mtype: "text/rtf1"},
	{prefix: []byte("BEGIN:VCARD\x0D\x0A"), mtype: "text/vcard"},
	{prefix: []byte("Return-Path: "), mtype: "message/rfc822"},

	// Definition data extracted automatically from the file utility source code.
	// See: http://darwinsys.com/file/ (version used: 5.19)
	{offset: 4, prefix: []byte("moov"), mtype: "video/quicktime"},                // Apple QuickTime
	{offset: 4, prefix: []byte("mdat"), mtype: "video/quicktime"},                // Apple QuickTime movie (unoptimized)
	{offset: 8, prefix: []byte("isom"), mtype: "video/mp4"},                      // MPEG v4 system, version 1
	{offset: 8, prefix: []byte("mp41"), mtype: "video/mp4"},                      // MPEG v4 system, version 1
	{offset: 8, prefix: []byte("mp42"), mtype: "video/mp4"},                      // MPEG v4 system, version 2
	{offset: 8, prefix: []byte("mmp4"), mtype: "video/mp4"},                      // MPEG v4 system, 3GPP Mobile
	{offset: 8, prefix: []byte("3ge"), mtype: "video/3gpp"},                      // MPEG v4 system, 3GPP
	{offset: 8, prefix: []byte("3gg"), mtype: "video/3gpp"},                      // MPEG v4 system, 3GPP
	{offset: 8, prefix: []byte("3gp"), mtype: "video/3gpp"},                      // MPEG v4 system, 3GPP
	{offset: 8, prefix: []byte("3gs"), mtype: "video/3gpp"},                      // MPEG v4 system, 3GPP
	{offset: 8, prefix: []byte("3g2"), mtype: "video/3gpp2"},                     // MPEG v4 system, 3GPP2
	{offset: 8, prefix: []byte("avc1"), mtype: "video/3gpp"},                     // MPEG v4 system, 3GPP JVT AVC
	{prefix: []byte("MThd"), mtype: "audio/midi"},                                // Standard MIDI data
	{prefix: []byte(".RMF\000\000\000"), mtype: "application/vnd.rn-realmedia"},  // RealMedia file
	{prefix: []byte("MAC\040"), mtype: "audio/ape"},                              // Monkey's Audio compressed format
	{prefix: []byte("MP+"), mtype: "audio/musepack"},                             // Musepack audio
	{prefix: []byte("II\x1a\000\000\000HEAPCCDR"), mtype: "image/x-canon-crw"},   // Canon CIFF raw image data
	{prefix: []byte("II\x2a\000\x10\000\000\000CR"), mtype: "image/x-canon-cr2"}, // Canon CR2 raw image data
	{prefix: []byte("MMOR"), mtype: "image/x-olympus-orf"},                       // Olympus ORF raw image data, big-endian
	{prefix: []byte("IIRO"), mtype: "image/x-olympus-orf"},                       // Olympus ORF raw image data, little-endian
	{prefix: []byte("IIRS"), mtype: "image/x-olympus-orf"},                       // Olympus ORF raw image data, little-endian
	{offset: 12, prefix: []byte("DJVM"), mtype: "image/vnd.djvu"},                // DjVu multiple page document
	{offset: 12, prefix: []byte("DJVU"), mtype: "image/vnd.djvu"},                // DjVu image or single page document
	{offset: 12, prefix: []byte("DJVI"), mtype: "image/vnd.djvu"},                // DjVu shared document
	{offset: 12, prefix: []byte("THUM"), mtype: "image/vnd.djvu"},                // DjVu page thumbnails
	{offset: 8, prefix: []byte("WAVE"), mtype: "audio/x-wav"},                    // WAVE audio
	{offset: 8, prefix: []byte("AVI\040"), mtype: "video/x-msvideo"},             // AVI
	{prefix: []byte("OggS"), mtype: "application/ogg"},                           // Ogg data
	{offset: 8, prefix: []byte("AIFF"), mtype: "audio/x-aiff"},                   // AIFF audio
	{offset: 8, prefix: []byte("AIFC"), mtype: "audio/x-aiff"},                   // AIFF-C compressed audio
	{offset: 8, prefix: []byte("8SVX"), mtype: "audio/x-aiff"},                   // 8SVX 8-bit sampled sound voice
	{prefix: []byte("\000\001\000\000\000"), mtype: "application/x-font-ttf"},    // TrueType font data
	{prefix: []byte("d8:announce"), mtype: "application/x-bittorrent"},           // BitTorrent file

	// iOS HEIC images
	{fn: isHEIC, mtype: "image/heic"},

	// TODO(bradfitz): popular audio & video formats at least
}

func init() {
	legal.RegisterLicense(`
$File: LEGAL.NOTICE,v 1.15 2006/05/03 18:48:33 christos Exp $
Copyright (c) Ian F. Darwin 1986, 1987, 1989, 1990, 1991, 1992, 1994, 1995.
Software written by Ian F. Darwin and others;
maintained 1994- Christos Zoulas.

This software is not subject to any export provision of the United States
Department of Commerce, and may be exported to any country or planet.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions
are met:
1. Redistributions of source code must retain the above copyright
   notice immediately at the beginning of the file, without modification,
   this list of conditions, and the following disclaimer.
2. Redistributions in binary form must reproduce the above copyright
   notice, this list of conditions and the following disclaimer in the
   documentation and/or other materials provided with the distribution.

THIS SOFTWARE IS PROVIDED BY THE AUTHOR AND CONTRIBUTORS ''AS IS'' AND
ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
ARE DISCLAIMED. IN NO EVENT SHALL THE AUTHOR OR CONTRIBUTORS BE LIABLE FOR
ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS
OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION)
HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT
LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY
OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF
SUCH DAMAGE.
`)
}

// MIMEType returns the MIME type from the data in the provided header
// of the data.
// It returns the empty string if the MIME type can't be determined.
func MIMEType(hdr []byte) string {
	hlen := len(hdr)
	for _, pte := range matchTable {
		if pte.fn != nil {
			if pte.fn(hdr) {
				return pte.mtype
			}
			continue
		}
		plen := pte.offset + len(pte.prefix)
		if hlen > plen && bytes.Equal(hdr[pte.offset:plen], pte.prefix) {
			return pte.mtype
		}
	}
	t := http.DetectContentType(hdr)
	t = strings.Replace(t, "; charset=utf-8", "", 1)
	if t != "application/octet-stream" && t != "text/plain" {
		return t
	}
	return ""
}

// MIMETypeFromReader takes a reader, sniffs the beginning of it,
// and returns the mime (if sniffed, else "") and a new reader
// that's the concatenation of the bytes sniffed and the remaining
// reader.
func MIMETypeFromReader(r io.Reader) (mime string, reader io.Reader) {
	var buf bytes.Buffer
	_, err := io.Copy(&buf, io.LimitReader(r, 1024))
	mime = MIMEType(buf.Bytes())
	if err != nil {
		return mime, io.MultiReader(&buf, errReader{err})
	}
	return mime, io.MultiReader(&buf, r)
}

// MIMETypeFromReaderAt takes a ReaderAt, sniffs the beginning of it,
// and returns the MIME type if sniffed, else the empty string.
func MIMETypeFromReaderAt(ra io.ReaderAt) (mime string) {
	var buf [1024]byte
	n, _ := ra.ReadAt(buf[:], 0)
	return MIMEType(buf[:n])
}

// errReader is an io.Reader which just returns err.
type errReader struct{ err error }

func (er errReader) Read([]byte) (int, error) { return 0, er.err }

// TODO(mpl): unexport VideoExtensions

// VideoExtensions are common video filename extensions that are not
// covered by mime.TypeByExtension.
var VideoExtensions = map[string]bool{
	"m1v": true,
	"m2v": true,
	"m4v": true,
}

// HasExtension returns whether the file extension of filename is among
// extensions. It is a case-insensitive lookup, optimized for the ASCII case.
func HasExtension(filename string, extensions map[string]bool) bool {
	var ext string
	if e := filepath.Ext(filename); strings.HasPrefix(e, ".") {
		ext = e[1:]
	} else {
		return false
	}

	// Case-insensitive lookup.
	// Optimistically assume a short ASCII extension and be
	// allocation-free in that case.
	var buf [10]byte
	lower := buf[:0]
	const utf8RuneSelf = 0x80 // from utf8 package, but not importing it.
	for i := 0; i < len(ext); i++ {
		c := ext[i]
		if c >= utf8RuneSelf {
			// Slow path.
			return extensions[strings.ToLower(ext)]
		}
		if 'A' <= c && c <= 'Z' {
			lower = append(lower, c+('a'-'A'))
		} else {
			lower = append(lower, c)
		}
	}
	// The conversion from []byte to string doesn't allocate in
	// a map lookup.
	return extensions[string(lower)]
}

// MIMETypeByExtension calls mime.TypeByExtension, and removes optional parameters,
// to keep only the type and subtype.
func MIMETypeByExtension(ext string) string {
	mimeParts := strings.SplitN(mime.TypeByExtension(ext), ";", 2)
	return strings.TrimSpace(mimeParts[0])
}

var pict = []byte("pict")

// isHEIC reports whether the prefix looks like a BMFF HEIF file for a
// still image. (image/heic type)
//
// We verify it starts with an "ftyp" box of MajorBrand heic, and then
// has a "hdlr" box of HandlerType "pict" (inside a meta box which we
// don't verify). This isn't a compliant parser, so might have false
// positives on invalid inputs, but that's acceptable, as long as it
// doesn't reject any valid HEIC images.
//
// The structure of the header of such a file looks like:
//
// Box: type "ftyp", size 24
// - *bmff.FileTypeBox: &{box:0xc00009a1e0 MajorBrand:heic MinorVersion:^@^@^@^@ Compatible:[mif1 heic]}
// Box: type "meta", size 4027
// - *bmff.MetaBox, 8 children:
//     Box: type "hdlr", size 34
//     - *bmff.HandlerBox: &{FullBox:{box:0xc00009a2d0 Version:0 Flags:0} HandlerType:pict Name:}
func isHEIC(prefix []byte) bool {
	if len(prefix) < 12 {
		return false
	}
	if string(prefix[4:12]) != "ftypheic" {
		return false
	}

	// Mini allocation-free BMFF parser for the two box types we
	// care about. We consersatively only check whether the "hdlr"
	// box type is "pict" for now, until we get a larger corpus of
	// HEIF files from iOS devices. (We'll probably want a
	// different mime type for videos in HEIF wrappers, but I
	// haven't run across those ... yet.)

	// Consume the "ftyp" box, required to be first in file.
	ftypLen := binary.BigEndian.Uint32(prefix[:4])
	if uint32(len(prefix)) < ftypLen {
		return false
	}

	// The meta box should follow the ftyp box, but we don't verify it here.
	// See comment above.
	metaBox := prefix[ftypLen:]

	// In the meta box, match /hdlr.{8}pict/, but without using a regexp.
	// The handler box always has its handler type 12 bytes into the record.
	const typeOffset = 12 // bytes from "hdlr" literal to 4 byte handler type
	pictPos := bytes.Index(metaBox, pict)
	if pictPos < typeOffset { // including -1
		return false
	}
	if string(metaBox[pictPos-12:pictPos-8]) != "hdlr" {
		return false
	}
	return true
}
