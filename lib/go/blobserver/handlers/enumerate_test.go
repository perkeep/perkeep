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

package handlers

import (
	"bufio"
	"bytes"
	"camli/blobref"
	"camli/blobserver"
	. "camli/testing"
	"http"
	"io"
	"os"
	"testing"
)

type responseWriterMethodCall struct {
	method                 string
	headerKey, headerValue string // if method == "SetHeader"
	bytesWritten           []byte // if method == "Write"
	responseCode           int    // if method == "WriteHeader"
}

type recordingResponseWriter struct {
	log    []*responseWriterMethodCall
	status int
	output *bytes.Buffer
}

func (rw *recordingResponseWriter) RemoteAddr() string {
	return "1.2.3.4"
}

func (rw *recordingResponseWriter) UsingTLS() bool {
	return false
}

func (rw *recordingResponseWriter) SetHeader(k, v string) {
	rw.log = append(rw.log, &responseWriterMethodCall{method: "SetHeader", headerKey: k, headerValue: v})
}

func (rw *recordingResponseWriter) Write(buf []byte) (int, os.Error) {
	rw.log = append(rw.log, &responseWriterMethodCall{method: "Write", bytesWritten: buf})
	rw.output.Write(buf)
	if rw.status == 0 {
		rw.status = 200
	}
	return len(buf), nil
}

func (rw *recordingResponseWriter) WriteHeader(code int) {
	rw.log = append(rw.log, &responseWriterMethodCall{method: "WriteHeader", responseCode: code})
	rw.status = code
}

func (rw *recordingResponseWriter) Flush() {
	rw.log = append(rw.log, &responseWriterMethodCall{method: "Flush"})
}

func (rw *recordingResponseWriter) Hijack() (io.ReadWriteCloser, *bufio.ReadWriter, os.Error) {
	panic("Not supported")
}

func NewRecordingWriter() *recordingResponseWriter {
	return &recordingResponseWriter{
	output: &bytes.Buffer{},
	}
}

func makeGetRequest(url string) *http.Request {
	req := &http.Request{
        Method: "GET",
	RawURL: url,
	}
	var err os.Error
	req.URL, err = http.ParseURL(url)
	if err != nil {
		panic("Error parsing url: " + url)
	}
	return req
}

type emptyEnumerator struct {
}

func (ee *emptyEnumerator) EnumerateBlobs(dest chan *blobref.SizedBlobRef,
        partition blobserver.Partition,
        after string,
        limit uint,
        waitSeconds int) os.Error {
	dest <- nil
	return nil
}

type enumerateInputTest struct {
	name         string
	url          string
	expectedCode int
	expectedBody string
}

func TestEnumerateInput(t *testing.T) {
	enumerator := &emptyEnumerator{}

	emptyOutput := "{\n  \"blobs\": [\n\n  ],\n  \"canLongPoll\": true\n}\n"

	tests := []enumerateInputTest{
		{"no 'after' with 'maxwaitsec'",
			"http://example.com/camli/enumerate-blobs?after=foo&maxwaitsec=1", 400,
			errMsgMaxWaitSecWithAfter},
		{"'maxwaitsec' of 0 is okay with 'after'",
			"http://example.com/camli/enumerate-blobs?after=foo&maxwaitsec=0", 200,
			emptyOutput},
	}
	for _, test := range tests {
		wr := NewRecordingWriter()
		req := makeGetRequest(test.url)
		handleEnumerateBlobs(wr, req, enumerator, nil)  // TODO: use better partition
		ExpectInt(t, test.expectedCode, wr.status, "response code for " + test.name)
		ExpectString(t, test.expectedBody, wr.output.String(), "output for " + test.name)
	}
}
