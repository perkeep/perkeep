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
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/types/camtypes"
)

const archiveTestName = "archive-test"

type archiveTestImporter struct {
	Importer
	run       func(*RunContext, *Archive) error
	calls     atomic.Int32
	inspected func()
}

func (im *archiveTestImporter) InspectArchive(ctx context.Context, a *Archive) (camtypes.ArchiveIdentity, error) {
	r, err := a.OpenLimit("owner.json", 4096)
	if err != nil {
		return camtypes.ArchiveIdentity{}, err
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return camtypes.ArchiveIdentity{}, err
	}
	var id camtypes.ArchiveIdentity
	err = json.Unmarshal(data, &id)
	if im.inspected != nil {
		im.inspected()
	}
	return id, err
}
func (im *archiveTestImporter) RunArchive(rc *RunContext, a *Archive) error {
	im.calls.Add(1)
	if im.run != nil {
		return im.run(rc, a)
	}
	return nil
}
func init() { Register(archiveTestName, &archiveTestImporter{Importer: TODOImporter}) }

func archiveTestHost(t *testing.T) (*Host, *archiveTestImporter, *objectClaimReceiver) {
	t.Helper()
	f := newObjectClaimFixture(t, nil)
	h := f.o.h
	h.blobSource = f.target
	h.search = archiveLiveSearcher{h}
	im := &archiveTestImporter{Importer: TODOImporter}
	h.imp[archiveTestName].impl = im
	h.imp[archiveTestName].allLoaded = true
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	return h, im, f.target
}

type archiveLiveSearcher struct{ host *Host }

type archiveAccountLimitSearcher struct {
	search.QueryDescriber
	limit  int
	called bool
}

func (s *archiveAccountLimitSearcher) Query(ctx context.Context, q *search.SearchQuery) (*search.SearchResult, error) {
	s.limit, s.called = q.Limit, true
	return s.QueryDescriber.Query(ctx, q)
}

func TestArchiveAccountResolutionHasNoSearchLimit(t *testing.T) {
	h, _, store := archiveTestHost(t)
	h.imp[archiveTestName].allLoaded = false
	queries := &archiveAccountLimitSearcher{QueryDescriber: h.search}
	h.search = queries
	ref := archiveTestUpload(t, store, "export.zip", camtypes.ArchiveIdentity{UserName: "runner"})
	if _, err := h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "inspect", FileRef: ref}); err != nil {
		t.Fatal(err)
	}
	if !queries.called || queries.limit != -1 {
		t.Fatalf("account query called=%v limit=%d; want unlimited", queries.called, queries.limit)
	}
}

func (s archiveLiveSearcher) Query(context.Context, *search.SearchQuery) (*search.SearchResult, error) {
	return &search.SearchResult{}, nil
}

func (s archiveLiveSearcher) Describe(ctx context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error) {
	im := s.host.imp[archiveTestName]
	im.acctmu.Lock()
	var found *Object
	for _, ia := range im.acct {
		for _, o := range []*Object{ia.acct, ia.root} {
			if o.PermanodeRef() == req.BlobRef {
				found = o
			}
		}
	}
	im.acctmu.Unlock()
	if found == nil {
		return nil, fmt.Errorf("unknown live fixture object %s", req.BlobRef)
	}
	found.mu.Lock()
	attrs := make(url.Values)
	for k, v := range found.attr {
		attrs[k] = append([]string(nil), v...)
	}
	found.mu.Unlock()
	return &search.DescribeResponse{Meta: search.MetaMap{
		req.BlobRef.String(): {BlobRef: req.BlobRef, CamliType: "permanode", Permanode: &search.DescribedPermanode{Attr: attrs}},
	}}, nil
}

type archiveMember struct {
	name, data string
	mode       os.FileMode
}

