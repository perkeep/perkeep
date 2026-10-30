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
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
	"perkeep.org/pkg/schema/nodeattr"
)

// newTestHost returns an importer host whose HTTP requests go to the
// fake LiveJournal s.
func newTestHost(t *testing.T, s *ljtest.Server) *importer.Host {
	return imptest.NewMemoryHost(t, importer.HostConfig{HTTPClient: s.HTTPClient()})
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

// TestLinkImporterAccount checks that the importer account that
// pk livejournal makes for a journal it keeps (with
// lj.LinkImporterAccount, as clients can't use package importer) is one
// that the importer pages list, with the journal in its root.
func TestLinkImporterAccount(t *testing.T) {
	if lj.AcctAttrUserName != importer.AcctAttrUserName {
		t.Errorf("lj.AcctAttrUserName = %q; want %q", lj.AcctAttrUserName, importer.AcctAttrUserName)
	}
	s := ljtest.New(t)
	h := newTestHost(t, s)
	ctx := context.Background()
	journal := blob.RefFromString("journal")
	acct, changed, err := lj.LinkImporterAccount(ctx, hostStore{h}, "brad", "UTC", journal)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("making the account changed nothing")
	}
	// Doing it again finds the same account, and changes nothing.
	again, changed, err := lj.LinkImporterAccount(ctx, hostStore{h}, "brad", "UTC", journal)
	if err != nil {
		t.Fatal(err)
	}
	if again != acct || changed {
		t.Errorf("second link = %v, changed %v; want %v, unchanged", again, changed, acct)
	}

	req := httptest.NewRequest("GET", "/importer/livejournal", nil)
	req.Header.Set(httputil.PathSuffixHeader, "livejournal")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), acct.String()) {
		t.Fatalf("importer page doesn't list the account %v:\n%s", acct, w.Body)
	}
	res, err := h.Searcher().Describe(ctx, &search.DescribeRequest{BlobRef: acct})
	if err != nil {
		t.Fatal(err)
	}
	root, ok := blob.Parse(res.Meta.Get(acct).Permanode.Attr.Get("importRoot"))
	if !ok {
		t.Fatalf("account has no import root: %v", res.Meta.Get(acct).Permanode.Attr)
	}
	res, err = h.Searcher().Describe(ctx, &search.DescribeRequest{BlobRef: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Meta.Get(root).Permanode.Attr.Get("camliPath:" + lj.RootPathJournal); got != journal.String() {
		t.Errorf("root's journal = %q; want %v", got, journal)
	}
}
