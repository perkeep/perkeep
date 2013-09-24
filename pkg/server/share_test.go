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

	var err *shareError

	if err = get(share.Blob().BlobRef().String()); err == nil || err.code != shareFetchFailed {
		t.Error("Expected missing blob error")
	}

	put(share.Blob())
	if err = get(fmt.Sprintf("%s?via=%s", contentRef, share.Blob().BlobRef())); err == nil || err.code != shareTargetInvalid {
		t.Error("Expected invalid target error")
	}

	putRaw(linkRef, link)
	if err = get(linkRef.String()); err == nil || err.code != shareReadFailed {
		t.Error("Expected invalid share blob error")
	}

	if err = get(share.Blob().BlobRef().String()); err != nil {
		t.Error("Expected to successfully fetch share, but got: %s", err)
	}

	if err = get(fmt.Sprintf("%s?via=%s", linkRef, share.Blob().BlobRef())); err != nil {
		t.Error("Expected to successfully fetch link via share, but got: %s", err)
	}

	if err = get(fmt.Sprintf("%s?via=%s,%s", contentRef, share.Blob().BlobRef(), linkRef)); err == nil || err.code != shareNotTransitive {
		t.Error("Expected share not transitive error")
	}

	share.SetShareIsTransitive(true)
	put(share.Blob())
	if err = get(fmt.Sprintf("%s?via=%s,%s", linkRef, share.Blob().BlobRef(), linkRef)); err == nil || err.code != viaChainInvalidLink {
		t.Error("Expected via chain invalid link err")
	}

	putRaw(contentRef, content)
	if err = get(fmt.Sprintf("%s?via=%s,%s", contentRef, share.Blob().BlobRef(), linkRef)); err != nil {
		t.Error("Expected to succesfully fetch via link via share, but got: %s", err)
	}

	share.SetShareExpiration(time.Now().Add(-time.Duration(10) * time.Minute))
	put(share.Blob())
	if err = get(fmt.Sprintf("%s?via=%s,%s", contentRef, share.Blob().BlobRef(), linkRef)); err == nil || err.code != shareExpired {
		t.Error("Expected share expired error")
	}

	share.SetShareExpiration(time.Now().Add(time.Duration(10) * time.Minute))
	put(share.Blob())
	if err = get(fmt.Sprintf("%s?via=%s,%s", contentRef, share.Blob().BlobRef(), linkRef)); err != nil {
		t.Error("Expected to successfully fetch unexpired share, but got: %s", err)
	}

	// TODO(aa): assemble
}
