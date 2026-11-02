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

package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/upload"
)

func TestPermanodes(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t, t.TempDir())
	s.testClient = newTestPerkeep(t, nil)
	var a Account
	do(t, s, "POST", "/accounts", accountFields{Server: "example.com"}, http.StatusCreated, &a)
	path := "/accounts/" + a.ID
	do(t, s, "POST", path+"/connect", map[string]string{}, http.StatusOK, nil)
	ac, err := s.conn(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	// hashOf hashes a file, as the app has the core do.
	hashOf := func(key, p string) []string {
		t.Helper()
		var res struct{ Refs []string }
		do(t, s, "POST", path+"/hashes/file", map[string]string{"key": key, "path": p}, http.StatusOK, &res)
		return res.Refs
	}
	present := func(key string, refs []string) bool {
		t.Helper()
		var res struct {
			Present, Missing []string
			Error            string
		}
		do(t, s, "POST", path+"/findmissing", map[string]any{"items": []map[string]any{{"key": key, "refs": refs}}}, http.StatusOK, &res)
		if res.Error != "" {
			t.Fatalf("findmissing: %v", res.Error)
		}
		return len(res.Present) == 1
	}
	uploadViaCore := func(key, p string, refs []string, modTime time.Time) (fileRef string) {
		t.Helper()
		do(t, s, "POST", path+"/uploads", map[string]any{"key": key, "path": p, "modTime": modTime, "refs": refs, "noDupCheck": true}, http.StatusOK, nil)
		ac.running.Wait()
		var st uploadsStatus
		do(t, s, "GET", path+"/uploads", nil, http.StatusOK, &st)
		if len(st.Done) != 1 || st.Done[0].Error != "" {
			t.Fatalf("upload: %+v", st.Done)
		}
		return st.Done[0].FileRef
	}
	// uploadBare uploads a file without a permanode, as an older app did.
	uploadBare := func(p string) blob.Ref {
		t.Helper()
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fi, _ := f.Stat()
		res, err := upload.NewFile(ac.c, fi, f, nil).Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return res.FileRef
	}
	permanodesOf := func(refs []string) int {
		t.Helper()
		sr, err := ac.c.SearchRoot()
		if err != nil {
			t.Fatal(err)
		}
		var res struct{ Permanodes map[string][]string }
		if err := ac.c.GetJSON(ctx, sr+"camli/search/contentpermanodes?wholedigest="+refs[0], &res); err != nil {
			t.Fatal(err)
		}
		return len(res.Permanodes[refs[0]])
	}
	camliContent := func(pn blob.Ref) []string {
		t.Helper()
		sr, _ := ac.c.SearchRoot()
		var res struct {
			Meta map[string]struct {
				Permanode struct {
					Attr map[string][]string `json:"attr"`
				} `json:"permanode"`
			} `json:"meta"`
		}
		if err := ac.c.GetJSON(ctx, sr+"camli/search/describe?blobref="+pn.String(), &res); err != nil {
			t.Fatal(err)
		}
		return res.Meta[pn.String()].Permanode.Attr["camliContent"]
	}

	// A file on the server without a permanode isn't present; uploading
	// it makes the permanode that pk-put and takeout would.
	bare := writeRandomFile(t, dir, "bare.jpg", 100<<10)
	bareRefs := hashOf("bare", bare)
	uploadBare(bare)
	if present("bare", bareRefs) {
		t.Fatal("file without a permanode is present")
	}
	modTime := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	bareFile := uploadViaCore("bare", bare, bareRefs, modTime)
	if !present("bare", bareRefs) {
		t.Fatal("not present after uploading")
	}
	local := plannedPermanodeOfTestKey(t, bareRefs)
	pn := blob.RefFromString(local)
	if got := camliContent(pn); len(got) != 1 || got[0] != bareFile {
		t.Errorf("planned permanode %v's camliContent = %v; want the file, %v", pn, got, bareFile)
	}

	// Uploading it again (as from another phone, without the cache)
	// doesn't make another permanode or claim.
	removeHaveFiles(filepath.Dir(s.dataPath(".")), a.ID, "")
	do(t, s, "POST", path+"/connect", map[string]string{}, http.StatusOK, nil)
	ac, _ = s.conn(a.ID)
	uploadViaCore("bare", bare, bareRefs, modTime.Add(time.Hour))
	if n := permanodesOf(bareRefs); n != 1 {
		t.Errorf("%d permanodes after uploading again; want 1", n)
	}
	var claims struct{ Claims []any }
	sr, _ := ac.c.SearchRoot()
	if err := ac.c.GetJSON(ctx, sr+"camli/search/claims?permanode="+pn.String(), &claims); err != nil {
		t.Fatal(err)
	}
	if len(claims.Claims) != 1 {
		t.Errorf("%d claims on the permanode after uploading again; want 1", len(claims.Claims))
	}

	// Contents with another permanode (as the web UI makes) are present,
	// and uploading them doesn't make a planned one.
	other := writeRandomFile(t, dir, "other.jpg", 50<<10)
	otherRefs := hashOf("other", other)
	otherFile := uploadBare(other)
	randomPN, err := ac.sign(ctx, schema.NewUnsignedPermanode(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cl := schema.NewSetAttributeClaim(blob.RefFromString(randomPN), "camliContent", otherFile.String())
	signedClaim, err := ac.sign(ctx, cl, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{randomPN, signedClaim} {
		if _, err := ac.c.Upload(ctx, client.NewUploadHandleFromString(b)); err != nil {
			t.Fatal(err)
		}
	}
	if !present("other", otherRefs) {
		t.Fatal("contents with a random permanode aren't present")
	}
	uploadViaCore("other", other, otherRefs, modTime)
	if n := permanodesOf(otherRefs); n != 1 {
		t.Errorf("%d permanodes of contents that had one, after uploading; want 1", n)
	}
}

// plannedPermanodeOfTestKey returns the planned permanode of the contents
// with the whole-file refs, signed with the test key, as pk-put
// -filenodes and the takeout importer sign it.
func plannedPermanodeOfTestKey(t *testing.T, refs []string) string {
	t.Helper()
	root, err := osutil.PkSourceRoot()
	if err != nil {
		t.Fatal(err)
	}
	ent, err := jsonsign.EntityFromSecring("26F5ABDA", filepath.Join(root, "pkg/jsonsign/testdata/test-secring.gpg"))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := jsonsign.ArmoredPublicKey(ent)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := schema.NewSigner(blob.RefFromString(pub), strings.NewReader(pub), ent)
	if err != nil {
		t.Fatal(err)
	}
	var key string
	for _, r := range refs {
		if strings.HasPrefix(r, blob.RefFromHash(blob.NewHash()).HashName()+"-") {
			key = r
		}
	}
	pn, err := schema.NewPlannedPermanode(key).SignAt(context.Background(), signer, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return pn
}
