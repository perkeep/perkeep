/*
Copyright 2011 The Perkeep Authors

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
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"perkeep.org/pkg/blob"
)

type emptyEnumerator struct {
}

func (ee *emptyEnumerator) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	close(dest)
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
		wr := httptest.NewRecorder()
		wr.Code = 200 // default
		req, _ := http.NewRequest("GET", test.url, nil)
		handleEnumerateBlobs(wr, req, enumerator)

		if got := wr.Code; got != test.expectedCode {
			t.Fatalf("response code for %q: %d want %d", test.name, got, test.expectedCode)
		}
		if got := wr.Body.String(); got != test.expectedBody {
			t.Fatalf("output for %q: %q want %q", test.name, got, wr.Body.String())
		}
	}
}
