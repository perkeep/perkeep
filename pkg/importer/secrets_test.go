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

package importer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/test"
	"reflect"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/secretstore"
)

type secretsLoader struct {
	blobserver.Loader
	store *secretstore.Store
}

func (l secretsLoader) GetHandler(prefix string) (any, error) {
	if prefix == "/test-secrets/" {
		return l, nil
	}
	return l.Loader.GetHandler(prefix)
}
func (l secretsLoader) SecretStore() *secretstore.Store { return l.store }
func secretTestLoader(t *testing.T, ld blobserver.Loader) blobserver.Loader {
	t.Helper()
	s, err := secretstore.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return secretsLoader{Loader: ld, store: s}
}

type legacySecretDescriber struct {
	nodes map[blob.Ref]url.Values
	query *search.SearchQuery
}

func (d *legacySecretDescriber) Query(_ context.Context, q *search.SearchQuery) (*search.SearchResult, error) {
	d.query = q
	r := new(search.SearchResult)
	for pn := range d.nodes {
		r.Blobs = append(r.Blobs, &search.SearchResultBlob{Blob: pn})
	}
	return r, nil
}
func (d *legacySecretDescriber) Describe(_ context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error) {
	attrs, ok := d.nodes[req.BlobRef]
	if !ok {
		return nil, errors.New("unexpected node")
	}
	return &search.DescribeResponse{Meta: search.MetaMap{req.BlobRef.String(): {BlobRef: req.BlobRef, Permanode: &search.DescribedPermanode{Attr: attrs}}}}, nil
}

func TestObjectCredentialWritesNeverSigned(t *testing.T) {
	f := newObjectClaimFixture(t, map[string][]string{"title": {"old"}})
	if err := f.o.SetAttrs("oauthAccessToken", "private-access", "stravaRefreshToken", "private-refresh", "title", "new"); err != nil {
		t.Fatal(err)
	}
	for _, body := range f.target.uploads() {
		if strings.Contains(body, "private-") || strings.Contains(body, "oauthAccessToken") || strings.Contains(body, "stravaRefreshToken") {
			t.Fatal("credential appeared in signed claims")
		}
	}
	if got := f.o.Attr("oauthAccessToken"); got != "private-access" {
		t.Fatal("credential not available to importer")
	}
	if got := f.o.Attrs("stravaRefreshToken"); !reflect.DeepEqual(got, []string{"private-refresh"}) {
		t.Fatal("credential Attrs mismatch")
	}
	f.o.ForeachAttr(func(key, value string) {
		if isSecretAttribute(key) || strings.Contains(value, "private-") {
			t.Error("ForeachAttr exposed credentials")
		}
	})
	before := len(f.target.uploads())
	if err := f.o.SetAttrValues("oauthAccessToken", []string{"a", "b"}); err == nil {
		t.Fatal("ambiguous secret values accepted")
	}
	if err := f.o.SetAttrValues("oauthAccessToken", []string{"replacement"}); err != nil {
		t.Fatal(err)
	}
	if err := f.o.DelAttr("oauthAccessToken", "replacement"); err != nil {
		t.Fatal(err)
	}
	if got := f.o.Attr("oauthAccessToken"); got != "" {
		t.Fatal("secret deletion failed")
	}
	if len(f.target.uploads()) != before {
		t.Fatal("credential-only mutation signed a claim")
	}
}