func archiveZIP(t *testing.T, members ...archiveMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range members {
		hdr := &zip.FileHeader{Name: m.name, Method: zip.Deflate}
		if m.mode != 0 {
			hdr.SetMode(m.mode)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, m.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func archiveTestUpload(t *testing.T, store blobserver.StatReceiver, name string, id camtypes.ArchiveIdentity) blob.Ref {
	t.Helper()
	owner, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	data := archiveZIP(t, archiveMember{name: "owner.json", data: string(owner)}, archiveMember{name: "activities.csv", data: "id,value\n1,2\n"})
	ref, err := schema.WriteFileFromReader(context.Background(), store, name, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
func waitArchiveIdle(t *testing.T, h *Host, ref blob.Ref) *camtypes.ArchiveImportStatus {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		ia, err := h.imp[archiveTestName].account(ref)
		if err != nil {
			t.Fatal(err)
		}
		ia.mu.Lock()
		idle := ia.current == nil
		ia.mu.Unlock()
		if idle {
			s, err := h.ArchiveStatus(context.Background(), archiveTestName, ref)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
		select {
		case <-timeout.C:
			t.Fatal("archive run did not stop")
		case <-tick.C:
		}
	}
}

func TestArchiveInspectAndIdentity(t *testing.T) {
	h, im, target := archiveTestHost(t)
	id := camtypes.ArchiveIdentity{UserName: " alice ", Email: "ALICE@example.test", Name: "Alice", FilenameUserID: "123"}
	ref := archiveTestUpload(t, target, "Runkeeper-export-123.zip", id)
	before := len(target.uploads())
	result, err := h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "inspect", FileRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	if result.AccountRef.Valid() || result.Created || result.Status != nil || result.Identity.UserName != "alice" {
		t.Fatalf("inspect = %+v", result)
	}
	if im.calls.Load() != 0 || len(target.uploads()) != before {
		t.Fatal("inspection mutated storage or started importing")
	}
	if accts, _ := h.imp[archiveTestName].Accounts(); len(accts) != 0 {
		t.Fatal("inspection created an account")
	}
	result, err = h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || !result.AccountRef.Valid() {
		t.Fatalf("start = %+v", result)
	}
	status := waitArchiveIdle(t, h, result.AccountRef)
	if status.State != "complete" || status.FileRef != ref || status.Error != "" {
		t.Fatalf("status = %+v", status)
	}
	ia, _ := h.imp[archiveTestName].account(result.AccountRef)
	if ia.acct.Attr(AcctAttrArchiveInput) != ref.String() || ia.acct.Attr(AcctAttrUserID) != "" || ia.acct.Attr(AcctAttrEmail) != id.Email ||
		ia.acct.Attr(AcctAttrArchiveFilenameUserID) != "123" || ia.acct.Attr(AcctAttrArchiveFilenameUserIDSource) != ref.String() {
		t.Fatalf("account attributes = %+v", ia.acct.attr)
	}
	// Explicit replay is not skipped: new importer versions may need to
	// reconcile previously imported inputs. Only providers suppress item claims.
	result, err = h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	waitArchiveIdle(t, h, result.AccountRef)
	if result.Created || im.calls.Load() != 2 {
		t.Fatalf("replay created=%v calls=%v", result.Created, im.calls.Load())
	}
	renamed := archiveTestUpload(t, target, "renamed.zip", camtypes.ArchiveIdentity{UserName: "alice", FilenameUserID: "999"})
	result, err = h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: renamed})
	if err != nil {
		t.Fatal(err)
	}
	waitArchiveIdle(t, h, result.AccountRef)
	if ia.acct.Attr(AcctAttrArchiveFilenameUserID) != "123" || ia.acct.Attr(AcctAttrArchiveFilenameUserIDSource) != ref.String() || ia.acct.Attr(AcctAttrUserID) != "" {
		t.Fatal("renamed input replaced advisory ID or its provenance")
	}
}

func TestArchiveAccountMatching(t *testing.T) {
	account := func(label, user, email, id string) *importerAcct {
		return &importerAcct{acct: &Object{pn: blob.RefFromString(label), attr: map[string][]string{attrNodeType: {nodeTypeImporterAccount}, AcctAttrUserName: {user}, AcctAttrEmail: {email}, AcctAttrUserID: {id}}}}
	}
	a := account("a", "alice", "alice@example.test", "1")
	b := account("b", "bob", "bob@example.test", "2")
	advisory := account("hint", "bob", "bob@example.test", "")
	advisory.acct.attr[AcctAttrArchiveFilenameUserID] = []string{"2"}
	corrected := account("corrected", "bob", "bob@example.test", "3")
	corrected.acct.attr[AcctAttrArchiveFilenameUserID] = []string{"2"}
	tests := []struct {
		name     string
		id       camtypes.ArchiveIdentity
		accounts []*importerAcct
		want     *importerAcct
		conflict bool
	}{
		{name: "username", id: camtypes.ArchiveIdentity{UserName: "alice"}, accounts: []*importerAcct{a, b}, want: a},
		{name: "email case insensitive", id: camtypes.ArchiveIdentity{Email: "ALICE@example.test"}, accounts: []*importerAcct{a, b}, want: a},
		{name: "no match filename not identity", id: camtypes.ArchiveIdentity{UserName: "other", FilenameUserID: "1"}, accounts: []*importerAcct{a, b}},
		{name: "username OR email ambiguity", id: camtypes.ArchiveIdentity{UserName: "alice", Email: "bob@example.test"}, accounts: []*importerAcct{a, b}, conflict: true},
		{name: "filename breaks tie", id: camtypes.ArchiveIdentity{UserName: "alice", Email: "bob@example.test", FilenameUserID: "2"}, accounts: []*importerAcct{a, b}, want: b},
		{name: "stored advisory breaks tie", id: camtypes.ArchiveIdentity{UserName: "alice", Email: "bob@example.test", FilenameUserID: "2"}, accounts: []*importerAcct{a, advisory}, want: advisory},
		{name: "actual ID overrides stale advisory", id: camtypes.ArchiveIdentity{UserName: "alice", Email: "bob@example.test", FilenameUserID: "2"}, accounts: []*importerAcct{a, corrected}, conflict: true},
		{name: "corrected actual ID breaks tie", id: camtypes.ArchiveIdentity{UserName: "alice", Email: "bob@example.test", FilenameUserID: "3"}, accounts: []*importerAcct{a, corrected}, want: corrected},
		{name: "advisory cannot supply identity", id: camtypes.ArchiveIdentity{UserName: "other", FilenameUserID: "2"}, accounts: []*importerAcct{a, advisory}},
		{name: "filename cannot override unique", id: camtypes.ArchiveIdentity{UserName: "alice", FilenameUserID: "2"}, accounts: []*importerAcct{a, b}, want: a},
		{name: "unmatched filename ambiguity", id: camtypes.ArchiveIdentity{UserName: "alice", Email: "bob@example.test", FilenameUserID: "3"}, accounts: []*importerAcct{a, b}, conflict: true},
		{name: "duplicate IDs ambiguous", id: camtypes.ArchiveIdentity{UserName: "alice", Email: "bob@example.test", FilenameUserID: "1"}, accounts: []*importerAcct{a, account("c", "bob", "bob@example.test", "1")}, conflict: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := matchArchiveAccount(tt.accounts, tt.id)
			if got != tt.want || errors.Is(err, errArchiveConflict) != tt.conflict || (err != nil && !tt.conflict) {
				t.Fatalf("match = %v, %v; want %v conflict=%v", got, err, tt.want, tt.conflict)
			}
		})
	}
	for _, id := range []camtypes.ArchiveIdentity{{FilenameUserID: "1"}, {Name: "Alice"}, {UserName: "bad\x00name"}, {Email: strings.Repeat("a", 4097)}} {
		if _, err := normalizeArchiveIdentity(id); err == nil {
			t.Errorf("accepted invalid identity %+v", id)
		}
	}
}

func TestArchiveConcurrentStartAndReplacement(t *testing.T) {
	h, im, target := archiveTestHost(t)
	entered := make(chan *RunContext, 1)
	release := make(chan struct{})
	im.run = func(rc *RunContext, a *Archive) error {
		entered <- rc
		select {
		case <-release:
			return nil
		case <-rc.Context().Done():
			return rc.Context().Err()
		}
	}
	ref := archiveTestUpload(t, target, "first.zip", camtypes.ArchiveIdentity{UserName: "alice"})
	type outcome struct {
		result *camtypes.ArchiveImportResult
		err    error
	}
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			r, e := h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
			results <- outcome{r, e}
		}()
	}
	var first *camtypes.ArchiveImportResult
	created := 0
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		if first == nil {
			first = got.result
		}
		if got.result.AccountRef != first.AccountRef {
			t.Fatal("concurrent start created duplicate accounts")
		}
		if got.result.Created {
			created++
		}
	}
	rc := <-entered
	if created != 1 || im.calls.Load() != 1 {
		t.Fatalf("created=%d runs=%d", created, im.calls.Load())
	}
	// Progress writes race with same-input starts and status reads. Both must
	// return snapshots and never replace this run's immutable input selection.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 10 {
			if err := rc.SetArchiveProgress(camtypes.ArchiveImportProgress{ItemsTotal: 10, ItemsDone: i}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for range 10 {
		got, err := h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
		if err != nil || got.AccountRef != first.AccountRef {
			t.Fatalf("same-input start = %+v, %v", got, err)
		}
		if _, err := h.ArchiveStatus(context.Background(), archiveTestName, first.AccountRef); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	other := archiveTestUpload(t, target, "replacement.zip", camtypes.ArchiveIdentity{UserName: "alice"})
	if _, err := h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: other}); !errors.Is(err, errArchiveActive) {
		t.Fatalf("replacement error = %v", err)
	}
	ia, _ := h.imp[archiveTestName].account(first.AccountRef)
	if ia.acct.Attr(AcctAttrArchiveInput) != ref.String() {
		t.Fatal("active input replaced")
	}
	close(release)
	status := waitArchiveIdle(t, h, first.AccountRef)
	if status.State != "complete" || status.Progress.ItemsDone != 9 {
		t.Fatalf("final status = %+v", status)
	}
}

