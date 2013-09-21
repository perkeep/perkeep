/*
Copyright 2013 Google Inc.

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
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/test"
	. "camlistore.org/pkg/test/asserts"
)

func TestHandleGetViaSharing(t *testing.T) {
	// TODO(aa): It would be good if we could test that we are failing for
	// the right reason for all of these (some kind of internal error code).

	sto := &test.Fetcher{}
	handler := &httputil.PrefixHandler{"/", &shareHandler{sto}}
	wr := &httptest.ResponseRecorder{}

	get := func(path string) *httptest.ResponseRecorder {
		wr = httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "http://unused/"+path, nil)
		handler.ServeHTTP(wr, req)
		return wr
	}

	content := "monkey"
	contentRef := blob.SHA1FromString(content)

	// For the purposes of following the via chain, the only thing that
	// matters is that the content of each link contains the name of the
	// next link.
	link := contentRef.String()
	linkRef := blob.SHA1FromString(link)

	share := schema.NewShareRef(schema.ShareHaveRef, linkRef, false).
		SetSigner(blob.SHA1FromString("irrelevant")).
		SetRawStringField("camliSig", "alsounused")

	log.Print("Should fail because first link does not exist")
	get(share.Blob().BlobRef().String())
	ExpectInt(t, 401, wr.Code, "")

	log.Print("Should fail because share target does not match next link")
	sto.ReceiveBlob(share.Blob().BlobRef(), strings.NewReader(share.Blob().JSON()))
	get(contentRef.String() + "?via=" + share.Blob().BlobRef().String())
	ExpectInt(t, 401, wr.Code, "")

	log.Print("Should fail because first link is not a share")
	sto.ReceiveBlob(linkRef, strings.NewReader(link))
	get(linkRef.String())
	ExpectInt(t, 401, wr.Code, "")
	log.Print("Should successfully fetch share")
	get(share.Blob().BlobRef().String())
	ExpectInt(t, 200, wr.Code, "")

	log.Print("Should successfully fetch link via share")
	get(linkRef.String() + "?via=" + share.Blob().BlobRef().String())
	ExpectInt(t, 200, wr.Code, "")

	log.Print("Should fail because share is not transitive")
	get(contentRef.String() + "?via=" + share.Blob().BlobRef().String() + "," + linkRef.String())
	ExpectInt(t, 401, wr.Code, "")

	log.Print("Should fail because link content does not contain target")
	share.SetShareIsTransitive(true)
	sto.ReceiveBlob(share.Blob().BlobRef(), strings.NewReader(share.Blob().JSON()))
	get(linkRef.String() + "?via=" + share.Blob().BlobRef().String() + "," + linkRef.String())
	ExpectInt(t, 401, wr.Code, "")

	log.Print("Should successfully fetch content via link via share")
	sto.ReceiveBlob(contentRef, strings.NewReader(content))
	get(contentRef.String() + "?via=" + share.Blob().BlobRef().String() + "," + linkRef.String())
	ExpectInt(t, 200, wr.Code, "")

	log.Print("Should fail because share is expired")
	share.SetShareExpiration(time.Now().Add(-time.Duration(10) * time.Minute))
	sto.ReceiveBlob(share.Blob().BlobRef(), strings.NewReader(share.Blob().JSON()))
	get(contentRef.String() + "?via=" + share.Blob().BlobRef().String() + "," + linkRef.String())
	ExpectInt(t, 401, wr.Code, "")

	log.Print("Should succeed because share has not expired")
	share.SetShareExpiration(time.Now().Add(time.Duration(10) * time.Minute))
	sto.ReceiveBlob(share.Blob().BlobRef(), strings.NewReader(share.Blob().JSON()))
	get(contentRef.String() + "?via=" + share.Blob().BlobRef().String() + "," + linkRef.String())
	ExpectInt(t, 200, wr.Code, "")

	// TODO(aa): assemble
}
