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

package serverinit

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/test"
)

// blobsRecorder is a BlobsReceiver that counts its ReceiveBlobs calls.
type blobsRecorder struct {
	*test.Fetcher
	calls int
}

func (r *blobsRecorder) ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error {
	r.calls++
	return blobserver.ReceiveBlobsUnchecked(ctx, r.Fetcher, blobs)
}

// TestCamliHandlerReceivesIntoStorage checks that the blob handlers
// receive uploads into the storage itself: with its own ReceiveBlobs, and
// notifying its blob hub, where receive hooks are added.
func TestCamliHandlerReceivesIntoStorage(t *testing.T) {
	auth.SetMode(auth.None{})
	rec := &blobsRecorder{Fetcher: new(test.Fetcher)}
	var hooked []blob.Ref
	blobserver.GetHub(rec).AddReceiveBlobsHook(func(sbs []blob.SizedRef) error {
		for _, sb := range sbs {
			hooked = append(hooked, sb.Ref)
		}
		return nil
	})
	h := makeCamliHandler("/bs/", func() string { return "http://example.com" }, rec, nil, nil)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	var want []blob.Ref
	for _, s := range []string{"a", "b"} {
		br := blob.RefFromString(s)
		want = append(want, br)
		w, err := mw.CreateFormFile(br.String(), br.String())
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(s))
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/bs/camli/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("upload = %d: %s", res.Code, res.Body)
	}

	if rec.calls != 1 {
		t.Errorf("storage's ReceiveBlobs called %d times; want 1", rec.calls)
	}
	if rec.NumBlobs() != 2 {
		t.Errorf("stored %d blobs; want 2", rec.NumBlobs())
	}
	if !slices.Equal(hooked, want) {
		t.Errorf("storage's receive hook got %v; want %v", hooked, want)
	}
}
