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

package search_test

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"hash/crc32"
	"reflect"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	. "perkeep.org/pkg/search"
)

func digestsOf(s string) []string {
	data := []byte(s)
	return []string{
		fmt.Sprintf("crc32-%08x", crc32.ChecksumIEEE(data)),
		fmt.Sprintf("md5-%x", md5.Sum(data)),
		fmt.Sprintf("sha1-%x", sha1.Sum(data)),
		fmt.Sprintf("sha224-%x", sha256.Sum224(data)),
		fmt.Sprintf("sha256-%x", sha256.Sum256(data)),
	}
}

func digestConstraint(d string) *Constraint {
	return &Constraint{File: &FileConstraint{Digest: d}}
}

// TestQueryFileSizeAndCRC32 checks finding files by their size and
// CRC-32, as of a zip file's entries, which a corpus does in memory.
func TestQueryFileSizeAndCRC32(t *testing.T) {
	testQuery(t, func(qt *queryTest) {
		id := qt.id
		f1, _ := id.UploadFile("a.jpg", "hello", time.Unix(100, 0))
		f1b, _ := id.UploadFile("b.jpg", "hello", time.Unix(200, 0))
		id.UploadFile("c.jpg", "world", time.Unix(300, 0)) // same size, other CRC-32

		sizeAndCRC := func(size int64, d string) *Constraint {
			return &Constraint{File: &FileConstraint{
				FileSize: &IntConstraint{Equals: &size},
				Digest:   d,
			}}
		}
		if qt.itype != indexClassic {
			qt.candidateSource = "corpus_candidates"
		}
		qt.wantRes(&SearchQuery{Constraint: sizeAndCRC(5, digestsOf("hello")[0])}, f1, f1b)
		qt.wantRes(&SearchQuery{Constraint: sizeAndCRC(6, digestsOf("hello")[0])})
		qt.wantRes(&SearchQuery{Constraint: sizeAndCRC(5, "crc32-00000000")})
	})
}

// TestQueryFileDigest checks finding files by each type of digest alone,
// which a corpus finds the candidates of in the index's storage, so it
// doesn't use the indexCorpusScan type, which prevents that.
func TestQueryFileDigest(t *testing.T) {
	testQueryTypes(t, []indexType{indexClassic, indexCorpusBuild}, func(qt *queryTest) {
		id := qt.id
		f1, _ := id.UploadFile("a.jpg", "hello", time.Unix(100, 0))
		f1b, _ := id.UploadFile("b.jpg", "hello", time.Unix(200, 0))
		f2, _ := id.UploadFile("c.jpg", "world", time.Unix(300, 0))

		if qt.itype != indexClassic {
			qt.candidateSource = "corpus_candidates"
		}
		for _, d := range digestsOf("hello") {
			t.Logf("digest %s", d)
			qt.wantRes(&SearchQuery{Constraint: digestConstraint(d)}, f1, f1b)
		}
		qt.wantRes(&SearchQuery{Constraint: digestConstraint(digestsOf("world")[1])}, f2)
		qt.wantRes(&SearchQuery{Constraint: digestConstraint("md5-" + strings.Repeat("0", 32))})
	})
}

func TestQueryFileDigestInvalid(t *testing.T) {
	testQueryTypes(t, corpusTypeOnly, func(qt *queryTest) {
		for _, d := range []string{"crc32-CBF43926", "md5-abc", "sha512-" + strings.Repeat("0", 128)} {
			_, err := qt.Handler().Query(ctxbg, &SearchQuery{Constraint: digestConstraint(d)})
			if err == nil || !strings.Contains(err.Error(), "invalid digest") {
				t.Errorf("query of digest %q: error %v; want invalid digest", d, err)
			}
		}
	})
}

func TestDescribeFileDigests(t *testing.T) {
	testQueryTypes(t, []indexType{indexClassic, indexCorpusBuild}, func(qt *queryTest) {
		fileRef, _ := qt.id.UploadFile("a.jpg", "hello", time.Unix(100, 0))
		for _, withDigests := range []bool{false, true} {
			res, err := qt.Handler().Query(ctxbg, &SearchQuery{
				Constraint: &Constraint{BlobRefPrefix: fileRef.String()},
				Describe:   &DescribeRequest{Digests: withDigests},
			})
			if err != nil {
				t.Fatal(err)
			}
			db := res.Describe.Meta.Get(fileRef)
			if db == nil || db.File == nil {
				t.Fatalf("file %v not described: %+v", fileRef, res.Describe.Meta)
			}
			var want []string
			if withDigests {
				want = digestsOf("hello")
			}
			if !reflect.DeepEqual(db.Digests, want) {
				t.Errorf("with Digests %v, described digests = %q; want %q", withDigests, db.Digests, want)
			}
		}
	})
}

// BenchmarkQueryFileBySizeAndCRC32 finds a file by its size and CRC-32
// among files of the same size.
func BenchmarkQueryFileBySizeAndCRC32(b *testing.B) {
	testQueryTypes(b, corpusTypeOnly, func(qt *queryTest) {
		id := qt.id
		const n = 1000
		var want blob.Ref
		for i := range n {
			contents := fmt.Sprintf("%08d", i)
			f, _ := id.UploadFile(fmt.Sprintf("f%d.jpg", i), contents, time.Unix(int64(i), 0))
			if i == n/2 {
				want = f
			}
		}
		size := int64(8)
		c := &Constraint{File: &FileConstraint{
			FileSize: &IntConstraint{Equals: &size},
			Digest:   digestsOf(fmt.Sprintf("%08d", n/2))[0],
		}}
		h := qt.Handler()
		for b.Loop() {
			res, err := h.Query(ctxbg, &SearchQuery{Constraint: c})
			if err != nil {
				b.Fatal(err)
			}
			if len(res.Blobs) != 1 || res.Blobs[0].Blob != want {
				b.Fatalf("got %v; want %v", res.Blobs, want)
			}
		}
	})
}
