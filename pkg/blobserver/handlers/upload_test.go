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

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/protocol"
	"perkeep.org/pkg/blobserver/uploadsession"
	"perkeep.org/pkg/test"
)

// uploadStorage is a BlobsReceiver recording the sizes of its
// ReceiveBlobs calls.
type uploadStorage struct {
	*test.Fetcher
	calls []int
	err   error // if non-nil, what ReceiveBlobs returns, storing nothing
}

func (s *uploadStorage) ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error {
	s.calls = append(s.calls, len(blobs))
	if s.err != nil {
		return s.err
	}
	return blobserver.ReceiveBlobsUnchecked(ctx, s.Fetcher, blobs)
}

// uploadPart is a part of a multipart upload: a blob ref and contents.
type uploadPart struct {
	name     string
	contents string
}

func blobPart(s string) uploadPart {
	return uploadPart{blob.RefFromString(s).String(), s}
}

// upload sends parts to the multipart upload handler, returning its
// response.
func upload(t *testing.T, sto blobserver.BlobReceiver, parts []uploadPart) *protocol.UploadResponse {
	t.Helper()
	return uploadURL(t, sto, nil, "/bs/camli/upload", parts)
}

// uploadRequest returns a multipart upload request of parts to url.
func uploadRequest(t *testing.T, url string, parts []uploadPart) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range parts {
		w, err := mw.CreateFormFile(p.name, p.name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(p.contents))
	}
	mw.Close()
	req := httptest.NewRequest("POST", url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// uploadURL is like upload, with upload sessions, to url.
func uploadURL(t *testing.T, sto blobserver.BlobReceiver, sessions *uploadsession.Manager, url string, parts []uploadPart) *protocol.UploadResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	CreateBatchUploadHandler(sto, nil, sessions).ServeHTTP(rec, uploadRequest(t, url, parts))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status %d: %s", rec.Code, rec.Body)
	}
	res := new(protocol.UploadResponse)
	if err := json.Unmarshal(rec.Body.Bytes(), res); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body, err)
	}
	return res
}

func receivedRefs(res *protocol.UploadResponse) []blob.Ref {
	var refs []blob.Ref
	for _, sb := range res.Received {
		refs = append(refs, sb.Ref)
	}
	return refs
}

func TestMultipartUploadGroups(t *testing.T) {
	sto := &uploadStorage{Fetcher: new(test.Fetcher)}
	var parts []uploadPart
	var want []blob.Ref
	for i := range uploadGroupBlobs + 10 {
		p := blobPart(fmt.Sprintf("blob %d", i))
		parts = append(parts, p)
		want = append(want, blob.MustParse(p.name))
	}
	bad := blob.RefFromString("bad")
	parts = append(parts, uploadPart{bad.String(), "BAD"}) // contents don't match

	res := upload(t, sto, parts)
	if got := receivedRefs(res); !slices.Equal(got, want) {
		t.Errorf("received %d blobs; want %d, in order", len(got), len(want))
	}
	if !strings.Contains(res.ErrorText, bad.String()) || !strings.Contains(res.ErrorText, blobserver.ErrCorruptBlob.Error()) {
		t.Errorf("ErrorText = %q; want it to report %v as corrupt", res.ErrorText, bad)
	}
	// The corrupt blob never reaches storage.
	if want := []int{uploadGroupBlobs, 10}; !slices.Equal(sto.calls, want) {
		t.Errorf("ReceiveBlobs calls of sizes %v; want %v", sto.calls, want)
	}
	if sto.NumBlobs() != len(want) {
		t.Errorf("stored %d blobs; want %d", sto.NumBlobs(), len(want))
	}
}

func TestMultipartUploadTooBig(t *testing.T) {
	sto := &uploadStorage{Fetcher: new(test.Fetcher)}
	a, b := blobPart("a"), blobPart("b")
	big := uploadPart{blob.RefFromString("big").String(), strings.Repeat("x", blobserver.MaxBlobSize+1)}
	res := upload(t, sto, []uploadPart{a, big, b})
	// Reading stops at the oversized blob, but the one before it is
	// still received.
	if got, want := receivedRefs(res), []blob.Ref{blob.MustParse(a.name)}; !slices.Equal(got, want) {
		t.Errorf("received %v; want %v", got, want)
	}
	if !strings.Contains(res.ErrorText, "over the limit") {
		t.Errorf("ErrorText = %q; want it to report the blob over the limit", res.ErrorText)
	}
}

func TestMultipartUploadStorageError(t *testing.T) {
	sto := &uploadStorage{Fetcher: new(test.Fetcher), err: blobserver.ErrReadonly}
	res := upload(t, sto, []uploadPart{blobPart("a"), blobPart("b")})
	if len(res.Received) != 0 {
		t.Errorf("received %v; want nothing", receivedRefs(res))
	}
	if want := "Error receiving 2 blobs: " + blobserver.ErrReadonly.Error(); strings.TrimSpace(res.ErrorText) != want {
		t.Errorf("ErrorText = %q; want %q", res.ErrorText, want)
	}
}

func TestMultipartUploadDuplicate(t *testing.T) {
	sto := &uploadStorage{Fetcher: new(test.Fetcher)}
	a := blobPart("a")
	res := upload(t, sto, []uploadPart{a, a})
	if got, want := receivedRefs(res), []blob.Ref{blob.MustParse(a.name)}; !slices.Equal(got, want) {
		t.Errorf("received %v; want %v", got, want)
	}
	if res.ErrorText != "" {
		t.Errorf("ErrorText = %q", res.ErrorText)
	}
}