type archiveReloadSearcher struct {
	attrs    map[blob.Ref]url.Values
	accounts []blob.Ref
}

func (s archiveReloadSearcher) Query(context.Context, *search.SearchQuery) (*search.SearchResult, error) {
	r := &search.SearchResult{}
	for _, ref := range s.accounts {
		r.Blobs = append(r.Blobs, &search.SearchResultBlob{Blob: ref})
	}
	return r, nil
}
func (s archiveReloadSearcher) Describe(ctx context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error) {
	attrs, ok := s.attrs[req.BlobRef]
	if !ok {
		return nil, fmt.Errorf("missing fixture permanode %s", req.BlobRef)
	}
	copy := make(url.Values)
	for k, v := range attrs {
		copy[k] = append([]string(nil), v...)
	}
	return &search.DescribeResponse{Meta: search.MetaMap{req.BlobRef.String(): {BlobRef: req.BlobRef, CamliType: "permanode", Permanode: &search.DescribedPermanode{Attr: copy}}}}, nil
}
func archiveReloadHost(t *testing.T, h *Host, ia *importerAcct, im *archiveTestImporter) *Host {
	t.Helper()
	attrs := make(map[blob.Ref]url.Values)
	for _, o := range []*Object{ia.acct, ia.root} {
		o.mu.Lock()
		copy := make(url.Values)
		for k, v := range o.attr {
			copy[k] = append([]string(nil), v...)
		}
		o.mu.Unlock()
		attrs[o.PermanodeRef()] = copy
	}
	next, err := NewHost(HostConfig{Target: h.target, BlobSource: h.blobSource, Signer: h.signer, Search: archiveReloadSearcher{attrs: attrs, accounts: []blob.Ref{ia.acct.PermanodeRef()}}})
	if err != nil {
		t.Fatal(err)
	}
	next.imp[archiveTestName].impl = im
	t.Cleanup(func() {
		if err := next.Close(); err != nil {
			t.Error(err)
		}
	})
	return next
}

