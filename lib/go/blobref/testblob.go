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

package blobref

import (
	"crypto/sha1"
	"io"
	"strings"
	"testing"
)

// TestBlob is a utility class for unit tests.
type TestBlob struct {
	Val string
}

func (tb *TestBlob) BlobRef() *BlobRef {
	h := sha1.New()
	h.Write([]byte(tb.Val))
	return FromHash("sha1", h)
}

func (tb *TestBlob) BlobRefSlice() []*BlobRef {
	return []*BlobRef{tb.BlobRef()}
}

func (tb *TestBlob) Size() int64 {
	return int64(len(tb.Val))
}

func (tb *TestBlob) Reader() io.Reader {
	return strings.NewReader(tb.Val)
}

func (tb *TestBlob) AssertMatches(t *testing.T, sb *SizedBlobRef) {
	if sb.Size != tb.Size() {
		t.Fatalf("Got size %d; expected %d", sb.Size, tb.Size())
	}
	if sb.BlobRef.String() != tb.BlobRef().String() {
		t.Fatalf("Got blob %q; expected %q", sb.BlobRef.String(), tb.BlobRef())
	}
}
