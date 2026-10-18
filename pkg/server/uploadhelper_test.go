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

package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/schema"
)

const uploadHelperTestBoundary = "upload-helper-test-boundary"

func uploadHelperTestRequest(body io.Reader) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/ui/?camli.mode=uploadhelper", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+uploadHelperTestBoundary)
	return req
}

func uploadHelperTestField(name string) string {
	return "--" + uploadHelperTestBoundary + "\r\nContent-Disposition: form-data; name=\"" + name + "\"\r\n\r\n"
}

func uploadHelperTestFile() string {
	return "--" + uploadHelperTestBoundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"tiny.txt\"\r\nContent-Type: application/octet-stream\r\n\r\n"
}

func TestUploadHelperFile(t *testing.T) {
	const contents = "ordinary file contents\n"
	const timestamp = "2026-10-15T12:34:56Z"
	for _, tc := range []struct {
		name, modtime string
		wantTime      time.Time
	}{
		{name: "without modtime"},
		{name: "with modtime", modtime: timestamp, wantTime: time.Date(2026, 10, 15, 12, 34, 56, 0, time.UTC)},
		{name: "invalid modtime", modtime: "not a timestamp"},
		{name: "maximum modtime length", modtime: strings.Repeat("x", maxUploadHelperModTime)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var metadata string
			if tc.modtime != "" {
				metadata = uploadHelperTestField("modtime") + tc.modtime + "\r\n"
			}
			body := io.MultiReader(
				strings.NewReader(metadata+uploadHelperTestFile()),
				strings.NewReader(contents),
				strings.NewReader("\r\n--"+uploadHelperTestBoundary+"--\r\n"),
			)
			sto := new(memory.Storage)
			ui := &UIHandler{root: &RootHandler{Storage: sto}}
			rec := httptest.NewRecorder()
			ui.serveUploadHelper(rec, uploadHelperTestRequest(body))
			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
			}
			var response uploadHelperResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Got) != 1 {
				t.Fatalf("got %d uploaded files, want 1", len(response.Got))
			}
			item := response.Got[0]
			if item.FileName != "tiny.txt" || item.FormName != "file" || !item.FileRef.Valid() || !item.ModTime.Time().Equal(tc.wantTime) {
				t.Fatalf("unexpected upload result: %+v", item)
			}
			fr, err := schema.NewFileReader(context.Background(), sto, item.FileRef)
			if err != nil {
				t.Fatalf("uploaded reference is not a readable file schema: %v", err)
			}
			defer fr.Close()
			if fr.FileName() != "tiny.txt" || !fr.ModTime().Equal(tc.wantTime) {
				t.Fatalf("file name/modtime = %q/%v", fr.FileName(), fr.ModTime())
			}
			got, err := io.ReadAll(fr) // Only this tiny fixture is buffered.
			if err != nil || string(got) != contents {
				t.Fatalf("file contents = %q, %v; want %q", got, err, contents)
			}
		})
	}
}

// uploadHelperMetadataReader generates an oversized field without buffering it.
type uploadHelperMetadataReader struct {
	read int
}

func (r *uploadHelperMetadataReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.read += len(p)
	return len(p), nil
}

func TestUploadHelperOversizedModTime(t *testing.T) {
	const metadataSize = 1 << 20
	metadata := new(uploadHelperMetadataReader)
	body := io.MultiReader(
		strings.NewReader(uploadHelperTestField("modtime")),
		io.LimitReader(metadata, metadataSize),
		strings.NewReader("\r\n"+uploadHelperTestFile()+"must not be uploaded\r\n--"+uploadHelperTestBoundary+"--\r\n"),
	)
	sto := new(memory.Storage)
	ui := &UIHandler{root: &RootHandler{Storage: sto}}
	rec := httptest.NewRecorder()
	ui.serveUploadHelper(rec, uploadHelperTestRequest(body))
	checkUploadHelperError(t, rec, http.StatusBadRequest)
	if metadata.read >= metadataSize/2 {
		t.Fatalf("read %d bytes of oversized metadata instead of rejecting it early", metadata.read)
	}
	if sto.NumBlobs() != 0 {
		t.Fatalf("oversized metadata uploaded %d blobs", sto.NumBlobs())
	}
}

func TestUploadHelperMultipartErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{name: "truncated first boundary", body: "--" + uploadHelperTestBoundary},
		{name: "malformed next part header", body: uploadHelperTestFile() + "tiny file\r\n--" + uploadHelperTestBoundary + "\r\ninvalid header\r\n\r\n"},
		{name: "modtime body", body: uploadHelperTestField("modtime") + "2026-10-15T12:34:56Z"},
		{name: "file body", body: uploadHelperTestFile() + "truncated file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := &UIHandler{root: &RootHandler{Storage: new(memory.Storage)}}
			rec := httptest.NewRecorder()
			ui.serveUploadHelper(rec, uploadHelperTestRequest(strings.NewReader(tc.body)))
			checkUploadHelperError(t, rec, http.StatusInternalServerError)
		})
	}
}

func checkUploadHelperError(t *testing.T, rec *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("HTTP %d, want %d: %s", rec.Code, code, rec.Body.String())
	}
	var response map[string]json.RawMessage
	// Unmarshal rejects a success JSON object appended after the error.
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("response is not a single JSON object: %v: %s", err, rec.Body.String())
	}
	var message string
	if err := json.Unmarshal(response["error"], &message); err != nil || message == "" {
		t.Fatalf("response has no error message: %s", rec.Body.String())
	}
	if _, ok := response["got"]; ok {
		t.Fatalf("error response contains upload success: %s", rec.Body.String())
	}
}
