/*
Copyright 2013 The Perkeep Authors.

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

// Package hashutil contains misc hashing functions lacking homes elsewhere.
package hashutil // import "perkeep.org/internal/hashutil"

import (
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"

	"perkeep.org/pkg/blob"
)

// SHA256Prefix computes the SHA-256 digest of data and returns
// its first twenty lowercase hex digits.
func SHA256Prefix(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))[:20]
}

// SHA1Prefix computes the SHA-1 digest of data and returns
// its first twenty lowercase hex digits.
func SHA1Prefix(data []byte) string {
	h := sha1.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))[:20]
}

// TrackDigestReader is an io.Reader wrapper which records the digest of what it reads.
type TrackDigestReader struct {
	r io.Reader
	h hash.Hash

	// DoLegacySHA1 sets whether to also compute the legacy SHA-1 hash.
	DoLegacySHA1 bool
	s1           hash.Hash // optional legacy SHA-1 hash, for servers with old data
}

func NewTrackDigestReader(r io.Reader) *TrackDigestReader {
	return &TrackDigestReader{r: r}
}

// Hash returns the current hash sum.
func (t *TrackDigestReader) Hash() hash.Hash {
	return t.h
}

// LegacySHA1Hash returns the current legacy SHA-1 hash sum.
func (t *TrackDigestReader) LegacySHA1Hash() hash.Hash {
	return t.s1
}

func (t *TrackDigestReader) Read(p []byte) (n int, err error) {
	n, err = t.r.Read(p)
	if t.h == nil {
		// TODO(mpl): maybe let the constructor take a Hash, and then no need to depend on blob pkg.
		t.h = blob.NewHash()
	}
	t.h.Write(p[:n])

	if t.DoLegacySHA1 {
		if t.s1 == nil {
			t.s1 = sha1.New()
		}
		t.s1.Write(p[:n])
	}
	return n, err
}
