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

package test

import (
	"crypto/sha1"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/types"
)

// Blob is a utility class for unit tests.
type Blob struct {
	Contents string // the contents of the blob
}

func (tb *Blob) Blob() *blob.Blob {
	s := tb.Contents
	return blob.NewBlob(tb.BlobRef(), tb.Size(), func() types.ReadSeekCloser {
		return struct {
			io.ReadSeeker
			io.Closer
		}{
			io.NewSectionReader(strings.NewReader(s), 0, int64(len(s))),
			ioutil.NopCloser(nil),
		}
	})
}

func (tb *Blob) BlobRef() blob.Ref {
	h := sha1.New()
	h.Write([]byte(tb.Contents))
	return blob.RefFromHash(h)
}

func (tb *Blob) SizedRef() blob.SizedRef {
	return blob.SizedRef{tb.BlobRef(), tb.Size()}
}

func (tb *Blob) BlobRefSlice() []blob.Ref {
	return []blob.Ref{tb.BlobRef()}
}

func (tb *Blob) Size() uint32 {
	// Check that it's not larger than a uint32 (possible with
	// 64-bit ints).  But while we're here, be more paranoid and
	// check for over the default max blob size of 16 MB.
	if len(tb.Contents) > 16<<20 {
		panic(fmt.Sprintf("test blob of %d bytes is larger than max 16MB allowed in testing", len(tb.Contents)))
	}
	return uint32(len(tb.Contents))
}

func (tb *Blob) Reader() io.Reader {
	return strings.NewReader(tb.Contents)
}

func (tb *Blob) AssertMatches(t *testing.T, sb blob.SizedRef) {
	if sb.Size != tb.Size() {
		t.Fatalf("Got size %d; expected %d", sb.Size, tb.Size())
	}
	if sb.Ref != tb.BlobRef() {
		t.Fatalf("Got blob %q; expected %q", sb.Ref.String(), tb.BlobRef())
	}
}

func (tb *Blob) MustUpload(t *testing.T, ds blobserver.BlobReceiver) {
	sb, err := ds.ReceiveBlob(tb.BlobRef(), tb.Reader())
	if err != nil {
		t.Fatalf("failed to upload blob %v (%q): %v", tb.BlobRef(), tb.Contents, err)
	}
	tb.AssertMatches(t, sb) // TODO: better error reporting
}
