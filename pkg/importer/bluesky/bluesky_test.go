/*
Copyright 2025 The Perkeep Authors

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

package bluesky

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/repo"
	"github.com/bluesky-social/indigo/atproto/repo/mst"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	blockstore "github.com/ipfs/go-ipfs-blockstore"
	"github.com/ipld/go-car"
	carutil "github.com/ipld/go-car/util"
	"github.com/multiformats/go-multihash"
	cbg "github.com/whyrusleeping/cbor-gen"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/importer"
	imptest "perkeep.org/pkg/importer/test"
	"perkeep.org/pkg/progress"
	"perkeep.org/pkg/schema/nodeattr"
	"perkeep.org/pkg/test"
)

const (
	testDID     = "did:plc:testuser000000000000000"
	otherDID    = "did:plc:otheruser00000000000000"
	testPDS     = "https://pds.example"
	otherPDS    = "https://otherpds.example"
	deletedPost = "at://" + otherDID + "/app.bsky.feed.post/3deleted00000"
	likedPost   = "at://" + otherDID + "/app.bsky.feed.post/3liked0000000"
)

var (
	likedPostCID = cborCID([]byte("liked post")).String()
	imageCID     = rawCID([]byte("dog image"))
	likedImgCID  = rawCID([]byte("cat image"))
)

func rawCID(b []byte) lexutil.LexLink {
	c, err := cid.NewPrefixV1(cid.Raw, multihash.SHA2_256).Sum(b)
	if err != nil {
		panic(err)
	}
	return lexutil.LexLink(c)
}

// listBlockstore is the subset of a blockstore that mst.Tree.WriteDiffBlocks
// uses, recording the blocks written to it.
type listBlockstore struct {
	blockstore.Blockstore // nil; only Put is used
	blocks                []blocks.Block
}

func (bs *listBlockstore) Put(_ context.Context, b blocks.Block) error {
	bs.blocks = append(bs.blocks, b)
	return nil
}

// testRepo builds a repo, generating CAR files of all the blocks changed
// since the previous call to car.
type testRepo struct {
	t       *testing.T
	tree    mst.Tree
	records []blocks.Block // not yet written to a CAR
	rev     *syntax.TIDClock
}

func newTestRepo(t *testing.T) *testRepo {
	return &testRepo{t: t, tree: mst.NewEmptyTree(), rev: syntax.NewTIDClock(0)}
}

func cborCID(b []byte) cid.Cid {
	c, err := cid.NewPrefixV1(cid.DagCBOR, multihash.SHA2_256).Sum(b)
	if err != nil {
		panic(err)
	}
	return c
}

func (tr *testRepo) put(path string, rec cbg.CBORMarshaler) cid.Cid {
	var buf bytes.Buffer
	if err := rec.MarshalCBOR(&buf); err != nil {
		tr.t.Fatal(err)
	}
	c := cborCID(buf.Bytes())
	blk, err := blocks.NewBlockWithCid(buf.Bytes(), c)
	if err != nil {
		tr.t.Fatal(err)
	}
	tr.records = append(tr.records, blk)
	if _, err := tr.tree.Insert([]byte(path), c); err != nil {
		tr.t.Fatal(err)
	}
	return c
}

// car returns a CAR of a new commit and all blocks since the last call,
// and the commit's rev.
func (tr *testRepo) car() ([]byte, string) {
	bs := &listBlockstore{}
	root, err := tr.tree.WriteDiffBlocks(context.Background(), bs)
	if err != nil {
		tr.t.Fatal(err)
	}
	commit := repo.Commit{
		DID:     testDID,
		Version: repo.ATPROTO_REPO_VERSION,
		Data:    *root,
		Rev:     tr.rev.Next().String(),
		Sig:     []byte("not checked"),
	}
	var cbuf bytes.Buffer
	if err := commit.MarshalCBOR(&cbuf); err != nil {
		tr.t.Fatal(err)
	}
	commitCID := cborCID(cbuf.Bytes())

	var out bytes.Buffer
	if err := car.WriteHeader(&car.CarHeader{Roots: []cid.Cid{commitCID}, Version: 1}, &out); err != nil {
		tr.t.Fatal(err)
	}
	write := func(c cid.Cid, data []byte) {
		if err := carutil.LdWrite(&out, c.Bytes(), data); err != nil {
			tr.t.Fatal(err)
		}
	}
	write(commitCID, cbuf.Bytes())
	for _, b := range append(bs.blocks, tr.records...) {
		write(b.Cid(), b.RawData())
	}
	tr.records = nil
	return out.Bytes(), commit.Rev
}

func strPtr(s string) *string { return &s }

// fakeNet serves the PDSes and AppView.
type fakeNet struct {
	t *testing.T

	mu       sync.Mutex
	repoCARs map[string][]byte // since => CAR
	requests []string
}

func (fn *fakeNet) RoundTrip(req *http.Request) (*http.Response, error) {
	fn.mu.Lock()
	defer fn.mu.Unlock()
	fn.requests = append(fn.requests, req.URL.String())
	q := req.URL.Query()
	host := req.URL.Scheme + "://" + req.URL.Host
	ok := func(body []byte) (*http.Response, error) {
		return &http.Response{
			StatusCode:    200,
			Header:        http.Header{},
			Body:          io.NopCloser(bytes.NewReader(body)),
			ContentLength: -1,
			Request:       req,
		}, nil
	}
	okJSON := func(v any) (*http.Response, error) {
		j, err := json.Marshal(v)
		if err != nil {
			fn.t.Fatal(err)
		}
		return ok(j)
	}
	switch {
	case host == testPDS && req.URL.Path == "/xrpc/com.atproto.sync.getRepo":
		if q.Get("did") != testDID {
			break
		}
		if car, ok2 := fn.repoCARs[q.Get("since")]; ok2 {
			return ok(car)
		}
	case req.URL.Path == "/xrpc/com.atproto.sync.getBlob":
		// The importer stores media as files, so the content needn't
		// match the CID.
		return ok([]byte("blob " + q.Get("did") + " " + q.Get("cid")))
	case host == appViewHost && req.URL.Path == "/xrpc/app.bsky.feed.getPosts":
		var out bsky.FeedGetPosts_Output
		for _, uri := range q["uris"] {
			if uri != likedPost {
				continue
			}
			out.Posts = append(out.Posts, &bsky.FeedDefs_PostView{
				Uri:    likedPost,
				Cid:    likedPostCID,
				Author: &bsky.ActorDefs_ProfileViewBasic{Did: otherDID, Handle: "other.example"},
				Record: &lexutil.LexiconTypeDecoder{Val: &bsky.FeedPost{
					LexiconTypeID: "app.bsky.feed.post",
					Text:          "a post worth liking",
					CreatedAt:     "2025-01-02T03:04:05.000Z",
					Embed: &bsky.FeedPost_Embed{EmbedImages: &bsky.EmbedImages{
						LexiconTypeID: "app.bsky.embed.images",
						Images: []*bsky.EmbedImages_Image{{
							Alt:   "a cat",
							Image: &lexutil.LexBlob{Ref: likedImgCID, MimeType: "image/jpeg", Size: 4},
						}},
					}},
				}},
				IndexedAt: "2025-01-02T03:04:05.000Z",
			})
		}
		return okJSON(out)
	case host == appViewHost && req.URL.Path == "/xrpc/app.bsky.actor.getProfiles":
		var out bsky.ActorGetProfiles_Output
		for _, did := range q["actors"] {
			if did == otherDID {
				out.Profiles = append(out.Profiles, &bsky.ActorDefs_ProfileViewDetailed{
					Did:         otherDID,
					Handle:      "other.example",
					DisplayName: strPtr("Other Person"),
					Avatar:      strPtr("https://cdn.example/avatar.jpg"),
				})
			}
		}
		return okJSON(out)
	case req.URL.String() == "https://cdn.example/avatar.jpg":
		return ok([]byte("avatar"))
	}
	fn.t.Errorf("unexpected request %s", req.URL)
	return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("{}")), Request: req}, nil
}

// testDirectory returns a directory of the test account and the other.
func testDirectory() *identity.MockDirectory {
	dir := identity.NewMockDirectory()
	dir.Insert(identity.Identity{
		DID:      testDID,
		Handle:   "test.example",
		Services: map[string]identity.ServiceEndpoint{"atproto_pds": {Type: "AtprotoPersonalDataServer", URL: testPDS}},
	})
	dir.Insert(identity.Identity{
		DID:      otherDID,
		Handle:   "other.example",
		Services: map[string]identity.ServiceEndpoint{"atproto_pds": {Type: "AtprotoPersonalDataServer", URL: otherPDS}},
	})
	return dir
}

func TestIntegration(t *testing.T) {
	test.TLog(t)
	dir := testDirectory()

	tr := newTestRepo(t)
	tr.put("app.bsky.feed.post/3post00000001", &bsky.FeedPost{
		LexiconTypeID: "app.bsky.feed.post",
		Text:          "hello, world",
		CreatedAt:     "2025-01-01T00:00:00.000Z",
		Embed: &bsky.FeedPost_Embed{EmbedRecordWithMedia: &bsky.EmbedRecordWithMedia{
			LexiconTypeID: "app.bsky.embed.recordWithMedia",
			Record:        &bsky.EmbedRecord{LexiconTypeID: "app.bsky.embed.record", Record: &atproto.RepoStrongRef{Uri: likedPost, Cid: likedPostCID}},
			Media: &bsky.EmbedRecordWithMedia_Media{EmbedImages: &bsky.EmbedImages{
				LexiconTypeID: "app.bsky.embed.images",
				Images: []*bsky.EmbedImages_Image{{
					Alt:   "a dog in a hat",
					Image: &lexutil.LexBlob{Ref: imageCID, MimeType: "image/jpeg", Size: 4},
				}},
			}},
		}},
	})
	tr.put("app.bsky.feed.like/3like00000001", &bsky.FeedLike{
		LexiconTypeID: "app.bsky.feed.like",
		CreatedAt:     "2025-01-03T00:00:00.000Z",
		Subject:       &atproto.RepoStrongRef{Uri: likedPost, Cid: likedPostCID},
	})
	tr.put("app.bsky.feed.like/3like00000002", &bsky.FeedLike{
		LexiconTypeID: "app.bsky.feed.like",
		CreatedAt:     "2025-01-03T00:00:00.000Z",
		Subject:       &atproto.RepoStrongRef{Uri: deletedPost, Cid: likedPostCID},
	})
	tr.put("app.bsky.graph.follow/3follow0000001", &bsky.GraphFollow{
		LexiconTypeID: "app.bsky.graph.follow",
		CreatedAt:     "2025-01-04T00:00:00.000Z",
		Subject:       otherDID,
	})
	blockCID := tr.put("app.bsky.graph.block/3block000000001", &bsky.GraphBlock{
		LexiconTypeID: "app.bsky.graph.block",
		CreatedAt:     "2025-01-05T00:00:00.000Z",
		Subject:       "did:plc:troll",
	})
	fullCAR, rev1 := tr.car()

	tr.put("app.bsky.feed.post/3post00000002", &bsky.FeedPost{
		LexiconTypeID: "app.bsky.feed.post",
		Text:          "second post",
		CreatedAt:     "2025-02-01T00:00:00.000Z",
	})
	diffCAR, rev2 := tr.car()

	fn := &fakeNet{t: t, repoCARs: map[string][]byte{"": fullCAR, rev1: diffCAR}}
	imptest.ImporterTest(t, "bluesky", fn, func(rc *importer.RunContext) {
		ctx := rc.Context()
		if err := rc.AccountNode().SetAttrs(importer.KV(acctAttrDID, testDID)); err != nil {
			t.Fatal(err)
		}
		im := &imp{dir: dir}
		if err := im.Run(rc); err != nil {
			t.Fatal(err)
		}
		acct := rc.AccountNode()
		if got := acct.Attr(acctAttrRev); got != rev1 {
			t.Errorf("rev = %q; want %q", got, rev1)
		}
		if got := acct.Attr(importer.AcctAttrUserName); got != "test.example" {
			t.Errorf("username = %q", got)
		}
		// The run's progress bars, which the account's page shows.
		var bars []string
		for _, st := range rc.Progress().Snapshot() {
			bars = append(bars, fmt.Sprintf("%s %d/%d %v", st.Name, st.Done, st.Total, st.Finished))
		}
		if got, want := strings.Join(bars, ", "), "blocks 11/11 true, records 5/5 true, posts 2/2 true, profiles 1/1 true"; got != want {
			t.Errorf("progress bars = %q; want %q", got, want)
		}

		child := func(parent *importer.Object, path ...string) *importer.Object {
			t.Helper()
			for _, p := range path {
				var err error
				parent, err = imptest.GetRequiredChildPathObj(parent, p)
				if err != nil {
					t.Fatal(err)
				}
			}
			return parent
		}
		root := rc.RootNode()

		post := child(root, collPost, "3post00000001")
		if got := post.Attr(nodeattr.Content); got != "hello, world" {
			t.Errorf("post content = %q", got)
		}
		if got := post.Attr(attrQuote); got != likedPost {
			t.Errorf("quoted post = %q", got)
		}
		img := child(post, "image1")
		if got := img.Attr(nodeattr.Description); got != "a dog in a hat" {
			t.Errorf("image alt text = %q", got)
		}
		if got, want := post.Attr(nodeattr.CamliContentImage), img.Attr(nodeattr.CamliContent); got != want || got == "" {
			t.Errorf("post camliContentImage = %q; want %q", got, want)
		}

		// The raw record is stored under the sha256 of its CID.
		blockNode := child(root, "app.bsky.graph.block", "3block000000001")
		wantRef, _ := blobRefOfCID(blockCID)
		if got := blockNode.Attr(attrRecord); got != wantRef.String() {
			t.Errorf("block record ref = %q; want %v", got, wantRef)
		}
		rd, _, err := rc.Host.BlobSource().Fetch(ctx, wantRef)
		if err != nil {
			t.Fatalf("fetching record blob %v: %v", wantRef, err)
		}
		raw, _ := io.ReadAll(rd)
		rd.Close()
		if cborCID(raw) != blockCID {
			t.Errorf("record blob doesn't match its CID")
		}
		if blockNode.Attr(nodeattr.CamliContent) == "" {
			t.Errorf("generic record has no content")
		}

		like := child(root, collLike, "3like00000001")
		liked := child(root, nodeLinkedPosts, likedPost)
		if got := liked.Attr(nodeattr.Type); got != nodeTypeLinkedPost {
			t.Errorf("liked post type = %q; want %q", got, nodeTypeLinkedPost)
		}
		if got := like.Attr(nodeattr.CamliContent); got != liked.PermanodeRef().String() {
			t.Errorf("like content = %q; want %v", got, liked.PermanodeRef())
		}
		if got := like.Attr(nodeattr.Title); got != "Like: a post worth liking" {
			t.Errorf("like title = %q", got)
		}
		if got := child(liked, "image1").Attr(nodeattr.Description); got != "a cat" {
			t.Errorf("liked post image alt text = %q", got)
		}
		deadLike := child(root, collLike, "3like00000002")
		if deadLike.Attr(attrCID) == "" || deadLike.Attr(attrSubject) != deletedPost {
			t.Errorf("like of deleted post not marked done with subject")
		}

		follow := child(root, collFollow, "3follow0000001")
		if got := follow.Attr(nodeattr.Title); got != "Follow: Other Person (@other.example)" {
			t.Errorf("follow title = %q", got)
		}

		// Incremental run: only the new post is fetched and imported.
		fn.mu.Lock()
		fn.requests = nil
		fn.mu.Unlock()
		if err := im.Run(rc); err != nil {
			t.Fatal(err)
		}
		if got := acct.Attr(acctAttrRev); got != rev2 {
			t.Errorf("rev after incremental = %q; want %q", got, rev2)
		}
		if got := child(root, collPost, "3post00000002").Attr(nodeattr.Content); got != "second post" {
			t.Errorf("second post content = %q", got)
		}
		for _, u := range fn.requests {
			if !strings.Contains(u, "getRepo") {
				t.Errorf("unexpected request in incremental run: %s", u)
			}
		}

		// No changes: the PDS returns just a header naming the same commit.
		commitCID, err := cid.Decode(acct.Attr(acctAttrCommit))
		if err != nil {
			t.Fatal(err)
		}
		var hdr bytes.Buffer
		if err := car.WriteHeader(&car.CarHeader{Roots: []cid.Cid{commitCID}, Version: 1}, &hdr); err != nil {
			t.Fatal(err)
		}
		fn.mu.Lock()
		fn.repoCARs[rev2] = hdr.Bytes()
		fn.mu.Unlock()
		if err := im.Run(rc); err != nil {
			t.Fatalf("run with no changes: %v", err)
		}
		if got := acct.Attr(acctAttrRev); got != rev2 {
			t.Errorf("rev after no-op run = %q; want %q", got, rev2)
		}
	})
}

// postsCAR returns the CAR of a repo of n posts.
func postsCAR(t *testing.T, n int) []byte {
	t.Helper()
	tr := newTestRepo(t)
	for i := range n {
		tr.put(fmt.Sprintf("app.bsky.feed.post/3post%08d", i), &bsky.FeedPost{
			LexiconTypeID: "app.bsky.feed.post",
			Text:          fmt.Sprintf("post %d", i),
			CreatedAt:     "2025-01-01T00:00:00.000Z",
		})
	}
	carBytes, _ := tr.car()
	return carBytes
}

// TestLoadCARSkipsStoredBlocks tests that a run doesn't store again the
// repo's blocks that are stored already, as by an earlier run that
// stopped before it finished.
func TestLoadCARSkipsStoredBlocks(t *testing.T) {
	carBytes := postsCAR(t, 10)
	h := imptest.NewMemoryHost(t, importer.HostConfig{})
	t.Cleanup(func() { h.Close() })
	var puts atomic.Int32
	testHookPutBlock = func() { puts.Add(1) }
	t.Cleanup(func() { testHookPutBlock = nil })

	load := func() progress.Status {
		t.Helper()
		puts.Store(0)
		rc, err := importer.CreateAccount(h, "bluesky")
		if err != nil {
			t.Fatal(err)
		}
		r := &run{RunContext: rc}
		if _, _, _, _, err := r.loadCAR(carBytes); err != nil {
			t.Fatal(err)
		}
		return rc.Progress().Bar("blocks", "").Status()
	}
	st := load()
	if n := puts.Load(); n == 0 || st.Done != int64(n) || st.Skipped != 0 || !st.Finished {
		t.Fatalf("first run stored %d blocks, bar %+v; want all stored, none skipped", n, st)
	}
	total := st.Total
	st = load()
	if n := puts.Load(); n != 0 || st.Total != total || st.Skipped != total || !st.Finished {
		t.Errorf("second run stored %d blocks, bar %+v; want all %d skipped", n, st, total)
	}
}

// TestLoadCARStopsWhenCanceled tests that a run stops storing its repo's
// blocks when its context is canceled, as when it's paused.
func TestLoadCARStopsWhenCanceled(t *testing.T) {
	const posts = 100
	carBytes := postsCAR(t, posts)

	h := imptest.NewMemoryHost(t, importer.HostConfig{})
	t.Cleanup(func() { h.Close() })
	rc, err := importer.CreateAccount(h, "bluesky")
	if err != nil {
		t.Fatal(err)
	}
	// Once a few blocks are stored, cancel the run's context, by closing
	// its host.
	const stopAt = 5
	var puts atomic.Int32
	testHookPutBlock = func() {
		if puts.Add(1) == stopAt {
			h.Close()
		}
	}
	t.Cleanup(func() { testHookPutBlock = nil })

	r := &run{RunContext: rc}
	if _, _, _, _, err := r.loadCAR(carBytes); !errors.Is(err, context.Canceled) {
		t.Fatalf("loadCAR = %v; want context.Canceled", err)
	}
	// Those being stored when it was canceled may finish.
	if n := puts.Load(); n > stopAt+importConcurrency {
		t.Errorf("stored %d blocks; want at most %d", n, stopAt+importConcurrency)
	}
	if st := rc.Progress().Bar("blocks", "").Status(); st.Finished || st.Total <= posts || st.Done >= st.Total {
		t.Errorf("blocks bar = %+v; want unfinished, of more than %d blocks", st, posts)
	}
}

// TestFetchOptions tests the account's options to fetch the posts it liked
// and reposted, each independently.
func TestFetchOptions(t *testing.T) {
	tr := newTestRepo(t)
	tr.put("app.bsky.feed.like/3like00000001", &bsky.FeedLike{
		LexiconTypeID: "app.bsky.feed.like",
		CreatedAt:     "2025-01-03T00:00:00.000Z",
		Subject:       &atproto.RepoStrongRef{Uri: likedPost, Cid: likedPostCID},
	})
	tr.put("app.bsky.feed.repost/3repost0000001", &bsky.FeedRepost{
		LexiconTypeID: "app.bsky.feed.repost",
		CreatedAt:     "2025-01-03T00:00:00.000Z",
		Subject:       &atproto.RepoStrongRef{Uri: likedPost, Cid: likedPostCID},
	})
	fullCAR, rev := tr.car()
	fn := &fakeNet{t: t, repoCARs: map[string][]byte{"": fullCAR}}

	h := imptest.NewMemoryHost(t, importer.HostConfig{HTTPClient: &http.Client{Transport: fn}})
	t.Cleanup(func() { h.Close() })
	rc, err := importer.CreateAccount(h, "bluesky")
	if err != nil {
		t.Fatal(err)
	}
	acct := rc.AccountNode()
	if err := acct.SetAttrs(importer.KV(acctAttrDID, testDID)); err != nil {
		t.Fatal(err)
	}
	im := &imp{dir: testDirectory()}

	// setOptions sets the options as the account's page does.
	setOptions := func(fetchLiked, fetchReposted bool) {
		t.Helper()
		form := url.Values{"step": {"options"}}
		if fetchLiked {
			form.Set("fetchLiked", "on")
		}
		if fetchReposted {
			form.Set("fetchReposted", "on")
		}
		req := httptest.NewRequest("POST", "/importer/bluesky/callback?acct="+acct.PermanodeRef().String(), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set(httputil.PathSuffixHeader, "bluesky/callback")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("setting options: %d %s", w.Code, w.Body.String())
		}
	}
	// run runs an import, and returns the like and repost nodes, and
	// whether it fetched the posts.
	run := func() (like, repost *importer.Object, fetched bool) {
		t.Helper()
		fn.mu.Lock()
		fn.requests = nil
		fn.mu.Unlock()
		if err := im.Run(rc); err != nil {
			t.Fatal(err)
		}
		if got := acct.Attr(acctAttrRev); got != rev {
			t.Errorf("rev = %q; want %q", got, rev)
		}
		fn.mu.Lock()
		fetched = slices.ContainsFunc(fn.requests, func(u string) bool { return strings.Contains(u, "getPosts") })
		fn.mu.Unlock()
		node := func(coll, rkey string) *importer.Object {
			t.Helper()
			n, err := imptest.GetRequiredChildPathObj(rc.RootNode(), coll)
			if err == nil {
				n, err = imptest.GetRequiredChildPathObj(n, rkey)
			}
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
		return node(collLike, "3like00000001"), node(collRepost, "3repost0000001"), fetched
	}
	// hydrated reports whether n, a like or repost, is of the fetched post.
	hydrated := func(n *importer.Object) bool {
		return strings.HasSuffix(n.Attr(nodeattr.Title), ": a post worth liking") && n.Attr(attrCID) != ""
	}

	setOptions(false, false)
	like, repost, fetched := run()
	if fetched || hydrated(like) || hydrated(repost) {
		t.Errorf("with neither: fetched = %v, like hydrated = %v, repost hydrated = %v", fetched, hydrated(like), hydrated(repost))
	}
	if like.Attr(attrSubject) != likedPost || repost.Attr(attrSubject) != likedPost {
		t.Errorf("like and repost don't have their subjects")
	}

	// Turning on reposts makes the next run fetch the whole repo again, for
	// the reposts imported without their posts.
	setOptions(false, true)
	if got := acct.Attr(acctAttrRev); got != "" {
		t.Errorf("rev after turning on reposts = %q; want none", got)
	}
	like, repost, fetched = run()
	if !fetched || hydrated(like) || !hydrated(repost) {
		t.Errorf("with reposts: fetched = %v, like hydrated = %v, repost hydrated = %v", fetched, hydrated(like), hydrated(repost))
	}

	setOptions(true, true)
	like, repost, _ = run()
	if !hydrated(like) || !hydrated(repost) {
		t.Errorf("with both: like hydrated = %v, repost hydrated = %v", hydrated(like), hydrated(repost))
	}

	// Turning one off leaves the rev, as there's nothing to fetch again.
	setOptions(true, false)
	if got := acct.Attr(acctAttrRev); got != rev {
		t.Errorf("rev after turning off reposts = %q; want %q", got, rev)
	}
}

// TestRetypeLinkedPosts tests that others' posts imported with the type of
// the account's own get nodeTypeLinkedPost.
func TestRetypeLinkedPosts(t *testing.T) {
	h := imptest.NewMemoryHost(t, importer.HostConfig{})
	t.Cleanup(func() { h.Close() })
	rc, err := importer.CreateAccount(h, "bluesky")
	if err != nil {
		t.Fatal(err)
	}
	r := &run{RunContext: rc}
	cn, err := r.collNode(nodeLinkedPosts)
	if err != nil {
		t.Fatal(err)
	}
	post, err := cn.ChildPathObject(likedPost)
	if err != nil {
		t.Fatal(err)
	}
	if err := post.SetAttr(nodeattr.Type, nodeTypePrefix+collPost); err != nil {
		t.Fatal(err)
	}
	if err := r.retypeLinkedPosts(); err != nil {
		t.Fatal(err)
	}
	post, err = rc.Host.ObjectFromRef(post.PermanodeRef())
	if err != nil {
		t.Fatal(err)
	}
	if got := post.Attr(nodeattr.Type); got != nodeTypeLinkedPost {
		t.Errorf("type = %q; want %q", got, nodeTypeLinkedPost)
	}
	if got := rc.AccountNode().Attr(acctAttrLinkedPostsTyped); got != "true" {
		t.Errorf("%s = %q; want true", acctAttrLinkedPostsTyped, got)
	}
}
