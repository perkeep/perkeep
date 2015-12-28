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

type shareTester struct {
	t          *testing.T
	sto        *test.Fetcher
	handler    *shareHandler
	sleeps     int
	rec        *httptest.ResponseRecorder
	restoreLog func()
}

func newShareTester(t *testing.T) *shareTester {
	sto := new(test.Fetcher)
	st := &shareTester{
		t:          t,
		sto:        sto,
		handler:    &shareHandler{fetcher: sto},
		restoreLog: test.TLog(t),
	}
	timeSleep = func(d time.Duration) {
		st.sleeps++
	}
	return st
}

func (st *shareTester) done() {
	timeSleep = time.Sleep
	st.restoreLog()
}

func (st *shareTester) slept() bool {
	v := st.sleeps > 0
	st.sleeps = 0
	return v
}

func (st *shareTester) putRaw(ref blob.Ref, data string) {
	if _, err := blobserver.Receive(st.sto, ref, strings.NewReader(data)); err != nil {
		st.t.Fatal(err)
	}
}

func (st *shareTester) put(blob *schema.Blob) {
	st.putRaw(blob.BlobRef(), blob.JSON())
}

func (st *shareTester) get(path string) *shareError {
	st.rec = httptest.NewRecorder()
	req, err := http.NewRequest("GET", "http://unused/"+path, nil)
	if err != nil {
		st.t.Fatalf("NewRequest(path=%q): %v", path, err)
	}
	if err := st.handler.serveHTTP(st.rec, req); err != nil {
		return err.(*shareError)
	}
	return nil
}

func (st *shareTester) testGet(path string, wantErr errorCode) {
	gotErr := st.get(path)
	if wantErr != noError {
		if gotErr == nil || gotErr.code != wantErr {
			st.t.Errorf("Fetching %s, error = %v; want %v", path, gotErr, wantErr)
		}
	} else {
		if gotErr != nil {
			st.t.Errorf("Fetching %s, error = %v; want success", path, gotErr)
		}
	}
}

func TestHandleGetViaSharing(t *testing.T) {
	st := newShareTester(t)
	defer st.done()

	content := "monkey" // the secret
	contentRef := blob.SHA1FromString(content)

	link := fmt.Sprintf(`{"camliVersion": 1,
"camliType": "file",
"parts": [
   {"blobRef": "%v", "size": %d}
]}`, contentRef, len(content))
	linkRef := blob.SHA1FromString(link)

	share := schema.NewShareRef(schema.ShareHaveRef, false).
		SetShareTarget(linkRef).
		SetSigner(blob.SHA1FromString("irrelevant")).
		SetRawStringField("camliSig", "alsounused")
	shareRef := func() blob.Ref { return share.Blob().BlobRef() }

	t.Logf("Checking share blob doesn't yet exist...")
	st.testGet(shareRef().String(), shareFetchFailed)
	if !st.slept() {
		t.Error("expected sleep after miss")
	}
	st.put(share.Blob())
	t.Logf("Checking share blob now exists...")
	st.testGet(shareRef().String(), noError)

	t.Logf("Checking we can't get the content directly via the share...")
	st.testGet(fmt.Sprintf("%s?via=%s", contentRef, shareRef()), shareTargetInvalid)

	t.Logf("Checking we can't get the link (file) blob directly...")
	st.putRaw(linkRef, link)
	st.testGet(linkRef.String(), shareBlobInvalid)

	t.Logf("Checking we can get the link (file) blob fia the share...")
	st.testGet(fmt.Sprintf("%s?via=%s", linkRef, shareRef()), noError)

	t.Logf("Checking we can't get the link (file) blob fia the non-transitive share...")
	st.testGet(fmt.Sprintf("%s?via=%s,%s", contentRef, shareRef(), linkRef), shareNotTransitive)

	// TODO: new test?
	share.SetShareIsTransitive(true)
	st.put(share.Blob())
	st.testGet(fmt.Sprintf("%s?via=%s,%s", linkRef, shareRef(), linkRef), viaChainInvalidLink)

	st.putRaw(contentRef, content)
	st.testGet(fmt.Sprintf("%s?via=%s,%s", contentRef, shareRef(), linkRef), noError)

	// new test?
	share.SetShareExpiration(time.Now().Add(-time.Duration(10) * time.Minute))
	st.put(share.Blob())
	st.testGet(fmt.Sprintf("%s?via=%s,%s", contentRef, shareRef(), linkRef), shareExpired)

	share.SetShareExpiration(time.Now().Add(time.Duration(10) * time.Minute))
	st.put(share.Blob())
	st.testGet(fmt.Sprintf("%s?via=%s,%s", contentRef, shareRef(), linkRef), noError)
}

// Issue 228: only follow transitive blobref links in known trusted schema fields.
func TestSharingTransitiveSafety(t *testing.T) {
	st := newShareTester(t)
	defer st.done()

	content := "the secret"
	contentRef := blob.SHA1FromString(content)

	// User-injected blob, somehow.
	evilClaim := fmt.Sprintf("Some payload containing the ref: %v", contentRef)
	evilClaimRef := blob.SHA1FromString(evilClaim)

	share := schema.NewShareRef(schema.ShareHaveRef, false).
		SetShareTarget(evilClaimRef).
		SetShareIsTransitive(true).
		SetSigner(blob.SHA1FromString("irrelevant")).
		SetRawStringField("camliSig", "alsounused")
	shareRef := func() blob.Ref { return share.Blob().BlobRef() }

	st.put(share.Blob())
	st.putRaw(contentRef, content)
	st.putRaw(evilClaimRef, evilClaim)

	st.testGet(shareRef().String(), noError)
	st.testGet(fmt.Sprintf("%s?via=%s", evilClaimRef, shareRef()), noError)

	st.testGet(fmt.Sprintf("%s?via=%s,%s", contentRef, shareRef(), evilClaimRef), viaChainInvalidLink)
	if !st.slept() {
		t.Error("expected sleep after miss")
	}
}

// TODO(aa): test the "assemble" mode too.