func TestObjectCredentialReadsObserveStoreUpdates(t *testing.T) {
	f := newObjectClaimFixture(t, map[string][]string{"oauthAccessToken": {"legacy-do-not-use"}})
	if f.o.Attr("oauthAccessToken") != "" {
		t.Fatal("read fell back to legacy PN credential")
	}
	name := f.o.secretName("oauthAccessToken")
	m, err := f.o.h.secrets.Put(context.Background(), name, "first", secretstore.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f.o.Attr("oauthAccessToken") != "first" {
		t.Fatal("object missed newly added secret")
	}
	m, err = f.o.h.secrets.Put(context.Background(), name, "second", secretstore.PutOptions{ExpectedRevision: m.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if f.o.Attr("oauthAccessToken") != "second" {
		t.Fatal("object retained stale credential")
	}
	if err = f.o.h.secrets.Delete(context.Background(), name, m.Revision); err != nil {
		t.Fatal(err)
	}
	if f.o.Attr("oauthAccessToken") != "" {
		t.Fatal("object retained deleted credential")
	}
	if err = f.o.h.secrets.Close(); err != nil {
		t.Fatal(err)
	}
	if f.o.Attr("oauthAccessToken") != "" || !errors.Is(f.o.CredentialError(), secretstore.ErrClosed) {
		t.Fatal("credential reads did not fail closed/report error")
	}
	if err = f.o.SetAttrs("oauthAccessToken", "never-sign", "title", "must-not-change"); !errors.Is(err, secretstore.ErrClosed) {
		t.Fatalf("closed store write = %v", err)
	}
	if len(f.target.uploads()) != 0 {
		t.Fatal("failed credential write uploaded claims")
	}
}

func TestFeedURLIsOrdinaryAccountMetadata(t *testing.T) {
	const feedURL = "https://feed.example/news.rss?format=full"
	f := newObjectClaimFixture(t, nil)
	if err := f.o.SetAttr("feedURL", feedURL); err != nil {
		t.Fatal(err)
	}
	f.wantClaims(t, objectClaimMutation{schema.SetAttributeClaim, "feedURL", feedURL})
	if got := f.o.Attr("feedURL"); got != feedURL {
		t.Fatalf("feed URL = %q; want %q", got, feedURL)
	}
	found := false
	f.o.ForeachAttr(func(key, value string) {
		if key == "feedURL" && value == feedURL {
			found = true
		}
	})
	if !found {
		t.Fatal("ordinary attribute enumeration omitted feed URL")
	}
	entries, err := f.o.h.secrets.List(context.Background())
	if err != nil || len(entries) != 0 {
		t.Fatalf("feed URL entered secret storage: %v, %v", entries, err)
	}
}

func TestCredentialMigrationLeavesFeedURLs(t *testing.T) {
	ctx := context.Background()
	const feedURL = "https://feed.example/news.rss?format=full"
	for _, withToken := range []bool{false, true} {
		name := "public feed"
		if withToken {
			name = "feed with separate credential"
		}
		t.Run(name, func(t *testing.T) {
			f := newObjectClaimFixture(t, nil)
			attrs := url.Values{
				attrNodeType: {nodeTypeImporterAccount}, attrImporterType: {"feed"},
				"feedURL": {feedURL},
			}
			if withToken {
				attrs["oauthAccessToken"] = []string{"separate-token"}
			}
			f.o.h.search = &legacySecretDescriber{nodes: map[blob.Ref]url.Values{f.o.pn: attrs}}
			if err := f.o.h.migrateCredentials(ctx); err != nil {
				t.Fatal(err)
			}
			for _, body := range f.target.uploads() {
				if strings.Contains(body, "feedURL") || strings.Contains(body, feedURL) {
					t.Fatal("migration changed feed URL metadata")
				}
			}
			loaded, err := f.o.h.ObjectFromRef(f.o.pn)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Attr("feedURL") != feedURL || loaded.MigratedFromPermanode() != withToken {
				t.Fatal("migration changed feed metadata or marked URL as exposed")
			}
			entries, err := f.o.h.secrets.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if withToken {
				if len(entries) != 1 || entries[0].Name != f.o.secretName("oauthAccessToken") {
					t.Fatal("migration did not separate credential from feed URL")
				}
			} else if len(entries) != 0 || len(f.target.uploads()) != 0 {
				t.Fatal("public feed created migrated credentials or cleanup claims")
			}
		})
	}
}

func TestCredentialMigrationDurableAndResumable(t *testing.T) {
	ctx := context.Background()
	f := newObjectClaimFixture(t, nil)
	file := filepath.Join(t.TempDir(), "private", "credentials.sqlite")
	s, err := secretstore.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	f.o.h.secrets = s
	pn := f.o.pn
	d := &legacySecretDescriber{nodes: map[blob.Ref]url.Values{pn: {attrNodeType: {nodeTypeImporterAccount}, attrImporterType: {"disabled-or-unregistered"}, "oauthAccessToken": {"working-access"}, "stravaRefreshToken": {"working-refresh"}, "userID": {"public-user"}}}}
	f.o.h.search = d
	f.target.err = errors.New("injected claim cleanup failure")
	if err := f.o.h.migrateCredentials(ctx); err == nil {
		t.Fatal("migration ignored cleanup failure")
	}
	entry, err := s.Get(ctx, f.o.secretName("oauthAccessToken"))
	if err != nil || entry.Value != "working-access" || !entry.MigratedFromPermanode {
		t.Fatal("migration did not save credentials before cleanup")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = secretstore.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	f.o.h.secrets = s
	// A replacement/deletion after the partial migration must survive resume.
	if _, err := s.Put(ctx, f.o.secretName("oauthAccessToken"), "newer-access", secretstore.PutOptions{ExpectedRevision: entry.Revision}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, f.o.secretName("stravaRefreshToken"), secretstore.AnyRevision); err != nil {
		t.Fatal(err)
	}
	f.target.err = nil
	if err := f.o.h.migrateCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.o.Attr("oauthAccessToken"); got != "newer-access" {
		t.Fatal("migration overwrote replacement")
	}
	if got := f.o.Attr("stravaRefreshToken"); got != "" {
		t.Fatal("migration resurrected deleted secret")
	}
	for _, body := range f.target.uploads() {
		if strings.Contains(body, "working-") || strings.Contains(body, "newer-") {
			t.Fatal("migration cleanup repeated a secret in a signed claim")
		}
	}
	loaded, err := f.o.h.ObjectFromRef(pn)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Attr("userID") != "public-user" || loaded.Attr("oauthAccessToken") != "newer-access" || !loaded.MigratedFromPermanode() {
		t.Fatal("loaded account lost public metadata, usable credential, or migration warning")
	}
	if _, ok := loaded.attr["oauthAccessToken"]; ok {
		t.Fatal("loaded object retained PN credential")
	}
	before, err := s.Get(ctx, f.o.secretName("oauthAccessToken"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.o.h.migrateCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(ctx, f.o.secretName("oauthAccessToken"))
	if err != nil || before.Revision != after.Revision {
		t.Fatal("migration not idempotent")
	}
	if d.query.Expression != "" || d.query.Limit != -1 {
		t.Fatal("migration used capped/expression query")
	}
	var types []string
	var walk func(*search.Constraint)
	walk = func(c *search.Constraint) {
		if c.Permanode != nil {
			if c.Permanode.SkipHidden {
				t.Error("migration skips hidden nodes")
			}
			types = append(types, c.Permanode.Value)
		}
		if c.Logical != nil {
			walk(c.Logical.A)
			walk(c.Logical.B)
		}
	}
	walk(d.query.Constraint)
	if !reflect.DeepEqual(types, []string{nodeTypeImporter, nodeTypeImporterAccount, nodeTypeImporterAccount + "-deleted"}) {
		t.Fatal("migration missed node types")
	}
}

func TestCredentialMigrationHiddenDeletedAndFutureClaims(t *testing.T) {
	ctx := context.Background()
	f := newObjectClaimFixture(t, nil)
	ix := index.NewMemoryIndex()
	t.Cleanup(func() { ix.Close() })
	ix.InitBlobSource(&f.target.Storage)
	corpus, err := ix.KeepInMemory()
	if err != nil {
		t.Fatal(err)
	}
	sh := search.NewHandler(ix, index.NewOwner(f.o.h.signer.KeyIDLong(), f.pubref))
	sh.SetCorpus(corpus)
	f.o.h.search = sh
	f.target.before = func(body string) {
		if _, err := blobserver.ReceiveString(ctx, ix, body); err != nil {
			t.Error(err)
		}
	}
	future := time.Now().Add(48 * time.Hour).UTC()
	var nodes []blob.Ref
	for _, typ := range []string{nodeTypeImporter, nodeTypeImporterAccount, nodeTypeImporterAccount + "-deleted"} {
		pn, err := f.o.h.upload(ctx, schema.NewUnsignedPermanode())
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, pn)
		for key, value := range map[string]string{attrNodeType: typ, attrImporterType: "removed-or-unregistered", "camliHidden": "true", "oauthToken": "legacy-future-secret", "userID": "keep-public"} {
			if _, err := f.o.h.uploadAt(ctx, schema.NewSetAttributeClaim(pn, key, value), future); err != nil {
				t.Fatal(err)
			}
		}
	}
	before := len(f.target.uploads())
	if err := f.o.h.migrateCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(f.target.uploads()) - before; got != len(nodes) {
		t.Fatalf("cleanup uploaded %d claims; want %d", got, len(nodes))
	}
	for _, pn := range nodes {
		res, err := sh.Describe(ctx, &search.DescribeRequest{BlobRef: pn, Depth: 1})
		if err != nil {
			t.Fatal(err)
		}
		attrs := res.Meta[pn.String()].Permanode.Attr
		if attrs.Get("oauthToken") != "" {
			t.Fatal("future-dated legacy secret survived cleanup")
		}
		if attrs.Get("userID") != "keep-public" {
			t.Fatal("migration modified identity")
		}
		o, err := f.o.h.ObjectFromRef(pn)
		if err != nil {
			t.Fatal(err)
		}
		if o.Attr("oauthToken") != "legacy-future-secret" || !o.MigratedFromPermanode() {
			t.Fatal("hidden/deleted/unknown account not migrated")
		}
	}
	for _, body := range f.target.uploads()[before:] {
		if strings.Contains(body, "legacy-future-secret") {
			t.Fatal("cleanup claim contains credential")
		}
		b, err := schema.BlobFromReader(blob.RefFromString(body), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		date, err := b.ClaimDate()
		if err != nil || !date.After(future) {
			t.Fatal("migration cleanup did not follow future claims")
		}
	}
	before = len(f.target.uploads())
	if err := f.o.h.migrateCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.target.uploads()) != before {
		t.Fatal("completed migration uploaded redundant cleanup claims")
	}
}

func TestCredentialWarningsAndBlankForms(t *testing.T) {
	f := newObjectClaimFixture(t, nil)
	im := f.o.h.imp["dummy1"]
	props := *im.props
	props.NeedsAPIKey = true
	props.TODOIssue = 0
	im.props = &props
	im.nodeCache = f.o
	f.o.h.search = &legacySecretDescriber{nodes: map[blob.Ref]url.Values{f.o.pn: {}}}
	if err := f.o.SetAttrs(attrClientID, "private-client-id", attrClientSecret, "private-client-secret"); err != nil {
		t.Fatal(err)
	}
	if err := f.o.h.secrets.Migrate(context.Background(), []secretstore.LegacyEntry{{Name: f.o.secretName(attrClientSecret), Value: "private-client-secret"}}); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	if err := f.o.h.tmpl.ExecuteTemplate(&body, "importerPage", importerPage{Body: importerBody{Host: f.o.h, Importer: im}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), "⚠️") || !strings.Contains(body.String(), "historical signed blobs") {
		t.Fatal("migration warning absent")
	}
	if strings.Contains(body.String(), "private-client") {
		t.Fatal("credential form prefilled stored values")
	}
	ia := &importerAcct{im: im, acct: f.o}
	for name, data := range map[string]any{
		"accountsTable": accountsTableData{Rows: []accountRow{{Acct: ia, Importer: im}}},
		"acctPage":      acctPage{Body: acctBody{Acct: ia, Importer: im}},
	} {
		var out strings.Builder
		if err := f.o.h.tmpl.ExecuteTemplate(&out, name, data); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "⚠️") || strings.Contains(out.String(), "private-client") {
			t.Fatal("account warning absent or exposed secret")
		}
	}
	post := func(form url.Values, origin string, site string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://example.test/importer/dummy1", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		w := httptest.NewRecorder()
		f.o.h.serveImporterPost(w, r, im)
		return w
	}
	values := url.Values{"mode": {"saveclientidsecret"}, "clientID": {""}, "clientSecret": {""}}
	if w := post(values, "http://example.test", "same-origin"); w.Code != http.StatusFound {
		t.Fatal(w.Code, w.Body.String())
	}
	if f.o.Attr(attrClientSecret) != "private-client-secret" || f.o.Attr(attrClientID) != "private-client-id" {
		t.Fatal("blank form removed credentials")
	}
	values.Set("clientSecret", "new-secret")
	if w := post(values, "https://evil.test", "cross-site"); w.Code != http.StatusForbidden {
		t.Fatal("cross-site credential mutation accepted")
	}
	if f.o.Attr(attrClientSecret) != "private-client-secret" {
		t.Fatal("cross-site request changed credentials")
	}
	if w := post(values, "", "none"); w.Code != http.StatusFound {
		t.Fatal(w.Code)
	}
	if !f.o.MigratedFromPermanode() {
		t.Fatal("ordinary secret update cleared migration warning")
	}
	values.Set("clearCredentials", "true")
	if w := post(values, "http://example.test", ""); w.Code != http.StatusFound {
		t.Fatal(w.Code)
	}
	if f.o.Attr(attrClientSecret) != "" || f.o.Attr(attrClientID) != "" {
		t.Fatal("explicit clear failed")
	}
}

func TestImporterMutationOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin, site, base string
		want               bool
	}{
		{"", "", "", true}, {"http://example.test", "same-origin", "", true},
		{"https://public.test", "same-origin", "https://public.test/perkeep", true},
		{"https://evil.test", "", "", false}, {"null", "", "", false},
		{"http://example.test/", "", "", false}, {"http://example.test", "cross-site", "", false},
		{"http://example.test", "same-site", "", false}, {"https://forwarded.test", "", "", false},
	} {
		r := httptest.NewRequest("POST", "http://example.test/importer/", nil)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		if tc.site != "" {
			r.Header.Set("Sec-Fetch-Site", tc.site)
		}
		r.Header.Set("X-Forwarded-Host", "forwarded.test")
		r.Header.Set("X-Forwarded-Proto", "https")
		if got := importerMutationOrigin(r, tc.base) == nil; got != tc.want {
			t.Errorf("origin %q site %q base %q allowed=%v", tc.origin, tc.site, tc.base, got)
		}
	}
	if _, err := newFromConfig(test.NewLoader(), map[string]any{}); err == nil {
		t.Fatal("production constructor allowed missing secretRoot")
	}
}

func TestImporterCredentialSetupRequiresOwner(t *testing.T) {
	auth.SetMode(auth.NewBasicAuth("owner", "owner-password"))
	t.Cleanup(func() { auth.SetMode(auth.None{}) })
	f := newObjectClaimFixture(t, nil)
	h := f.o.h
	h.requireOwnerForSetup = true
	im := h.imp["dummy1"]
	im.nodeCache = f.o
	ia := &importerAcct{im: im, acct: f.o}
	if err := f.o.SetAttrs(attrClientID, "existing-id", attrClientSecret, "existing-secret"); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []bool{false, true} {
		r := httptest.NewRequest("POST", "http://example.test/importer/dummy1", strings.NewReader(url.Values{"mode": {"saveclientidsecret"}, "clientSecret": {"replacement"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if owner {
			r.SetBasicAuth("owner", "owner-password")
		} else {
			r.Header.Set("Authorization", "Token "+auth.DiscoveryToken())
		}
		w := httptest.NewRecorder()
		h.serveImporterPost(w, r, im)
		if owner {
			if w.Code != http.StatusFound || f.o.Attr(attrClientSecret) != "replacement" {
				t.Fatal("owner credential write denied")
			}
		} else {
			if w.Code != http.StatusForbidden || f.o.Attr(attrClientSecret) != "existing-secret" {
				t.Fatal("discovery token modified credentials")
			}
			if strings.Contains(w.Body.String(), "owner-password") {
				t.Fatal("auth denial leaked owner password")
			}
			for name, serve := range map[string]func(http.ResponseWriter, *http.Request){
				"new account": func(w http.ResponseWriter, r *http.Request) { h.serveNewAccount(w, r, im) },
				"callback":    func(w http.ResponseWriter, r *http.Request) { h.serveImporterAcctCallback(w, r, im) },
				"setup":       ia.setup,
			} {
				out := httptest.NewRecorder()
				serve(out, r)
				if out.Code != http.StatusForbidden {
					t.Errorf("%s allowed discovery-token setup", name)
				}
			}
		}
	}
}

func TestAllKnownCredentialAttributesStayLocal(t *testing.T) {
	f := newObjectClaimFixture(t, nil)
	var attrs []string
	for _, key := range secretAttributes {
		attrs = append(attrs, key, "private-value-for-"+key)
	}
	if err := f.o.SetAttrs(attrs...); err != nil {
		t.Fatal(err)
	}
	if len(f.target.uploads()) != 0 {
		t.Fatal("credential-only batch uploaded a claim")
	}
	for _, key := range secretAttributes {
		if f.o.Attr(key) != "private-value-for-"+key {
			t.Errorf("credential %s not available", key)
		}
		if _, ok := f.o.attr[key]; ok {
			t.Errorf("credential %s present in public attribute map", key)
		}
	}
	entries, err := f.o.h.secrets.List(context.Background())
	if err != nil || len(entries) != len(secretAttributes) {
		t.Fatal("credential batch missing entries")
	}
	if err := f.o.SetAttrsWithDeletes(secretAttributes); err != nil {
		t.Fatal(err)
	}
	for _, key := range secretAttributes {
		if f.o.Attr(key) != "" {
			t.Errorf("credential %s not deleted", key)
		}
	}
	if len(f.target.uploads()) != 0 {
		t.Fatal("credential deletion signed a claim")
	}
}

func TestCredentialMigrationFailuresDoNotScrubLegacy(t *testing.T) {
	for _, mode := range []string{"store closed", "ambiguous values"} {
		t.Run(mode, func(t *testing.T) {
			f := newObjectClaimFixture(t, nil)
			values := []string{"still-working"}
			if mode == "ambiguous values" {
				values = append(values, "also-working")
			}
			f.o.h.search = &legacySecretDescriber{nodes: map[blob.Ref]url.Values{f.o.pn: {attrNodeType: {nodeTypeImporterAccount}, "oauthAccessToken": values}}}
			if mode == "store closed" {
				f.o.h.secrets.Close()
			}
			if err := f.o.h.migrateCredentials(context.Background()); err == nil {
				t.Fatal("migration failure ignored")
			}
			if len(f.target.uploads()) != 0 {
				t.Fatal("failed migration removed legacy credentials")
			}
			if mode == "store closed" {
				if _, err := f.o.h.ObjectFromRef(f.o.pn); !errors.Is(err, secretstore.ErrClosed) {
					t.Fatal("ObjectFromRef silently ignored unavailable credential store")
				}
			}
		})
	}
}

func TestMastodonCredentialDestinationMigratesWithTokens(t *testing.T) {
	f := newObjectClaimFixture(t, nil)
	pn := f.o.pn
	f.o.h.search = &legacySecretDescriber{nodes: map[blob.Ref]url.Values{pn: {attrNodeType: {nodeTypeImporterAccount}, attrImporterType: {"mastodon"}, "instanceURL": {"https://authorized.example"}, "oauthAccessToken": {"private-token"}, "oauthClientID": {"client-id"}, "oauthClientSecret": {"client-secret"}}}}
	if err := f.o.h.migrateCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"instanceURL": "https://authorized.example", "oauthAccessToken": "private-token", "oauthClientID": "client-id", "oauthClientSecret": "client-secret"} {
		e, err := f.o.h.secrets.Get(context.Background(), f.o.secretName(key))
		if err != nil || e.Value != value || !e.MigratedFromPermanode {
			t.Errorf("Mastodon credential binding %s not migrated", key)
		}
	}
	for _, body := range f.target.uploads() {
		if strings.Contains(body, "authorized.example") || strings.Contains(body, "private-token") || strings.Contains(body, "client-secret") {
			t.Fatal("migration repeated Mastodon credentials or destination in claim")
		}
	}
}

type callbackTransportImporter struct {
	Importer
	calls int
}

func (i *callbackTransportImporter) CallbackRequestAccount(r *http.Request) (blob.Ref, error) {
	return (OAuth1{}).CallbackRequestAccount(r)
}
func (i *callbackTransportImporter) ServeCallback(w http.ResponseWriter, r *http.Request, c *SetupContext) {
	i.calls++
	w.WriteHeader(http.StatusNoContent)
}

func TestCredentialFormCallbacksRequirePOST(t *testing.T) {
	f := newObjectClaimFixture(t, nil)
	im := f.o.h.imp["dummy1"]
	impl := &callbackTransportImporter{Importer: TODOImporter}
	im.impl = impl
	im.acct = map[blob.Ref]*importerAcct{f.o.pn: {im: im, acct: f.o}}
	for _, tc := range []struct {
		postOnly       bool
		method, origin string
		code           int
	}{
		{true, "GET", "", http.StatusMethodNotAllowed},
		{true, "POST", "http://example.test", http.StatusNoContent},
		{true, "POST", "https://evil.test", http.StatusForbidden},
		{false, "GET", "", http.StatusNoContent},
		{false, "POST", "http://example.test", http.StatusMethodNotAllowed},
	} {
		props := *im.props
		props.CallbackRequiresPOST = tc.postOnly
		im.props = &props
		r := httptest.NewRequest(tc.method, "http://example.test/importer/dummy1/callback?acct="+f.o.pn.String(), nil)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		f.o.h.serveImporterAcctCallback(w, r, im)
		if w.Code != tc.code {
			t.Errorf("postOnly %v %s origin %s code=%d want %d", tc.postOnly, tc.method, tc.origin, w.Code, tc.code)
		}
	}
	if impl.calls != 2 {
		t.Fatal("invalid callback method/origin reached credential handler")
	}
}
