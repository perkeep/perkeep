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
	"io"
	"strings"
	"testing"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
)

// Blob is a utility class for unit tests.
type Blob struct {
	Contents string // the contents of the blob
}

func (tb *Blob) BlobRef() *blobref.BlobRef {
	h := sha1.New()
	h.Write([]byte(tb.Contents))
	return blobref.FromHash(h)
}

func (tb *Blob) BlobRefSlice() []*blobref.BlobRef {
	return []*blobref.BlobRef{tb.BlobRef()}
}

func (tb *Blob) Size() int64 {
	return int64(len(tb.Contents))
}

func (tb *Blob) Reader() io.Reader {
	return strings.NewReader(tb.Contents)
}

func (tb *Blob) AssertMatches(t *testing.T, sb blobref.SizedBlobRef) {
	if sb.Size != tb.Size() {
		t.Fatalf("Got size %d; expected %d", sb.Size, tb.Size())
	}
	if sb.BlobRef.String() != tb.BlobRef().String() {
		t.Fatalf("Got blob %q; expected %q", sb.BlobRef.String(), tb.BlobRef())
	}
}

func (tb *Blob) MustUpload(t *testing.T, ds blobserver.BlobReceiver) {
	sb, err := ds.ReceiveBlob(tb.BlobRef(), tb.Reader())
	if err != nil {
		t.Fatalf("failed to upload blob %v (%q): %v", tb.BlobRef(), tb.Contents, err)
	}
	tb.AssertMatches(t, sb) // TODO: better error reporting
}
