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

package livejournal

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/internal/httputil"
	lj "perkeep.org/internal/livejournal"
	"perkeep.org/internal/livejournal/ljtest"
	search "perkeep.org/internal/searchtypes"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
	pksearch "perkeep.org/pkg/search"
)

// indexingStorage is memory storage that also indexes what it receives.
type indexingStorage struct {
	*memory.Storage
	ix *index.Index
}

func (s indexingStorage) ReceiveBlob(ctx context.Context, br blob.Ref, r io.Reader) (blob.SizedRef, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return blob.SizedRef{}, err
	}
	sb, err := s.Storage.ReceiveBlob(ctx, br, strings.NewReader(string(b)))
	if err != nil {
		return sb, err
	}
	_, err = s.ix.ReceiveBlob(ctx, br, strings.NewReader(string(b)))
	return sb, err
}

// newTestHost returns an importer host on memory storage, with an
// in-memory index, whose HTTP requests go to the fake LiveJournal s.
func newTestHost(t *testing.T, s *ljtest.Server) *importer.Host {
	t.Helper()
	ctx := context.Background()
	entity, err := jsonsign.EntityFromSecring("26F5ABDA", "../../jsonsign/testdata/test-secring.gpg")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := jsonsign.ArmoredPublicKey(entity)
	if err != nil {
		t.Fatal(err)
	}
	pubRef := blob.RefFromString(pub)
	signer, err := schema.NewSigner(pubRef, strings.NewReader(pub), entity)
	if err != nil {
		t.Fatal(err)
	}
	mem := new(memory.Storage)
	ix := index.NewMemoryIndex()
	t.Cleanup(func() { ix.Close() })
	ix.InitBlobSource(mem)
	corpus, err := ix.KeepInMemory()
	if err != nil {
		t.Fatal(err)
	}
	st := indexingStorage{mem, ix}
	if _, err := st.ReceiveBlob(ctx, pubRef, strings.NewReader(pub)); err != nil {
		t.Fatal(err)
	}
	sh := pksearch.NewHandler(ix, index.NewOwner(signer.KeyIDLong(), pubRef))
	sh.SetCorpus(corpus)
	h, err := importer.NewHost(importer.HostConfig{
		BaseURL:    "https://perkeep.example",
		Prefix:     "/importer/",
		Target:     st,
		BlobSource: mem,
		Signer:     signer,
		Search:     sh,
		HTTPClient: s.HTTPClient(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func writeFile(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetupCallback(t *testing.T) {
	s := ljtest.New(t)
	h := newTestHost(t, s)
	rc, err := importer.CreateAccount(h, "livejournal")
	if err != nil {
		t.Fatal(err)
	}
	acct := rc.AccountNode()
	pwFile := writeFile(t, "pw", ljtest.Password+"\n")
	callback := func(form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		form.Set("acct", acct.PermanodeRef().String())
		req := httptest.NewRequest("GET", "/importer/livejournal/callback?"+form.Encode(), nil)
		req.Header.Set(httputil.PathSuffixHeader, "livejournal/callback")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	good := func() url.Values {
		return url.Values{
			"user":         {ljtest.User},
			"passwordFile": {pwFile},
			"timeZone":     {"America/Los_Angeles"},
			"server":       {ljtest.BaseURL},
		}
	}

	for name, change := range map[string]func(url.Values){
		"no user":          func(v url.Values) { v.Del("user") },
		"missing file":     func(v url.Values) { v.Set("passwordFile", filepath.Join(t.TempDir(), "nope")) },
		"wrong password":   func(v url.Values) { v.Set("passwordFile", writeFile(t, "bad", "wrong")) },
		"bad time zone":    func(v url.Values) { v.Set("timeZone", "Mars/Olympus") },
		"empty password":   func(v url.Values) { v.Set("passwordFile", writeFile(t, "empty", "\n")) },
		"no time zone":     func(v url.Values) { v.Del("timeZone") },
		"no password file": func(v url.Values) { v.Del("passwordFile") },
	} {
		form := good()
		change(form)
		if rec := callback(form); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d %q; want 400", name, rec.Code, rec.Body)
		}
	}
	if ok, _ := (imp{}).IsAccountReady(acct); ok {
		t.Fatal("account ready after only failed setups")
	}

	if rec := callback(good()); rec.Code != http.StatusFound {
		t.Fatalf("good setup: got %d %q; want a redirect", rec.Code, rec.Body)
	}
	if ok, _ := (imp{}).IsAccountReady(acct); !ok {
		t.Fatal("account not ready after setup")
	}
	if got := acct.Attr(acctAttrPasswordFile); got != pwFile {
		t.Errorf("password file attr = %q; want %q", got, pwFile)
	}
	acct.ForeachAttr(func(k, v string) {
		if strings.Contains(v, ljtest.Password) {
			t.Errorf("account attribute %s has the password", k)
		}
	})
	if s.SawPassword() {
		t.Error("the password was sent to LiveJournal")
	}
}

func TestRun(t *testing.T) {
	s := ljtest.New(t)
	h := newTestHost(t, s)
	rc, err := importer.CreateAccount(h, "livejournal")
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.AccountNode().SetAttrs(
		importer.AcctAttrUserName, ljtest.User,
		acctAttrPasswordFile, writeFile(t, "pw", ljtest.Password),
		acctAttrTimeZone, "UTC",
		acctAttrServer, ljtest.BaseURL,
	); err != nil {
		t.Fatal(err)
	}
	for run := range 2 {
		if err := (imp{}).Run(rc); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if n := s.Logins(); n != 2 {
		t.Errorf("logged in %d times in two runs; want once each", n)
	}

	root := rc.RootNode()
	if got := root.Attr(nodeattr.Title); got != "LiveJournal data for brad" {
		t.Errorf("root title = %q", got)
	}
	journalRef, ok := blob.Parse(root.Attr("camliPath:journal"))
	if !ok {
		t.Fatalf("root's camliPath:journal = %q", root.Attr("camliPath:journal"))
	}
	ctx := context.Background()
	res, err := h.Searcher().Describe(ctx, &search.DescribeRequest{BlobRef: journalRef})
	if err != nil {
		t.Fatal(err)
	}
	jm := res.Meta.Get(journalRef)
	if jm == nil || jm.Permanode == nil {
		t.Fatal("journal permanode not indexed")
	}
	members := jm.Permanode.Attr["camliMember"]
	if len(members) != len(s.Entries()) || jm.Permanode.Attr.Get(nodeattr.Type) != lj.NodeTypeJournal {
		t.Errorf("journal attrs = %v", jm.Permanode.Attr)
	}

	// The importer keeps the same permanodes as pk livejournal: the
	// planned ones of the same signer and keys.
	refs := make([]blob.Ref, len(members))
	for i, m := range members {
		refs[i], _ = blob.Parse(m)
	}
	res, err = h.Searcher().Describe(ctx, &search.DescribeRequest{BlobRefs: refs})
	if err != nil {
		t.Fatal(err)
	}
	for _, br := range refs {
		m := res.Meta.Get(br)
		if m == nil || m.Permanode == nil || m.Permanode.Attr.Get(nodeattr.Type) != lj.NodeTypeEntry || m.Permanode.Attr.Get(nodeattr.StartDate) == "" {
			t.Errorf("entry %v not indexed as one: %+v", br, m)
		}
	}
}