func TestArchiveDurableErrorResumeAndHistory(t *testing.T) {
	h, im, target := archiveTestHost(t)
	failure := errors.New("activity import failed")
	im.run = func(rc *RunContext, a *Archive) error { return failure }
	ref := archiveTestUpload(t, target, "first.zip", camtypes.ArchiveIdentity{UserName: "alice"})
	result, err := h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	status := waitArchiveIdle(t, h, result.AccountRef)
	if status.State != "error" || status.Error != failure.Error() || status.Finished.IsZero() {
		t.Fatalf("failure status = %+v", status)
	}
	ia, _ := h.imp[archiveTestName].account(result.AccountRef)
	// Reload cold canonical accounts from persisted attributes, not the previous
	// host's RunContext, status cache, or disposable importer caches.
	next := archiveReloadHost(t, h, ia, &archiveTestImporter{Importer: TODOImporter})
	reloaded, err := next.ArchiveStatus(context.Background(), archiveTestName, result.AccountRef)
	if err != nil || reloaded.State != "error" || reloaded.Error != failure.Error() {
		t.Fatalf("reloaded error = %+v, %v", reloaded, err)
	}
	// Leave a running claim without scheduling work, as at a process crash.
	ia.mu.Lock()
	_, err = ia.prepareArchiveLocked(ref)
	ia.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	resumed := archiveReloadHost(t, h, ia, &archiveTestImporter{Importer: TODOImporter})
	reloaded, err = resumed.ArchiveStatus(context.Background(), archiveTestName, result.AccountRef)
	if err != nil || reloaded.State != "interrupted" || reloaded.FileRef != ref {
		t.Fatalf("interrupted status = %+v, %v", reloaded, err)
	}
	// The existing normal account start action resumes exactly the durable ref.
	loaded, _ := resumed.imp[archiveTestName].account(result.AccountRef)
	loaded.start()
	if s := waitArchiveIdle(t, resumed, result.AccountRef); s.State != "complete" || s.FileRef != ref {
		t.Fatalf("resume = %+v", s)
	}
	other := archiveTestUpload(t, target, "second.zip", camtypes.ArchiveIdentity{UserName: "alice"})
	result2, err := resumed.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: other})
	if err != nil {
		t.Fatal(err)
	}
	waitArchiveIdle(t, resumed, result2.AccountRef)
	if result2.Created || result2.AccountRef != result.AccountRef || loaded.acct.Attr(archiveHistoryPrefix+ref.String()) == "" || loaded.acct.Attr(archiveHistoryPrefix+other.String()) == "" {
		t.Fatal("replacement lost account or input history")
	}
}

