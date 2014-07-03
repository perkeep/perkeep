/*
Copyright 2011 Google Inc.

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
package magic // import "camlistore.org/pkg/magic"

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"go4.org/legal"
)

type prefixEntry struct {
	offset int
	prefix []byte
	mtype  string
}

// usable source: http://www.garykessler.net/library/file_sigs.html
// mime types: http://www.iana.org/assignments/media-types/media-types.xhtml
var prefixTable = []prefixEntry{
	{0, []byte("GIF87a"), "image/gif"},
	{0, []byte("GIF89a"), "image/gif"}, // TODO: Others?
	{0, []byte("\xff\xd8\xff\xe2"), "image/jpeg"},
	{0, []byte("\xff\xd8\xff\xe1"), "image/jpeg"},
	{0, []byte("\xff\xd8\xff\xe0"), "image/jpeg"},
	{0, []byte("\xff\xd8\xff\xdb"), "image/jpeg"},
	{0, []byte("\x49\x49\x2a\x00\x10\x00\x00\x00\x43\x52\x02"), "image/cr2"},
	{0, []byte{137, 'P', 'N', 'G', '\r', '\n', 26, 10}, "image/png"},
	{0, []byte{0x49, 0x20, 0x49}, "image/tiff"},
	{0, []byte{0x49, 0x49, 0x2A, 0}, "image/tiff"},
	{0, []byte{0x4D, 0x4D, 0, 0x2A}, "image/tiff"},
	{0, []byte{0x4D, 0x4D, 0, 0x2B}, "image/tiff"},
	{0, []byte("8BPS"), "image/vnd.adobe.photoshop"},
	{0, []byte("gimp xcf "), "image/x-xcf"},
	{0, []byte("-----BEGIN PGP PUBLIC KEY BLOCK---"), "text/x-openpgp-public-key"},
	{0, []byte("fLaC\x00\x00\x00"), "audio/x-flac"},
	{0, []byte{'I', 'D', '3'}, "audio/mpeg"},
	{0, []byte{0, 0, 1, 0xB7}, "video/mpeg"},
	{0, []byte{0, 0, 0, 0x14, 0x66, 0x74, 0x79, 0x70, 0x71, 0x74, 0x20, 0x20}, "video/quicktime"},
	{0, []byte{0, 0x6E, 0x1E, 0xF0}, "application/vnd.ms-powerpoint"},
	{0, []byte{0x1A, 0x45, 0xDF, 0xA3}, "video/webm"},
	{0, []byte("FLV\x01"), "application/vnd.adobe.flash.video"},
	{0, []byte{0x1F, 0x8B, 0x08}, "application/x-gzip"},
	{0, []byte{0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C}, "application/x-7z-compressed"},
	{0, []byte("BZh"), "application/x-bzip2"},
	{0, []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0}, "application/x-xz"},
	{0, []byte{'P', 'K', 3, 4, 0x0A, 0, 2, 0}, "application/epub+zip"},
	{0, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, "application/vnd.ms-word"},
	{0, []byte{'P', 'K', 3, 4, 0x0A, 0x14, 0, 6, 0}, "application/vnd.openxmlformats-officedocument.custom-properties+xml"},
	{0, []byte{'P', 'K', 3, 4}, "application/zip"},
	{0, []byte("%PDF"), "application/pdf"},
	{0, []byte("{rtf"), "text/rtf1"},
	{0, []byte("BEGIN:VCARD\x0D\x0A"), "text/vcard"},
	{0, []byte("Return-Path: "), "message/rfc822"},

	// Definition data extracted automatically from the file utility source code.
	// See: http://darwinsys.com/file/ (version used: 5.19)
	{4, []byte("moov"), "video/quicktime"},                           // Apple QuickTime
	{4, []byte("mdat"), "video/quicktime"},                           // Apple QuickTime movie (unoptimized)
	{8, []byte("isom"), "video/mp4"},                                 // MPEG v4 system, version 1
	{8, []byte("mp41"), "video/mp4"},                                 // MPEG v4 system, version 1
	{8, []byte("mp42"), "video/mp4"},                                 // MPEG v4 system, version 2
	{8, []byte("mmp4"), "video/mp4"},                                 // MPEG v4 system, 3GPP Mobile
	{8, []byte("3ge"), "video/3gpp"},                                 // MPEG v4 system, 3GPP
	{8, []byte("3gg"), "video/3gpp"},                                 // MPEG v4 system, 3GPP
	{8, []byte("3gp"), "video/3gpp"},                                 // MPEG v4 system, 3GPP
	{8, []byte("3gs"), "video/3gpp"},                                 // MPEG v4 system, 3GPP
	{8, []byte("3g2"), "video/3gpp2"},                                // MPEG v4 system, 3GPP2
	{8, []byte("avc1"), "video/3gpp"},                                // MPEG v4 system, 3GPP JVT AVC
	{0, []byte("MThd"), "audio/midi"},                                // Standard MIDI data
	{0, []byte(".RMF\000\000\000"), "application/vnd.rn-realmedia"},  // RealMedia file
	{0, []byte("MAC\040"), "audio/ape"},                              // Monkey's Audio compressed format
	{0, []byte("MP+"), "audio/musepack"},                             // Musepack audio
	{0, []byte("II\x1a\000\000\000HEAPCCDR"), "image/x-canon-crw"},   // Canon CIFF raw image data
	{0, []byte("II\x2a\000\x10\000\000\000CR"), "image/x-canon-cr2"}, // Canon CR2 raw image data
	{0, []byte("MMOR"), "image/x-olympus-orf"},                       // Olympus ORF raw image data, big-endian
	{0, []byte("IIRO"), "image/x-olympus-orf"},                       // Olympus ORF raw image data, little-endian
	{0, []byte("IIRS"), "image/x-olympus-orf"},                       // Olympus ORF raw image data, little-endian
	{12, []byte("DJVM"), "image/vnd.djvu"},                           // DjVu multiple page document
	{12, []byte("DJVU"), "image/vnd.djvu"},                           // DjVu image or single page document
	{12, []byte("DJVI"), "image/vnd.djvu"},                           // DjVu shared document
	{12, []byte("THUM"), "image/vnd.djvu"},                           // DjVu page thumbnails
	{8, []byte("WAVE"), "audio/x-wav"},                               // WAVE audio
	{8, []byte("AVI\040"), "video/x-msvideo"},                        // AVI
	{0, []byte("OggS"), "application/ogg"},                           // Ogg data
	{8, []byte("AIFF"), "audio/x-aiff"},                              // AIFF audio
	{8, []byte("AIFC"), "audio/x-aiff"},                              // AIFF-C compressed audio
	{8, []byte("8SVX"), "audio/x-aiff"},                              // 8SVX 8-bit sampled sound voice
	{0, []byte("\000\001\000\000\000"), "application/x-font-ttf"},    // TrueType font data
	{0, []byte("d8:announce"), "application/x-bittorrent"},           // BitTorrent file

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
	for _, pte := range prefixTable {
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

// MIMETypeFromReader takes a ReaderAt, sniffs the beginning of it,
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
