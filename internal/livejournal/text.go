/*
Copyright 2026 The Perkeep Authors

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

package livejournal

import (
	"unicode/utf8"
)

// windows1252 maps the bytes 0x80 to 0x9f of Windows-1252 to their runes.
// The rest of its upper half is the same as Latin-1's.
var windows1252 = [32]rune{
	'€', 0x81, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0x8d, 'Ž', 0x8f,
	0x90, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0x9d, 'ž', 'Ÿ',
}

// fixText returns s as valid UTF-8 without control characters that XML
// doesn't allow. Text from before LiveJournal used Unicode is in whatever
// encoding its poster's browser sent, which was usually Windows-1252 (or
// Latin-1, its subset), so a byte that isn't part of valid UTF-8 is
// decoded as Windows-1252. Other control characters than tab, newline,
// and carriage return become U+FFFD.
func fixText(s []byte) []byte {
	if isClean(s) {
		return s
	}
	out := make([]byte, 0, len(s)+len(s)/8)
	for len(s) > 0 {
		r, n := utf8.DecodeRune(s)
		switch {
		case r == utf8.RuneError && n <= 1:
			b := s[0]
			r = rune(b)
			if b >= 0x80 && b <= 0x9f {
				r = windows1252[b-0x80]
			}
			n = 1
		case r < 0x20 && r != '\t' && r != '\n' && r != '\r':
			r = utf8.RuneError
		}
		out = utf8.AppendRune(out, r)
		s = s[n:]
	}
	return out
}

func isClean(s []byte) bool {
	for _, b := range s {
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
			return false
		}
	}
	return utf8.Valid(s)
}

// fixString is fixText for a string.
func fixString(s string) string {
	b := []byte(s)
	if isClean(b) {
		return s
	}
	return string(fixText(b))
}