func TestArchiveFailureCleanupDisconnectAndCancel(t *testing.T) {
	h, im, target := archiveTestHost(t)
	entered := make(chan *RunContext, 1)
	finish := make(chan struct{})
	im.run = func(rc *RunContext, a *Archive) error {
		entered <- rc
		select {
		case <-finish:
			return nil
		case <-rc.Context().Done():
			return rc.Context().Err()
		}
	}
	ref := archiveTestUpload(t, target, "export.zip", camtypes.ArchiveIdentity{UserName: "alice"})
	ctx, cancel := context.WithCancel(context.Background())
	result, err := h.ImportArchive(ctx, archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	rc := <-entered
	cancel()
	if err := rc.Context().Err(); err != nil {
		t.Fatalf("request disconnect canceled managed run: %v", err)
	}
	ia, _ := h.imp[archiveTestName].account(result.AccountRef)
	ia.stop()
	status := waitArchiveIdle(t, h, result.AccountRef)
	if status.State != "error" || !strings.Contains(status.Error, "canceled") {
		t.Fatalf("canceled status = %+v", status)
	}
	// A failure recording completion is reported in-process, not a false success;
	// the previous durable running record becomes interrupted on restart.
	result, err = h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	target.mu.Lock()
	target.err = errors.New("outcome storage unavailable")
	target.mu.Unlock()
	close(finish)
	status = waitArchiveIdle(t, h, result.AccountRef)
	if status.State != "error" || !strings.Contains(status.Error, "recording archive outcome") {
		t.Fatalf("persist failure = %+v", status)
	}
	target.mu.Lock()
	target.err = nil
	target.mu.Unlock()
	next := archiveReloadHost(t, h, ia, &archiveTestImporter{Importer: TODOImporter})
	reloaded, err := next.ArchiveStatus(context.Background(), archiveTestName, result.AccountRef)
	if err != nil || reloaded.State != "interrupted" {
		t.Fatalf("failed outcome reloaded = %+v, %v", reloaded, err)
	}
}

func TestArchiveValidationAndReadLimits(t *testing.T) {
	store := new(memory.Storage)
	cases := []struct {
		name    string
		members []archiveMember
	}{
		{"duplicate", []archiveMember{{name: "owner.json", data: "a"}, {name: "owner.json", data: "b"}}},
		{"parent", []archiveMember{{name: "../owner.json"}}},
		{"absolute", []archiveMember{{name: "/owner.json"}}},
		{"backslash", []archiveMember{{name: `dir\owner.json`}}},
		{"dot", []archiveMember{{name: "dir/./owner.json"}}},
		{"control", []archiveMember{{name: "dir/evil\nname"}}},
		{"symlink", []archiveMember{{name: "owner.json", mode: os.ModeSymlink | 0777}}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := schema.WriteFileFromReader(context.Background(), store, "bad.zip", bytes.NewReader(archiveZIP(t, tt.members...)))
			if err != nil {
				t.Fatal(err)
			}
			if a, err := openArchive(context.Background(), store, ref); err == nil {
				a.file.Close()
				t.Fatal("invalid archive accepted")
			}
		})
	}
	ref := archiveTestUpload(t, store, "good.zip", camtypes.ArchiveIdentity{UserName: "alice"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := openArchive(ctx, store, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer a.file.Close()
	if _, err := a.OpenLimit("activities.csv", 2); err == nil {
		t.Fatal("declared-size limit not enforced")
	}
	r, err := a.Open("activities.csv")
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.read = maxArchiveRead
	a.mu.Unlock()
	if _, err := io.ReadAll(r); err == nil {
		t.Fatal("cumulative actual-byte budget not enforced")
	}
	r.Close()
	// Exercise the actual per-reader budget independent of ZIP's own size check.
	bounded := &archiveEntryReader{ReadCloser: io.NopCloser(strings.NewReader("12345")), archive: &Archive{ctx: context.Background()}, left: 3}
	if _, err := io.ReadAll(bounded); err == nil {
		t.Fatal("actual per-entry budget not enforced")
	}
	r, err = a.Open("activities.csv")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read = %v", err)
	}
	r.Close()
	if _, err := a.Open("activities.csv"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open = %v", err)
	}
	// FileReader also accepts bytes schemas; the archive API intentionally does not.
	raw := `{"camliVersion":1,"camliType":"bytes","parts":[]}`
	br, err := blobserver.ReceiveString(context.Background(), store, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openArchive(context.Background(), store, br.Ref); err == nil {
		t.Fatal("bytes schema accepted as file")
	}
}

func TestArchiveDirectoryLimits(t *testing.T) {
	valid := archiveZIP(t, archiveMember{name: "owner.json", data: "{}"})
	mutate := func(fn func([]byte)) []byte { b := bytes.Clone(valid); fn(b[len(b)-22:]); return b }
	for _, data := range [][]byte{
		mutate(func(e []byte) { binary.LittleEndian.PutUint32(e[12:], maxArchiveDirectory+1) }),
		mutate(func(e []byte) { binary.LittleEndian.PutUint16(e[8:], 0); binary.LittleEndian.PutUint16(e[10:], 0) }),
		mutate(func(e []byte) { binary.LittleEndian.PutUint16(e[4:], 1) }),
		mutate(func(e []byte) { binary.LittleEndian.PutUint32(e[16:], 0xfffffffe) }),
	} {
		if err := checkZIPDirectory(bytes.NewReader(data), int64(len(data))); err == nil {
			t.Error("invalid directory accepted")
		}
	}
	if err := checkZIPDirectory(bytes.NewReader(valid), int64(len(valid))); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveHTTPJSONSecurity(t *testing.T) {
	h, _, target := archiveTestHost(t)
	im := h.imp[archiveTestName]
	ref := archiveTestUpload(t, target, "export.zip", camtypes.ArchiveIdentity{UserName: "alice"})
	body := fmt.Sprintf(`{"action":"inspect","fileRef":%q}`, ref.String())
	tests := []struct {
		name, method, body, typ, origin, site string
		code                                  int
	}{
		{name: "CLI", method: "POST", body: body, typ: "application/json", code: 200},
		{name: "same origin", method: "POST", body: body, typ: "application/json; charset=utf-8", origin: "http://example.test", site: "same-origin", code: 200},
		{name: "form", method: "POST", body: body, typ: "application/x-www-form-urlencoded", code: 403},
		{name: "cross origin", method: "POST", body: body, typ: "application/json", origin: "https://evil.test", code: 403},
		{name: "null origin", method: "POST", body: body, typ: "application/json", origin: "null", code: 403},
		{name: "same site not origin", method: "POST", body: body, typ: "application/json", site: "same-site", code: 403},
		{name: "cross site", method: "POST", body: body, typ: "application/json", site: "cross-site", code: 403},
		{name: "unknown field", method: "POST", body: `{"action":"inspect","secret":1}`, typ: "application/json", code: 400},
		{name: "trailing JSON", method: "POST", body: body + ` {}`, typ: "application/json", code: 400},
		{name: "oversize", method: "POST", body: strings.Repeat(" ", 20<<10) + body, typ: "application/json", code: 400},
		{name: "method", method: "PUT", code: 405},
		{name: "invalid account", method: "GET", code: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "http://example.test/importer/archive-test/archive", strings.NewReader(tt.body))
			if tt.typ != "" {
				r.Header.Set("Content-Type", tt.typ)
			}
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if tt.site != "" {
				r.Header.Set("Sec-Fetch-Site", tt.site)
			}
			w := httptest.NewRecorder()
			h.serveArchive(w, r, im)
			if w.Code != tt.code {
				t.Fatalf("HTTP %d, want %d: %s", w.Code, tt.code, w.Body.String())
			}
			if w.Header().Get("Content-Type") != "application/json" {
				t.Fatal("non-JSON response")
			}
		})
	}
	result, err := h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	waitArchiveIdle(t, h, result.AccountRef)
	r := httptest.NewRequest(http.MethodGet, "http://example.test/importer/archive-test/archive?account="+result.AccountRef.String(), nil)
	w := httptest.NewRecorder()
	h.serveArchive(w, r, im)
	var status camtypes.ArchiveImportStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || status.AccountRef != result.AccountRef || status.State != "complete" {
		t.Fatalf("bare GET status = %s (%v)", w.Body.String(), err)
	}
}

func TestArchiveProgressBounded(t *testing.T) {
	h, im, target := archiveTestHost(t)
	im.run = func(rc *RunContext, a *Archive) error {
		return rc.SetArchiveProgress(camtypes.ArchiveImportProgress{ItemsTotal: 5, ItemsDone: 5, ItemsUnchanged: 4, Warnings: strings.Fields(strings.Repeat(strings.Repeat("w", 2048)+" ", 150))})
	}
	ref := archiveTestUpload(t, target, "export.zip", camtypes.ArchiveIdentity{UserName: "alice"})
	result, err := h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	status := waitArchiveIdle(t, h, result.AccountRef)
	if status.Progress.WarningCount != 150 || len(status.Progress.Warnings) != 100 || len(status.Progress.Warnings[0]) != 1024 || status.Progress.ItemsUnchanged != 4 {
		t.Fatalf("unbounded or incorrect progress: %+v", status.Progress)
	}
	ia, _ := h.imp[archiveTestName].account(result.AccountRef)
	next := archiveReloadHost(t, h, ia, &archiveTestImporter{Importer: TODOImporter})
	got, err := next.ArchiveStatus(context.Background(), archiveTestName, result.AccountRef)
	if err != nil || len(got.Progress.Warnings) != 100 || got.Progress.WarningCount != 150 {
		t.Fatalf("durable progress = %+v, %v", got, err)
	}
}

// The first 1.5 GiB of this file is deliberately unavailable. Only the small
// tail is stored. Opening and inspecting the ZIP must never try to fetch the
// prefix, allocate an archive-sized buffer, or copy the archive to scratch.
func TestArchiveLargeRandomAccess(t *testing.T) {
	const prefixSize = int64(1536 << 20)
	var suffix bytes.Buffer
	suffix.Write(make([]byte, 65557)) // keep the ZIP tail window in available chunks
	zw := zip.NewWriter(&suffix)
	zw.SetOffset(prefixSize + int64(suffix.Len()))
	w, err := zw.Create("owner.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, `{"userName":"alice","email":"alice@example.test"}`); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	store := new(memory.Storage)
	tail, err := blobserver.ReceiveString(context.Background(), store, suffix.String())
	if err != nil {
		t.Fatal(err)
	}
	missing := blob.RefFromString("unavailable 1.5 GiB prefix; must not be fetched")
	bb := schema.NewFileMap("large-export.zip")
	if err := bb.PopulateParts(prefixSize+int64(suffix.Len()), []schema.BytesPart{{BlobRef: missing, Size: uint64(prefixSize)}, {BlobRef: tail.Ref, Size: uint64(suffix.Len())}}); err != nil {
		t.Fatal(err)
	}
	file, err := blobserver.ReceiveString(context.Background(), store, bb.Blob().JSON())
	if err != nil {
		t.Fatal(err)
	}
	a, err := openArchive(context.Background(), store, file.Ref)
	if err != nil {
		t.Fatal(err)
	}
	defer a.file.Close()
	im := &archiveTestImporter{Importer: TODOImporter}
	id, err := im.InspectArchive(context.Background(), a)
	if err != nil || id.UserName != "alice" {
		t.Fatalf("large archive inspection = %+v, %v", id, err)
	}
	_, fetched := store.Stats()
	if fetched > 1<<20 {
		t.Fatalf("inspection fetched %d bytes for a %d-byte archive", fetched, a.file.Size())
	}
	if a.FileName != "large-export.zip" || a.FileRef != file.Ref || a.file.Size() < 1500<<20 {
		t.Fatal("large file schema metadata lost")
	}
}

func TestArchiveOriginConfiguredPublicURL(t *testing.T) {
	// The reverse proxy's HTTP backend cannot be used to infer the browser's
	// public origin. Only trusted configuration, never Forwarded, selects it.
	r := httptest.NewRequest("POST", "http://backend.internal/importer/archive-test/archive", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://public.example.test")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if err := archiveJSONRequest(r, "https://public.example.test"); err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", "https://evil.example.test")
	r.Header.Set("Forwarded", "proto=https;host=evil.example.test")
	if err := archiveJSONRequest(r, "https://public.example.test"); err == nil {
		t.Fatal("forwarded hostile origin accepted")
	}
}

func TestArchiveLogicalSizeRejectedBeforeData(t *testing.T) {
	store := new(memory.Storage)
	missing := blob.RefFromString("must not be fetched for an oversized archive")
	for _, sizes := range [][]uint64{{maxArchiveSize + 1}, {uint64(1 << 63), uint64(1<<63) + 500}, {maxArchiveSize, 1}} {
		var parts []map[string]any
		for _, n := range sizes {
			parts = append(parts, map[string]any{"blobRef": missing.String(), "size": n})
		}
		raw, err := json.Marshal(map[string]any{"camliVersion": 1, "camliType": "file", "fileName": "oversized.zip", "parts": parts})
		if err != nil {
			t.Fatal(err)
		}
		file, err := blobserver.ReceiveString(context.Background(), store, string(raw))
		if err != nil {
			t.Fatal(err)
		}
		_, err = openArchive(context.Background(), store, file.Ref)
		if err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("oversized %v = %v; wanted rejection before missing data fetch", sizes, err)
		}
	}
}

func TestArchiveDeleteSerializesResolution(t *testing.T) {
	h, im, target := archiveTestHost(t)
	ref := archiveTestUpload(t, target, "export.zip", camtypes.ArchiveIdentity{UserName: "alice"})
	first, err := h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	waitArchiveIdle(t, h, first.AccountRef)
	provider := h.imp[archiveTestName]
	ia, _ := provider.account(first.AccountRef)
	snapshot, _ := provider.Accounts()
	deleting := make(chan struct{})
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	target.mu.Lock()
	target.before = func(body string) {
		if strings.Contains(body, nodeTypeImporterAccount+"-deleted") {
			close(deleting)
			<-release
		}
	}
	target.mu.Unlock()
	deleted := make(chan error, 1)
	go func() { deleted <- ia.delete() }()
	<-deleting
	inspected := make(chan struct{})
	im.inspected = func() { close(inspected) }
	type outcome struct {
		result *camtypes.ArchiveImportResult
		err    error
	}
	started := make(chan outcome, 1)
	go func() {
		r, e := h.ImportArchive(context.Background(), archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
		started <- outcome{r, e}
	}()
	<-inspected
	unblock()
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	got := <-started
	if got.err != nil {
		t.Fatal(got.err)
	}
	if !got.result.Created || got.result.AccountRef == first.AccountRef {
		t.Fatal("archive started on deleted account")
	}
	waitArchiveIdle(t, h, got.result.AccountRef)
	if matched, err := matchArchiveAccount(snapshot, camtypes.ArchiveIdentity{UserName: "alice"}); err != nil || matched != nil {
		t.Fatalf("stale account snapshot selected deleted account: %v %v", matched, err)
	}
	calls := im.calls.Load()
	ia.start()
	if im.calls.Load() != calls {
		t.Fatal("normal account start ran a deleted archive account")
	}
}

func TestArchiveCanceledResolveDoesNotCreate(t *testing.T) {
	h, im, target := archiveTestHost(t)
	ref := archiveTestUpload(t, target, "export.zip", camtypes.ArchiveIdentity{UserName: "alice"})
	inspected := make(chan struct{})
	im.inspected = func() { close(inspected) }
	provider := h.imp[archiveTestName]
	provider.archiveMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	before := len(target.uploads())
	go func() {
		_, err := h.ImportArchive(ctx, archiveTestName, camtypes.ArchiveImportRequest{Action: "start", FileRef: ref})
		done <- err
	}()
	<-inspected
	cancel()
	provider.archiveMu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolution = %v", err)
	}
	if accts, _ := provider.Accounts(); len(accts) != 0 || len(target.uploads()) != before || im.calls.Load() != 0 {
		t.Fatal("canceled archive resolution mutated an account")
	}
}

func TestArchiveZIP64AndEOCDSelection(t *testing.T) {
	ordinary := archiveZIP(t, archiveMember{name: "owner.json", data: `{"userName":"alice"}`})
	eocd := bytes.Clone(ordinary[len(ordinary)-22:])
	endPos := uint64(len(ordinary) - 22)
	z := make([]byte, 56)
	copy(z, []byte{'P', 'K', 6, 6})
	binary.LittleEndian.PutUint64(z[4:], 44)
	binary.LittleEndian.PutUint16(z[12:], 45)
	binary.LittleEndian.PutUint16(z[14:], 45)
	binary.LittleEndian.PutUint64(z[24:], 1)
	binary.LittleEndian.PutUint64(z[32:], 1)
	binary.LittleEndian.PutUint64(z[40:], uint64(binary.LittleEndian.Uint32(eocd[12:])))
	binary.LittleEndian.PutUint64(z[48:], uint64(binary.LittleEndian.Uint32(eocd[16:])))
	locator := make([]byte, 20)
	copy(locator, []byte{'P', 'K', 6, 7})
	binary.LittleEndian.PutUint64(locator[8:], endPos)
	binary.LittleEndian.PutUint32(locator[16:], 1)
	binary.LittleEndian.PutUint16(eocd[8:], 65535)
	binary.LittleEndian.PutUint16(eocd[10:], 65535)
	binary.LittleEndian.PutUint32(eocd[12:], 0xffffffff)
	binary.LittleEndian.PutUint32(eocd[16:], 0xffffffff)
	zip64 := bytes.Clone(ordinary[:endPos])
	zip64 = append(zip64, z...)
	zip64 = append(zip64, locator...)
	zip64 = append(zip64, eocd...)
	store := new(memory.Storage)
	ref, err := schema.WriteFileFromReader(context.Background(), store, "zip64.zip", bytes.NewReader(zip64))
	if err != nil {
		t.Fatal(err)
	}
	a, err := openArchive(context.Background(), store, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer a.file.Close()
	id, err := (&archiveTestImporter{Importer: TODOImporter}).InspectArchive(context.Background(), a)
	if err != nil || id.UserName != "alice" {
		t.Fatalf("ZIP64 inspection = %+v, %v", id, err)
	}
	// A fake EOCD in a real EOCD's comment must not make our preflight validate
	// one directory while archive/zip later selects the other.
	spoof := bytes.Clone(ordinary)
	comment := make([]byte, 23)
	copy(comment, []byte{'P', 'K', 5, 6})
	binary.LittleEndian.PutUint16(spoof[len(spoof)-2:], uint16(len(comment)))
	spoof = append(spoof, comment...)
	if err := checkZIPDirectory(bytes.NewReader(spoof), int64(len(spoof))); err == nil {
		t.Fatal("ambiguous EOCD selection accepted")
	}
}
