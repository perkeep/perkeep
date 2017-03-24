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
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/jsonsign"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/test"
)

type shareTester struct {
	t          *testing.T
	sto        *test.Fetcher
	signer     *schema.Signer
	handler    *shareHandler
	sleeps     int
	rec        *httptest.ResponseRecorder
	restoreLog func()
}

// newSigner returns the armored public key of the newly created signer as well,
// so we can upload it to the index.
func newSigner(t *testing.T) (*schema.Signer, string) {
	ent, err := jsonsign.NewEntity()
	if err != nil {
		t.Fatal(err)
	}
	armorPub, err := jsonsign.ArmoredPublicKey(ent)
	if err != nil {
		t.Fatal(err)
	}
	pubRef := blob.SHA1FromString(armorPub)
	sig, err := schema.NewSigner(pubRef, strings.NewReader(armorPub), ent)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return sig, armorPub
}

func newShareTester(t *testing.T) *shareTester {
	return newShareTesterIdx(t, false)
}

func newShareTesterIdx(t *testing.T, withIndex bool) *shareTester {
	sto := new(test.Fetcher)
	var idx *index.Index
	var sig *schema.Signer
	var armorPub string
	if withIndex {
		idx = index.NewMemoryIndex()
		idx.InitBlobSource(sto)
		if _, err := idx.KeepInMemory(); err != nil {
			t.Fatal(err)
		}
		sig, armorPub = newSigner(t)
	}
	st := &shareTester{
		t:          t,
		sto:        sto,
		signer:     sig,
		handler:    &shareHandler{fetcher: sto, idx: idx},
		restoreLog: test.TLog(t),
	}
	if withIndex {
		st.putRaw(blob.SHA1FromString(armorPub), armorPub)
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
		st.t.Fatalf("error storing %q: %v", ref, err)
	}
	if st.handler.idx != nil {
		if _, err := st.handler.idx.ReceiveBlob(ref, strings.NewReader(data)); err != nil {
			st.t.Fatalf("error indexing %q, with schema \n%q\n: %v", ref, data, err)
		}
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

	t.Logf("Checking we can get the link (file) blob via the share...")
	st.testGet(fmt.Sprintf("%s?via=%s", linkRef, shareRef()), noError)

	t.Logf("Checking we can't get the content via the non-transitive share...")
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

// TODO(mpl): try to refactor TestHandleGet*, but there are enough subtle differences to barely make it worth it

func TestHandleGetFilePartViaSharing(t *testing.T) {
	st := newShareTester(t)
	defer st.done()

	content1 := "monkey" // part1
	contentRef1 := blob.SHA1FromString(content1)
	content2 := "banana" // part2
	contentRef2 := blob.SHA1FromString(content2)

	link := fmt.Sprintf(`{"camliVersion": 1,
"camliType": "file",
"parts": [
   {"blobRef": "%v", "size": %d},
   {"blobRef": "%v", "size": %d}
]}`, contentRef1, len(content1), contentRef2, len(content2))
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
	st.testGet(fmt.Sprintf("%s?via=%s", contentRef2, shareRef()), shareTargetInvalid)

	t.Logf("Checking we can't get the link (file) blob directly...")
	st.putRaw(linkRef, link)
	st.testGet(linkRef.String(), shareBlobInvalid)

	t.Logf("Checking we can get the link (file) blob via the share...")
	st.testGet(fmt.Sprintf("%s?via=%s", linkRef, shareRef()), noError)

	t.Logf("Checking we can't get the content via the non-transitive share...")
	st.testGet(fmt.Sprintf("%s?via=%s,%s", contentRef2, shareRef(), linkRef), shareNotTransitive)

	// TODO: new test?
	share.SetShareIsTransitive(true)
	st.put(share.Blob())
	st.testGet(fmt.Sprintf("%s?via=%s,%s", linkRef, shareRef(), linkRef), viaChainInvalidLink)

	st.putRaw(contentRef2, content2)
	st.testGet(fmt.Sprintf("%s?via=%s,%s", contentRef2, shareRef(), linkRef), noError)

	// new test?
	share.SetShareExpiration(time.Now().Add(-time.Duration(10) * time.Minute))
	st.put(share.Blob())
	st.testGet(fmt.Sprintf("%s?via=%s,%s", contentRef2, shareRef(), linkRef), shareExpired)

	share.SetShareExpiration(time.Now().Add(time.Duration(10) * time.Minute))
	st.put(share.Blob())
	st.testGet(fmt.Sprintf("%s?via=%s,%s", contentRef2, shareRef(), linkRef), noError)
}

func TestHandleGetBytesPartViaSharing(t *testing.T) {
	st := newShareTester(t)
	defer st.done()

	content1 := "monkey" // part1
	contentRef1 := blob.SHA1FromString(content1)
	content2 := "banana" // part2
	contentRef2 := blob.SHA1FromString(content2)

	link2 := fmt.Sprintf(`{"camliVersion": 1,
"camliType": "bytes",
"parts": [
   {"blobRef": "%v", "size": %d},
   {"blobRef": "%v", "size": %d}
]}`, contentRef1, len(content1), contentRef2, len(content2))
	linkRef2 := blob.SHA1FromString(link2)

	link1 := fmt.Sprintf(`{"camliVersion": 1,
"camliType": "file",
"parts": [
   {"blobRef": "%v", "size": %d},
   {"bytesRef": "%v", "size": %d}
]}`, blob.SHA1FromString("irrelevant content"), len("irrelevant content"), linkRef2, len(link2))
	linkRef1 := blob.SHA1FromString(link1)

	share := schema.NewShareRef(schema.ShareHaveRef, false).
		SetShareTarget(linkRef1).
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
	st.testGet(fmt.Sprintf("%s?via=%s", contentRef2, shareRef()), shareTargetInvalid)

	t.Logf("Checking we can't get the link (file) blob directly...")
	st.putRaw(linkRef1, link1)
	st.testGet(linkRef1.String(), shareBlobInvalid)

	t.Logf("Checking we can get the link (file) blob via the share...")
	st.testGet(fmt.Sprintf("%s?via=%s", linkRef1, shareRef()), noError)

	t.Logf("Checking we can't get the deeper link (bytes) blob via the non-transitive share...")
	st.putRaw(linkRef2, link2)
	st.testGet(fmt.Sprintf("%s?via=%s,%s", linkRef2, shareRef(), linkRef1), shareNotTransitive)

	// TODO: new test?
	share.SetShareIsTransitive(true)
	st.put(share.Blob())
	st.testGet(fmt.Sprintf("%s?via=%s,%s,%s", linkRef2, shareRef(), linkRef1, linkRef2), viaChainInvalidLink)

	st.putRaw(contentRef2, content2)
	st.testGet(fmt.Sprintf("%s?via=%s,%s,%s", contentRef2, shareRef(), linkRef1, linkRef2), noError)

	// new test?
	share.SetShareExpiration(time.Now().Add(-time.Duration(10) * time.Minute))
	st.put(share.Blob())
	st.testGet(fmt.Sprintf("%s?via=%s,%s,%s", contentRef2, shareRef(), linkRef1, linkRef2), shareExpired)

	share.SetShareExpiration(time.Now().Add(time.Duration(10) * time.Minute))
	st.put(share.Blob())
	st.testGet(fmt.Sprintf("%s?via=%s,%s,%s", contentRef2, shareRef(), linkRef1, linkRef2), noError)
}

// TODO(aa): test the "assemble" mode too.

// TestHandleShareDeletion makes sure that deleting (with a delete claim) a
// share claim invalidates the sharing.
func TestHandleShareDeletion(t *testing.T) {
	st := newShareTesterIdx(t, true)
	defer st.done()

	content := "monkey" // the secret
	contentRef := blob.SHA1FromString(content)

	link := fmt.Sprintf(`{"camliVersion": 1,
"camliType": "file",
"parts": [
   {"blobRef": "%v", "size": %d}
]}`, contentRef, len(content))
	linkRef := blob.SHA1FromString(link)
	st.putRaw(contentRef, content)
	st.putRaw(linkRef, link)

	share := schema.NewShareRef(schema.ShareHaveRef, false).
		SetShareTarget(linkRef).
		SetShareIsTransitive(true)
	signed, err := share.SignAt(st.signer, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	shareRef := blob.SHA1FromString(signed)
	st.putRaw(shareRef, signed)

	// Test we can get content.
	st.testGet(fmt.Sprintf("%s?via=%s,%s", contentRef, shareRef, linkRef), noError)

	// Delete share
	deletion := schema.NewDeleteClaim(shareRef)
	signedDel, err := deletion.SignAt(st.signer, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	deleteRef := blob.SHA1FromString(signedDel)
	st.putRaw(deleteRef, signedDel)

	// Test we can't get the content anymore
	st.testGet(fmt.Sprintf("%s?via=%s,%s", contentRef, shareRef, linkRef), shareDeleted)

	// Test we can't even get the share itself anymore, just in case.
	st.testGet(fmt.Sprintf("%s", shareRef), shareDeleted)
}
