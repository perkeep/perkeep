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

package integration

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/protocol"
	"perkeep.org/pkg/test"
)

// benchBlobSize is about the average size of a file chunk.
const benchBlobSize = 64 << 10

// benchBlobSeq makes every benchmark blob unique, across runs too.
var benchBlobSeq atomic.Uint64

// BenchmarkUpload measures how fast a server with the default storage,
// index, and sync-to-index setup (see bench-upload-server-config.json)
// receives new 64KB blobs, uploaded one per request from several
// concurrent requests, as clients do today, or many per request. It
// measures until each blob's upload request is answered, not until the
// index has caught up.
//
// Storage syncs to disk, so for meaningful numbers, run it with TMPDIR on
// a real disk, not a tmpfs. Each run starts a new server, so set
// PERKEEP_TEST_BIN_DIR to prebuilt binaries rather than building them for
// every run, and raise -timeout:
//
//	go build -o $DIR/ ./server/perkeepd ./cmd/pk ./cmd/pk-get ./cmd/pk-put ./cmd/pk-mount ./app/webdav
//	PERKEEP_TEST_BIN_DIR=$DIR TMPDIR=/some/disk go test -run XXX -bench BenchmarkUpload -benchtime 512x -timeout 60m ./pkg/test/integration
func BenchmarkUpload(b *testing.B) {
	for _, bc := range []struct {
		perRequest, concurrency int
	}{
		{1, 5},   // a client's HTTP/1.1 concurrency
		{1, 50},  // a client's HTTP/2 concurrency
		{256, 1}, // one request at a time, each of many blobs
	} {
		b.Run(fmt.Sprintf("perRequest=%d/concurrency=%d", bc.perRequest, bc.concurrency), func(b *testing.B) {
			benchmarkUpload(b, bc.perRequest, bc.concurrency)
		})
	}
}

func benchmarkUpload(b *testing.B, perRequest, concurrency int) {
	w, err := test.WorldFromConfig("bench-upload-server-config.json")
	if err != nil {
		b.Fatal(err)
	}
	if err := w.Start(); err != nil {
		b.Fatal(err)
	}
	defer w.Stop()
	uploadURL := w.ServerBaseURL() + "/bs-and-maybe-also-index/camli/upload"

	// Build the request bodies first, so only uploading is timed.
	type body struct {
		data        []byte
		contentType string
		refs        []blob.Ref
	}
	var bodies []body
	for left := b.N; left > 0; left -= perRequest {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		var refs []blob.Ref
		for range min(left, perRequest) {
			data := make([]byte, benchBlobSize)
			binary.BigEndian.PutUint64(data, benchBlobSeq.Add(1))
			rand.NewChaCha8([32]byte{}).Read(data[8:])
			br := blob.RefFromBytes(data)
			part, err := mw.CreateFormFile(br.String(), br.String())
			if err != nil {
				b.Fatal(err)
			}
			part.Write(data)
			refs = append(refs, br)
		}
		mw.Close()
		bodies = append(bodies, body{buf.Bytes(), mw.FormDataContentType(), refs})
	}

	upload := func(bd body) error {
		req, err := http.NewRequest("POST", uploadURL, bytes.NewReader(bd.data))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", bd.contentType)
		req.SetBasicAuth("testuser", "passTestWorld")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("upload: %v", res.Status)
		}
		var ur protocol.UploadResponse
		if err := json.NewDecoder(res.Body).Decode(&ur); err != nil {
			return err
		}
		if len(ur.Received) != len(bd.refs) {
			return fmt.Errorf("server received %d of %d blobs: %s", len(ur.Received), len(bd.refs), ur.ErrorText)
		}
		return nil
	}

	b.SetBytes(benchBlobSize)
	b.ResetTimer()
	work := make(chan body)
	var wg sync.WaitGroup
	var firstErr atomic.Pointer[error]
	for range concurrency {
		wg.Go(func() {
			for bd := range work {
				if err := upload(bd); err != nil {
					firstErr.CompareAndSwap(nil, &err)
				}
			}
		})
	}
	for _, bd := range bodies {
		work <- bd
	}
	close(work)
	wg.Wait()
	b.StopTimer()
	if errp := firstErr.Load(); errp != nil {
		b.Fatal(*errp)
	}
}
