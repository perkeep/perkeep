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

package index

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"hash/crc32"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
)

// wantDigests returns the digests of data, computed separately from
// fileDigester.
func wantDigests(data []byte) []string {
	return []string{
		fmt.Sprintf("crc32-%08x", crc32.ChecksumIEEE(data)),
		fmt.Sprintf("md5-%x", md5.Sum(data)),
		fmt.Sprintf("sha1-%x", sha1.Sum(data)),
		fmt.Sprintf("sha224-%x", sha256.Sum224(data)),
		fmt.Sprintf("sha256-%x", sha256.Sum256(data)),
	}
}

// addWantDigestRows adds to want the digest rows of data, the contents
// of a file.
func addWantDigestRows(want map[string]string, data []byte) {
	whole := blob.RefFromBytes(data)
	digests := wantDigests(data)
	want[keyFileDigests.Key(whole)] = strings.Join(digests, "|")
	for _, d := range digests {
		want[keyDigestToWhole.Key(d, whole)] = "1"
	}
}

func TestFileDigester(t *testing.T) {
	for _, data := range []string{"", "123456789", strings.Repeat("perkeep", 10000)} {
		d := newFileDigester()
		// Write in pieces, as a file's chunks are.
		for s := data; s != ""; {
			n := min(len(s), 1000)
			d.Write([]byte(s[:n]))
			s = s[n:]
		}
		got, want := d.digests(), wantDigests([]byte(data))
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("digests of %d bytes = %q; want %q", len(data), got, want)
		}
		if got, want := d.wholeRef(), blob.RefFromString(data); got != want {
			t.Errorf("wholeRef of %d bytes = %v; want %v", len(data), got, want)
		}
		for _, d := range got {
			if !ValidDigest(d) {
				t.Errorf("ValidDigest(%q) = false", d)
			}
		}
	}
	// The check value of CRC-32 (IEEE), as zip files use it.
	d := newFileDigester()
	d.Write([]byte("123456789"))
	if got := d.digests()[0]; got != "crc32-cbf43926" {
		t.Errorf("CRC-32 of the check string = %q; want crc32-cbf43926", got)
	}
}

func BenchmarkFileDigester(b *testing.B) {
	buf := make([]byte, 1<<20)
	b.SetBytes(int64(len(buf)))
	for b.Loop() {
		d := newFileDigester()
		d.Write(buf)
		d.digests()
	}
}

func TestValidDigest(t *testing.T) {
	for _, tt := range []struct {
		d    string
		want bool
	}{
		{"crc32-cbf43926", true},
		{"crc32-0000000", false},
		{"crc32-CBF43926", false}, // not canonical
		{"md5-" + strings.Repeat("0", 32), true},
		{"md5-" + strings.Repeat("0", 31) + "g", false},
		{"sha1-" + strings.Repeat("a", 40), true},
		{"sha224-" + strings.Repeat("a", 56), true},
		{"sha256-" + strings.Repeat("a", 64), true},
		{"sha256-" + strings.Repeat("a", 56), false},
		{"sha512-" + strings.Repeat("a", 128), false},
		{"crc32", false},
		{"", false},
	} {
		if got := ValidDigest(tt.d); got != tt.want {
			t.Errorf("ValidDigest(%q) = %v; want %v", tt.d, got, tt.want)
		}
	}
}
