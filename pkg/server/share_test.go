/*
Copyright 2013 The Camlistore Authors.

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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/test"
)

func TestHandleGetViaSharing(t *testing.T) {
	sto := &test.Fetcher{}
	handler := &shareHandler{fetcher: sto}
	var wr *httptest.ResponseRecorder

	putRaw := func(ref blob.Ref, data string) {
		if _, err := blobserver.Receive(sto, ref, strings.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}

	put := func(blob *schema.Blob) {
		putRaw(blob.BlobRef(), blob.JSON())
	}

	get := func(path string) *shareError {
		wr = httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "http://unused/"+path, nil)
		err := handler.serveHTTP(wr, req)
		if err != nil {
			return err.(*shareError)
		}
		return nil
	}

	testGet := func(path string, expectedError errorCode) {
		err := get(path)
		if expectedError != noError {
			if err == nil || err.code != expectedError {
				t.Errorf("Fetching %s, expected error %#v, but got %#v", path, expectedError, err)
			}
		} else {
			if err != nil {
				t.Errorf("Fetching %s, expected success but got %#v", path, err)
			}
		}

		if wr.HeaderMap.Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("Fetching %s, share response did not contain expected CORS header", path)
		}
	}

	content := "monkey"
	contentRef := blob.SHA1FromString(content)

	// For the purposes of following the via chain, the only thing that
	// matters is that the content of each link contains the name of the
	// next link.
	link := contentRef.String()
	linkRef := blob.SHA1FromString(link)

	share := schema.NewShareRef(schema.ShareHaveRef, false).
		SetShareTarget(linkRef).
		SetSigner(blob.SHA1FromString("irrelevant")).
		SetRawStringField("camliSig", "alsounused")

	testGet(share.Blob().BlobRef().String(), shareFetchFailed)

	put(share.Blob())
	testGet(fmt.Sprintf("%s?via=%s", contentRef, share.Blob().BlobRef()), shareTargetInvalid)

	putRaw(linkRef, link)
	testGet(linkRef.String(), shareReadFailed)
	testGet(share.Blob().BlobRef().String(), noError)
	testGet(fmt.Sprintf("%s?via=%s", linkRef, share.Blob().BlobRef()), noError)
	testGet(fmt.Sprintf("%s?via=%s,%s", contentRef, share.Blob().BlobRef(), linkRef), shareNotTransitive)

	share.SetShareIsTransitive(true)
	put(share.Blob())
	testGet(fmt.Sprintf("%s?via=%s,%s", linkRef, share.Blob().BlobRef(), linkRef), viaChainInvalidLink)

	putRaw(contentRef, content)
	testGet(fmt.Sprintf("%s?via=%s,%s", contentRef, share.Blob().BlobRef(), linkRef), noError)

	share.SetShareExpiration(time.Now().Add(-time.Duration(10) * time.Minute))
	put(share.Blob())
	testGet(fmt.Sprintf("%s?via=%s,%s", contentRef, share.Blob().BlobRef(), linkRef), shareExpired)

	share.SetShareExpiration(time.Now().Add(time.Duration(10) * time.Minute))
	put(share.Blob())
	testGet(fmt.Sprintf("%s?via=%s,%s", contentRef, share.Blob().BlobRef(), linkRef), noError)

	// TODO(aa): assemble
}
