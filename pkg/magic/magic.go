/*
Copyright 2011 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
nYou may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package magic

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

type prefixEntry struct {
	prefix []byte
	mtype  string
}

var prefixTable = []prefixEntry{
	{[]byte("\xff\xd8\xff\xe2"), "image/jpeg"},
	{[]byte("\xff\xd8\xff\xe1"), "image/jpeg"},
	{[]byte("\xff\xd8\xff\xe0"), "image/jpeg"},
	{[]byte("\xff\xd8\xff\xdb"), "image/jpeg"},
	{[]byte{137, 'P', 'N', 'G', '\r', '\n', 26, 10}, "image/png"},
	{[]byte("-----BEGIN PGP PUBLIC KEY BLOCK---"), "text/x-openpgp-public-key"},
}

// Returns the emptry string if unknown.
func MimeType(hdr []byte) string {
	hlen := len(hdr)
	for _, pte := range prefixTable {
		plen := len(pte.prefix)
		if hlen > plen && bytes.Equal(hdr[:plen], pte.prefix) {
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

// MimeTypeFromReader takes a reader, sniffs the beginning of it,
// and returns the mime (if sniffed, else "") and a new reader
// that's the concatenation of the bytes sniffed and the remaining
// reader.
func MimeTypeFromReader(r io.Reader) (mime string, reader io.Reader) {
	var buf bytes.Buffer
	io.CopyN(&buf, r, 1024)
	mime = MimeType(buf.Bytes())
	return mime, io.MultiReader(&buf, r)
}
