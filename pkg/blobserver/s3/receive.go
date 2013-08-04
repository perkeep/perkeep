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

package s3

import (
	"bytes"
	"crypto/md5"
	"hash"
	"io"
	"io/ioutil"
	"os"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
)

const maxInMemorySlurp = 4 << 20 // 4MB.  *shrug*

// amazonSlurper slurps up a blob to memory (or spilling to disk if
// over maxInMemorySlurp) to verify its digest (and also gets its MD5
// for Amazon's Content-MD5 header, even if the original blobref
// is e.g. sha1-xxxx)
type amazonSlurper struct {
	blob    blob.Ref // only used for tempfile's prefix
	buf     *bytes.Buffer
	md5     hash.Hash
	file    *os.File // nil until allocated
	reading bool     // transitions at most once from false -> true
}

func newAmazonSlurper(blob blob.Ref) *amazonSlurper {
	return &amazonSlurper{
		blob: blob,
		buf:  new(bytes.Buffer),
		md5:  md5.New(),
	}
}

func (as *amazonSlurper) Read(p []byte) (n int, err error) {
	if !as.reading {
		as.reading = true
		if as.file != nil {
			as.file.Seek(0, 0)
		}
	}
	if as.file != nil {
		return as.file.Read(p)
	}
	return as.buf.Read(p)
}

func (as *amazonSlurper) Write(p []byte) (n int, err error) {
	if as.reading {
		panic("write after read")
	}
	as.md5.Write(p)
	if as.file != nil {
		n, err = as.file.Write(p)
		return
	}

	if as.buf.Len()+len(p) > maxInMemorySlurp {
		as.file, err = ioutil.TempFile("", as.blob.String())
		if err != nil {
			return
		}
		_, err = io.Copy(as.file, as.buf)
		if err != nil {
			return
		}
		as.buf = nil
		n, err = as.file.Write(p)
		return
	}

	return as.buf.Write(p)
}

func (as *amazonSlurper) Cleanup() {
	if as.file != nil {
		os.Remove(as.file.Name())
	}
}

func (sto *s3Storage) ReceiveBlob(b blob.Ref, source io.Reader) (outsb blob.SizedRef, outerr error) {
	zero := outsb
	slurper := newAmazonSlurper(b)
	defer slurper.Cleanup()

	hash := b.Hash()
	size, err := io.Copy(io.MultiWriter(hash, slurper), source)
	if err != nil {
		return zero, err
	}
	if !b.HashMatches(hash) {
		return zero, blobserver.ErrCorruptBlob
	}
	err = sto.s3Client.PutObject(b.String(), sto.bucket, slurper.md5, size, slurper)
	if err != nil {
		return zero, err
	}
	return blob.SizedRef{Ref: b, Size: size}, nil
}
