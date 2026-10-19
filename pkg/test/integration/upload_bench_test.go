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
// index has caught up. With session, the blobs are uploaded into an
// upload session, and it measures until a flush of the session reports
// them all durable.
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
		session                 bool
	}{
		{1, 5, false},   // a client's HTTP/1.1 concurrency
		{1, 50, false},  // a client's HTTP/2 concurrency
		{256, 1, false}, // one request at a time, each of many blobs
		{1, 5, true},
		{1, 50, true},
	} {
		name := fmt.Sprintf("perRequest=%d/concurrency=%d", bc.perRequest, bc.concurrency)
		if bc.session {
			name += "/session"
		}
		b.Run(name, func(b *testing.B) {
			benchmarkUpload(b, bc.perRequest, bc.concurrency, bc.session)
		})
	}
}

func benchmarkUpload(b *testing.B, perRequest, concurrency int, session bool) {
	w, err := test.WorldFromConfig("bench-upload-server-config.json")
	if err != nil {
		b.Fatal(err)
	}
	if err := w.Start(); err != nil {
		b.Fatal(err)
	}
	defer w.Stop()
	blobRoot := w.ServerBaseURL() + "/bs/camli/"
	uploadURL := blobRoot + "upload"

	post := func(url, contentType string, body []byte, v any) error {
		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.SetBasicAuth("testuser", "passTestWorld")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("POST %s: %v", url, res.Status)
		}
		return json.NewDecoder(res.Body).Decode(v)
	}
	var sessionID string
	if session {
		var opened protocol.SessionOpenResponse
		if err := post(blobRoot+"session", "", nil, &opened); err != nil {
			b.Fatal(err)
		}
		sessionID = opened.Session
		uploadURL += "?session=" + sessionID
	}

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
		var ur protocol.UploadResponse
		if err := post(uploadURL, bd.contentType, bd.data, &ur); err != nil {
			return err
		}
		got := ur.Received
		if session {
			got = ur.Accepted
		}
		if len(got) != len(bd.refs) {
			return fmt.Errorf("server took %d of %d blobs: %s", len(got), len(bd.refs), ur.ErrorText)
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
	if session && firstErr.Load() == nil {
		var st protocol.SessionStatusResponse
		err := post(blobRoot+"session/"+sessionID+"/flush", "", nil, &st)
		if err == nil && (len(st.Durable) != b.N || len(st.Failed) > 0) {
			err = fmt.Errorf("flush reported %d of %d blobs durable, %d failed", len(st.Durable), b.N, len(st.Failed))
		}
		if err != nil {
			firstErr.CompareAndSwap(nil, &err)
		}
	}
	b.StopTimer()
	if errp := firstErr.Load(); errp != nil {
		b.Fatal(*errp)
	}
}
