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

// Package bluesky implements an importer for Bluesky (atproto) accounts.
//
// It fetches the account's whole repository from its PDS as a CAR file
// (incrementally, after the first run) and imports every record in it.
// Each block of the repo (commits, MST nodes, and records) is also stored
// verbatim as a sha256 blob, so the Perkeep blobref of each block maps 1:1
// to its atproto CID.
//
// Posts, likes, reposts, follows and the profile get Perkeep-friendly
// permanodes; all other record types are imported generically. Liked and
// reposted posts and followed profiles are fetched from the public AppView.
//
// No authentication is required; only public data is imported.
package bluesky // import "perkeep.org/pkg/importer/bluesky"

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/repo"
	"github.com/bluesky-social/indigo/atproto/repo/mst"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/ipfs/go-cid"
	"github.com/ipld/go-car"
	"github.com/multiformats/go-multihash"
	"golang.org/x/sync/errgroup"

	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
)

const (
	// Account node attributes.
	acctAttrDID    = "did"
	acctAttrRev    = "repoRev"    // rev of the last fully imported commit; used for getRepo?since=
	acctAttrCommit = "repoCommit" // CID of the last fully imported commit

	// Attributes on imported nodes.
	attrURI        = "atURI"         // at:// URI of the record
	attrCID        = "atprotoCID"    // CID of the record (or blob); set last, so it marks the node as complete
	attrRecord     = "atprotoRecord" // blobref of the record's original DAG-CBOR bytes
	attrDID        = "did"
	attrHandle     = "handle"
	attrSubject    = "subject" // at:// URI (or DID, for follows) that a like, repost or follow refers to
	attrReplyTo    = "inReplyTo"
	attrQuote      = "quotedPost"
	attrLinkURL    = "linkURL"
	attrLinkTitle  = "linkTitle"
	attrBanner     = "banner"
	nodeTypePrefix = "bluesky:"

	// Children of the root node, in addition to one per collection NSID.
	nodeLinkedPosts = "linkedPosts" // posts by others that were liked or reposted, keyed by at:// URI
	nodeProfiles    = "profiles"    // followed accounts, keyed by DID

	appViewHost = "https://public.api.bsky.app"

	importConcurrency = 10
	hydrateBatchSize  = 25 // max URIs or actors per getPosts or getProfiles call
)

const (
	collPost    = "app.bsky.feed.post"
	collLike    = "app.bsky.feed.like"
	collRepost  = "app.bsky.feed.repost"
	collFollow  = "app.bsky.graph.follow"
	collProfile = "app.bsky.actor.profile"
)

// collectionTitles are the titles of the nodes for well-known collections.
// Other collections are titled by their NSID.
var collectionTitles = map[string]string{
	collPost:                     "Posts",
	collLike:                     "Likes",
	collRepost:                   "Reposts",
	collFollow:                   "Follows",
	collProfile:                  "Profile",
	"app.bsky.graph.block":       "Blocks",
	"app.bsky.graph.list":        "Lists",
	"app.bsky.graph.listitem":    "List items",
	"app.bsky.feed.threadgate":   "Thread gates",
	"app.bsky.feed.postgate":     "Post gates",
	"app.bsky.graph.starterpack": "Starter packs",
}

func init() {
	importer.Register("bluesky", &imp{})
}

type imp struct {
	importer.OAuth1 // for CallbackRequestAccount and CallbackURLParameters

	// dir, if non-nil, is used to resolve handles and DIDs instead of
	// identity.DefaultDirectory. For tests.
	dir identity.Directory
}

func (*imp) Properties() importer.Properties {
	return importer.Properties{
		Title:               "Bluesky",
		Description:         "import posts, likes, reposts and follows from a Bluesky (atproto) account",
		SupportsIncremental: true,
	}
}

func (im *imp) directory() identity.Directory {
	if im.dir != nil {
		return im.dir
	}
	return defaultDirectory()
}

var defaultDirectory = sync.OnceValue(identity.DefaultDirectory)

func (*imp) IsAccountReady(acct *importer.Object) (bool, error) {
	return acct.Attr(acctAttrDID) != "", nil
}

func (*imp) SummarizeAccount(acct *importer.Object) string {
	if acct.Attr(acctAttrDID) == "" {
		return "Not configured"
	}
	return "@" + acct.Attr(importer.AcctAttrUserName)
}

var tmpl = template.Must(template.New("root").Parse(`
{{define "serveSetup"}}
<h1>Configuring Bluesky account</h1>
<p>Only public data is imported, so no password is needed.</p>
<form method="get" action="{{.CallbackURL}}">
	<input type="hidden" name="acct" value="{{.AccountNode.PermanodeRef}}">
	<label>Handle or DID (e.g. perkeep.bsky.social) <input name="handle" size="40" required></label>
	<input type="submit" value="Add">
</form>
{{end}}
`))

func (*imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	return tmpl.ExecuteTemplate(w, "serveSetup", ctx)
}

func (im *imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	raw := strings.TrimPrefix(strings.TrimSpace(r.FormValue("handle")), "@")
	atid, err := syntax.ParseAtIdentifier(raw)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid handle or DID %q: %v", raw, err), http.StatusBadRequest)
		return
	}
	ident, err := im.directory().Lookup(ctx, atid)
	if err != nil {
		http.Error(w, fmt.Sprintf("looking up %s: %v", atid, err), http.StatusBadRequest)
		return
	}
	if err := ctx.AccountNode.SetAttrs(
		nodeattr.Title, fmt.Sprintf("Bluesky account @%s", ident.Handle),
		importer.AcctAttrUserName, ident.Handle.String(),
		acctAttrDID, ident.DID.String(),
	); err != nil {
		httputil.ServeError(w, r, err)
		return
	}
	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

func (im *imp) Run(rctx *importer.RunContext) error {
	ctx := rctx.Context()
	acct := rctx.AccountNode()
	did, err := syntax.ParseDID(acct.Attr(acctAttrDID))
	if err != nil {
		return fmt.Errorf("bluesky: account has invalid DID: %w", err)
	}
	dir := im.directory()
	ident, err := dir.LookupDID(ctx, did)
	if err != nil {
		return fmt.Errorf("bluesky: looking up %s: %w", did, err)
	}
	pds := ident.PDSEndpoint()
	if pds == "" {
		return fmt.Errorf("bluesky: %s has no PDS endpoint", did)
	}
	handle := ident.Handle.String()
	if acct.Attr(importer.AcctAttrUserName) != handle {
		if err := acct.SetAttrs(
			importer.AcctAttrUserName, handle,
			nodeattr.Title, fmt.Sprintf("Bluesky account @%s", handle),
		); err != nil {
			return err
		}
	}
	if err := rctx.RootNode().SetAttr(nodeattr.Title, fmt.Sprintf("Bluesky data for @%s", handle)); err != nil {
		return err
	}

	r := &run{
		RunContext: rctx,
		dir:        dir,
		did:        did,
		appView:    newClient(rctx, appViewHost),
		pdsClients: map[syntax.DID]*atclient.APIClient{did: newClient(rctx, pds)},
	}
	return r.importRepo(acct.Attr(acctAttrRev))
}

func newClient(rctx *importer.RunContext, host string) *atclient.APIClient {
	c := atclient.NewAPIClient(host)
	c.Client = rctx.Host.HTTPClient()
	c.Headers.Set("User-Agent", "Perkeep-bluesky-importer")
	return c
}

type run struct {
	*importer.RunContext
	dir     identity.Directory
	did     syntax.DID
	appView *atclient.APIClient

	mu         sync.Mutex
	pdsClients map[syntax.DID]*atclient.APIClient
	collNodes  map[string]*importer.Object // collection NSID (or nodeLinkedPosts, nodeProfiles) => node
}

// pdsClient returns a client for the PDS hosting did's repo.
func (r *run) pdsClient(did syntax.DID) (*atclient.APIClient, error) {
	r.mu.Lock()
	c, ok := r.pdsClients[did]
	r.mu.Unlock()
	if ok {
		return c, nil
	}
	ident, err := r.dir.LookupDID(r.Context(), did)
	if err != nil {
		return nil, err
	}
	pds := ident.PDSEndpoint()
	if pds == "" {
		return nil, fmt.Errorf("%s has no PDS endpoint", did)
	}
	c = newClient(r.RunContext, pds)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pdsClients[did] = c
	return c, nil
}

// collNode returns the child of the root node holding the named collection.
func (r *run) collNode(name string) (*importer.Object, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n, ok := r.collNodes[name]; ok {
		return n, nil
	}
	n, err := r.RootNode().ChildPathObject(name)
	if err != nil {
		return nil, err
	}
	title, ok := collectionTitles[name]
	switch {
	case name == nodeLinkedPosts:
		title = "Liked and reposted posts"
	case name == nodeProfiles:
		title = "Followed profiles"
	case !ok:
		title = name
	}
	if err := n.SetAttr(nodeattr.Title, title); err != nil {
		return nil, err
	}
	if r.collNodes == nil {
		r.collNodes = map[string]*importer.Object{}
	}
	r.collNodes[name] = n
	return n, nil
}

// recordNode returns the node for the record at path ("collection/rkey")
// in the account's own repo.
func (r *run) recordNode(path string) (*importer.Object, error) {
	coll, rkey, ok := strings.Cut(path, "/")
	if !ok {
		return nil, fmt.Errorf("invalid repo path %q", path)
	}
	cn, err := r.collNode(coll)
	if err != nil {
		return nil, err
	}
	return cn.ChildPathObject(rkey)
}

// postNode returns the node for the post with the given at:// URI: under
// the account's own posts if it's ours, else under nodeLinkedPosts.
func (r *run) postNode(uri syntax.ATURI) (*importer.Object, error) {
	if did, err := uri.Authority().AsDID(); err == nil && did == r.did {
		return r.recordNode(collPost + "/" + uri.RecordKey().String())
	}
	cn, err := r.collNode(nodeLinkedPosts)
	if err != nil {
		return nil, err
	}
	return cn.ChildPathObject(uri.String())
}

func (r *run) profileNode(did string) (*importer.Object, error) {
	cn, err := r.collNode(nodeProfiles)
	if err != nil {
		return nil, err
	}
	return cn.ChildPathObject(did)
}

// repoRecord is a record from the account's repo.
type repoRecord struct {
	path string // "collection/rkey"
	cid  cid.Cid
	data []byte // DAG-CBOR
}

func (rec repoRecord) collection() string {
	coll, _, _ := strings.Cut(rec.path, "/")
	return coll
}

func (rec repoRecord) uri(did syntax.DID) string {
	return "at://" + did.String() + "/" + rec.path
}

// importRepo fetches the repo (just the blocks changed since rev, if
// non-empty) and imports its new records.
func (r *run) importRepo(since string) error {
	ctx := r.Context()
	pds, err := r.pdsClient(r.did)
	if err != nil {
		return err
	}
	log.Printf("bluesky: fetching repo of %s from %s (since %q)", r.did, pds.Host, since)
	carBytes, err := atproto.SyncGetRepo(ctx, pds, r.did.String(), since)
	if err != nil {
		return fmt.Errorf("bluesky: getRepo: %w", err)
	}
	commitCID, commit, tree, blocks, err := r.loadCAR(carBytes)
	if errors.Is(err, errNoCommitBlock) && since != "" && commitCID.String() == r.AccountNode().Attr(acctAttrCommit) {
		// When nothing has changed since the last import, the PDS
		// returns just a CAR header naming the same commit.
		log.Printf("bluesky: repo of %s unchanged since rev %s", r.did, since)
		return nil
	}
	if err != nil {
		return fmt.Errorf("bluesky: loading repo CAR: %w", err)
	}
	if commit.DID != r.did.String() {
		return fmt.Errorf("bluesky: repo commit is for %s, not %s", commit.DID, r.did)
	}

	// The tree is partial when fetched with since: subtrees unchanged
	// since then aren't in the CAR, nor are their records. And records
	// still in the CAR that we've already imported are skipped by
	// importRecord. So it suffices to import all records in the CAR.
	var recs []repoRecord
	if err := tree.Walk(func(key []byte, val cid.Cid) error {
		if blk, ok := blocks[val.KeyString()]; ok {
			recs = append(recs, repoRecord{path: string(key), cid: val, data: blk})
		}
		return nil
	}); err != nil {
		return fmt.Errorf("bluesky: walking repo: %w", err)
	}
	log.Printf("bluesky: repo of %s at rev %s has %d new or changed records", r.did, commit.Rev, len(recs))

	h := &hydrator{
		posts:    map[string][]pendingRef{},
		profiles: map[string][]pendingRef{},
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(importConcurrency)
	for _, rec := range recs {
		if gctx.Err() != nil {
			break
		}
		g.Go(func() error {
			if err := r.importRecord(rec, h); err != nil {
				return fmt.Errorf("bluesky: importing %s: %w", rec.uri(r.did), err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.hydratePosts(h.posts); err != nil {
		return err
	}
	if err := r.hydrateProfiles(h.profiles); err != nil {
		return err
	}

	return r.AccountNode().SetAttrs(
		acctAttrRev, commit.Rev,
		acctAttrCommit, commitCID.String(),
	)
}

// errNoCommitBlock is returned by loadCAR, along with the root CID, when
// the CAR doesn't contain its root commit block.
var errNoCommitBlock = errors.New("commit block missing from CAR")

// loadCAR parses a repo CAR file, uploads all its blocks as sha256 blobs,
// and returns its commit and (possibly partial) MST, along with the CAR's
// blocks keyed by cid.Cid.KeyString.
func (r *run) loadCAR(carBytes []byte) (cid.Cid, *repo.Commit, *mst.Tree, map[string][]byte, error) {
	ctx := r.Context()
	cr, err := car.NewCarReader(bytes.NewReader(carBytes))
	if err != nil {
		return cid.Undef, nil, nil, nil, err
	}
	if cr.Header.Version != 1 || len(cr.Header.Roots) < 1 {
		return cid.Undef, nil, nil, nil, fmt.Errorf("unsupported CAR header %+v", cr.Header)
	}
	commitCID := cr.Header.Roots[0]

	bs := repo.NewTinyBlockstore()
	blocks := map[string][]byte{}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(importConcurrency)
	for {
		blk, err := cr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return cid.Undef, nil, nil, nil, err
		}
		if err := bs.Put(ctx, blk); err != nil {
			return cid.Undef, nil, nil, nil, err
		}
		blocks[blk.Cid().KeyString()] = blk.RawData()
		g.Go(func() error {
			return r.putBlock(gctx, blk.Cid(), blk.RawData())
		})
	}
	if err := g.Wait(); err != nil {
		return cid.Undef, nil, nil, nil, fmt.Errorf("storing blocks: %w", err)
	}

	commitBlk, ok := blocks[commitCID.KeyString()]
	if !ok {
		return commitCID, nil, nil, nil, errNoCommitBlock
	}
	var commit repo.Commit
	if err := commit.UnmarshalCBOR(bytes.NewReader(commitBlk)); err != nil {
		return cid.Undef, nil, nil, nil, fmt.Errorf("parsing commit: %w", err)
	}
	if err := commit.VerifyStructure(); err != nil {
		return cid.Undef, nil, nil, nil, err
	}
	tree, err := mst.LoadTreeFromStore(ctx, bs, commit.Data)
	if err != nil {
		return cid.Undef, nil, nil, nil, fmt.Errorf("loading MST: %w", err)
	}
	return commitCID, &commit, tree, blocks, nil
}

// blobRefOfCID returns the Perkeep blobref with the same digest as c, if
// c uses sha2-256 (as all atproto CIDs do).
func blobRefOfCID(c cid.Cid) (blob.Ref, bool) {
	dmh, err := multihash.Decode(c.Hash())
	if err != nil || dmh.Code != multihash.SHA2_256 {
		return blob.Ref{}, false
	}
	return blob.Parse("sha256-" + hex.EncodeToString(dmh.Digest))
}

// putBlock stores a repo block as a blob, under the same digest as its CID.
func (r *run) putBlock(ctx context.Context, c cid.Cid, data []byte) error {
	br, ok := blobRefOfCID(c)
	if !ok {
		return fmt.Errorf("block %s isn't sha2-256", c)
	}
	_, err := blobserver.Receive(ctx, r.Host.Target(), br, bytes.NewReader(data))
	return err
}

// hydrator collects the posts and profiles referenced by likes, reposts
// and follows, to be fetched from the AppView in batches once all repo
// records are imported.
type hydrator struct {
	mu       sync.Mutex
	posts    map[string][]pendingRef // at:// URI => likes and reposts of it
	profiles map[string][]pendingRef // DID => follows of it
}

// pendingRef is a like, repost or follow node waiting for its subject to
// be imported. Its attrCID isn't set until then, so it's retried if the
// import is interrupted.
type pendingRef struct {
	n   *importer.Object
	cid string // of the like, repost or follow record
}

func (h *hydrator) addPost(uri string, p pendingRef) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.posts[uri] = append(h.posts[uri], p)
}

func (h *hydrator) addProfile(did string, p pendingRef) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.profiles[did] = append(h.profiles[did], p)
}

// importRecord imports one record from the account's repo, unless it was
// already imported.
func (r *run) importRecord(rec repoRecord, h *hydrator) error {
	if err := r.Context().Err(); err != nil {
		return err
	}
	n, err := r.recordNode(rec.path)
	if err != nil {
		return err
	}
	if n.Attr(attrCID) == rec.cid.String() {
		return nil
	}
	recRef, _ := blobRefOfCID(rec.cid)
	attrs := []string{
		nodeattr.Type, nodeTypePrefix + rec.collection(),
		attrURI, rec.uri(r.did),
		attrRecord, recRef.String(),
	}

	var val any
	if dv, err := lexutil.CborDecodeValue(rec.data); err == nil {
		val = dv
	} else if !errors.Is(err, lexutil.ErrUnrecognizedType) {
		log.Printf("bluesky: decoding %s: %v; importing generically", rec.uri(r.did), err)
	}

	var more []string
	pending := false // whether attrCID is set later, by hydration
	pr := pendingRef{n, rec.cid.String()}
	switch v := val.(type) {
	case *bsky.FeedPost:
		more, err = r.postAttrs(n, r.did, v)
	case *bsky.FeedLike:
		more, pending = r.subjectAttrs(pr, v.CreatedAt, v.Subject, h)
	case *bsky.FeedRepost:
		more, pending = r.subjectAttrs(pr, v.CreatedAt, v.Subject, h)
	case *bsky.GraphFollow:
		more = append(dateAttrs(v.CreatedAt), attrSubject, v.Subject)
		h.addProfile(v.Subject, pr)
		pending = true
	case *bsky.ActorProfile:
		more, err = r.profileRecordAttrs(n, v)
	default:
		// TODO: titles, dates and media for more record types.
	}
	if err != nil {
		return err
	}
	attrs = append(attrs, more...)

	if !slices.Contains(attrs, nodeattr.CamliContent) {
		obj, err := atdata.UnmarshalCBOR(rec.data)
		if err != nil {
			return fmt.Errorf("decoding record: %w", err)
		}
		ref, err := r.writeJSON(obj)
		if err != nil {
			return err
		}
		attrs = append(attrs, nodeattr.CamliContent, ref.String())
	}
	if !slices.Contains(attrs, nodeattr.Title) {
		attrs = append(attrs, nodeattr.Title, collectionTitle(rec.collection()))
	}
	if !pending {
		attrs = append(attrs, attrCID, rec.cid.String())
	}
	return n.SetAttrs(attrs...)
}

func collectionTitle(coll string) string {
	if t, ok := collectionTitles[coll]; ok {
		return strings.TrimSuffix(t, "s")
	}
	return coll
}

// writeJSON stores v (typically an atproto record in its JSON form) as a
// file named "record.json".
func (r *run) writeJSON(v any) (blob.Ref, error) {
	j, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return blob.Ref{}, err
	}
	return schema.WriteFileFromReader(r.Context(), r.Host.Target(), "record.json", bytes.NewReader(j))
}

func dateAttrs(createdAt string) []string {
	dt, err := syntax.ParseDatetimeLenient(createdAt)
	if err != nil {
		return nil
	}
	return []string{nodeattr.StartDate, schema.RFC3339FromTime(dt.Time())}
}

// subjectAttrs returns the attributes for a like or repost of subject, and
// arranges for the subject to be hydrated if it's a post. Likes of other
// things (feed generators, lists, ...) just record the subject URI.
func (r *run) subjectAttrs(p pendingRef, createdAt string, subject *atproto.RepoStrongRef, h *hydrator) (attrs []string, pending bool) {
	attrs = dateAttrs(createdAt)
	if subject == nil {
		return attrs, false
	}
	attrs = append(attrs, attrSubject, subject.Uri)
	if uri, err := syntax.ParseATURI(subject.Uri); err == nil && uri.Collection() == collPost {
		h.addPost(subject.Uri, p)
		return attrs, true
	}
	return attrs, false
}

// postAttrs returns the attributes for post node n of a post by did,
// downloading its media as children of n.
func (r *run) postAttrs(n *importer.Object, did syntax.DID, post *bsky.FeedPost) ([]string, error) {
	attrs := dateAttrs(post.CreatedAt)
	if post.Text != "" {
		attrs = append(attrs,
			nodeattr.Title, post.Text,
			nodeattr.Content, post.Text,
		)
	}
	if post.Reply != nil && post.Reply.Parent != nil {
		attrs = append(attrs, attrReplyTo, post.Reply.Parent.Uri)
	}
	if e := post.Embed; e != nil {
		var quoted *bsky.EmbedRecord
		if e.EmbedRecord != nil {
			quoted = e.EmbedRecord
		} else if e.EmbedRecordWithMedia != nil {
			quoted = e.EmbedRecordWithMedia.Record
		}
		if quoted != nil && quoted.Record != nil {
			attrs = append(attrs, attrQuote, quoted.Record.Uri)
		}
		if ext := externalOf(e); ext != nil {
			attrs = append(attrs, attrLinkURL, ext.Uri)
			if ext.Title != "" {
				attrs = append(attrs, attrLinkTitle, ext.Title)
			}
		}
	}

	for i, m := range postMedia(post) {
		name := fmt.Sprintf("%s%d", m.kind, i+1)
		ref, err := r.importMedia(n, name, did, m)
		if err != nil {
			// Don't fail the whole import for one missing blob
			// (e.g. a deleted account's PDS is gone).
			log.Printf("bluesky: fetching %s of post %s: %v", name, n.PermanodeRef(), err)
			continue
		}
		if i == 0 && m.kind != "video" {
			attrs = append(attrs, nodeattr.CamliContentImage, ref.String())
		}
	}
	return attrs, nil
}

// media is an image or video attached to a post.
type media struct {
	kind string // "image", "video" or "thumb"
	blob *lexutil.LexBlob
	alt  string
}

func externalOf(e *bsky.FeedPost_Embed) *bsky.EmbedExternal_External {
	if e.EmbedExternal != nil {
		return e.EmbedExternal.External
	}
	if e.EmbedRecordWithMedia != nil && e.EmbedRecordWithMedia.Media != nil && e.EmbedRecordWithMedia.Media.EmbedExternal != nil {
		return e.EmbedRecordWithMedia.Media.EmbedExternal.External
	}
	return nil
}

// postMedia returns the media embedded in post, including in quote posts
// with media.
func postMedia(post *bsky.FeedPost) []media {
	e := post.Embed
	if e == nil {
		return nil
	}
	var images *bsky.EmbedImages
	var video *bsky.EmbedVideo
	var gallery *bsky.EmbedGallery
	switch {
	case e.EmbedImages != nil:
		images = e.EmbedImages
	case e.EmbedVideo != nil:
		video = e.EmbedVideo
	case e.EmbedGallery != nil:
		gallery = e.EmbedGallery
	case e.EmbedRecordWithMedia != nil && e.EmbedRecordWithMedia.Media != nil:
		m := e.EmbedRecordWithMedia.Media
		images, video, gallery = m.EmbedImages, m.EmbedVideo, m.EmbedGallery
	}
	var ms []media
	if images != nil {
		for _, img := range images.Images {
			if img != nil && img.Image != nil {
				ms = append(ms, media{kind: "image", blob: img.Image, alt: img.Alt})
			}
		}
	}
	if gallery != nil {
		for _, it := range gallery.Items {
			if img := it.EmbedGallery_Image; img != nil && img.Image != nil {
				ms = append(ms, media{kind: "image", blob: img.Image, alt: img.Alt})
			}
		}
	}
	if video != nil && video.Video != nil {
		var alt string
		if video.Alt != nil {
			alt = *video.Alt
		}
		ms = append(ms, media{kind: "video", blob: video.Video, alt: alt})
	}
	if ext := externalOf(e); ext != nil && ext.Thumb != nil {
		ms = append(ms, media{kind: "thumb", blob: ext.Thumb})
	}
	return ms
}

var extOfMIME = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
	"video/mp4":  ".mp4",
}

// importMedia downloads the blob m from did's PDS and stores it as the
// content of the child node of parent with the given name, with the alt
// text as the child's description. It returns the file's blobref.
func (r *run) importMedia(parent *importer.Object, name string, did syntax.DID, m media) (blob.Ref, error) {
	n, err := parent.ChildPathObject(name)
	if err != nil {
		return blob.Ref{}, err
	}
	blobCID := m.blob.Ref.String()
	if n.Attr(attrCID) == blobCID {
		if ref, ok := blob.Parse(n.Attr(nodeattr.CamliContent)); ok {
			return ref, nil
		}
	}
	pds, err := r.pdsClient(did)
	if err != nil {
		return blob.Ref{}, err
	}
	data, err := atproto.SyncGetBlob(r.Context(), pds, blobCID, did.String())
	if err != nil {
		return blob.Ref{}, err
	}
	ref, err := schema.WriteFileFromReader(r.Context(), r.Host.Target(), name+extOfMIME[m.blob.MimeType], bytes.NewReader(data))
	if err != nil {
		return blob.Ref{}, err
	}
	attrs := []string{
		nodeattr.Type, nodeTypePrefix + m.kind,
		nodeattr.CamliContent, ref.String(),
	}
	if m.alt != "" {
		attrs = append(attrs, nodeattr.Description, m.alt)
	}
	attrs = append(attrs, attrCID, blobCID)
	return ref, n.SetAttrs(attrs...)
}

// profileRecordAttrs returns the attributes for the account's own profile
// record node n.
func (r *run) profileRecordAttrs(n *importer.Object, p *bsky.ActorProfile) ([]string, error) {
	var attrs []string
	if p.DisplayName != nil && *p.DisplayName != "" {
		attrs = append(attrs, nodeattr.Title, *p.DisplayName)
	}
	if p.Description != nil {
		attrs = append(attrs, nodeattr.Description, *p.Description)
	}
	if p.Website != nil {
		attrs = append(attrs, nodeattr.URL, *p.Website)
	}
	for _, m := range []media{{kind: "avatar", blob: p.Avatar}, {kind: "banner", blob: p.Banner}} {
		if m.blob == nil {
			continue
		}
		ref, err := r.importMedia(n, m.kind, r.did, m)
		if err != nil {
			log.Printf("bluesky: fetching profile %s: %v", m.kind, err)
			continue
		}
		if m.kind == "avatar" {
			attrs = append(attrs, nodeattr.CamliContentImage, ref.String())
		}
	}
	return attrs, nil
}

// hydratePosts fetches the given posts from the AppView, imports them,
// and points the likes and reposts of them at them.
func (r *run) hydratePosts(posts map[string][]pendingRef) error {
	uris := make([]string, 0, len(posts))
	for uri := range posts {
		uris = append(uris, uri)
	}
	slices.Sort(uris)
	log.Printf("bluesky: fetching %d liked or reposted posts", len(uris))
	for batch := range slices.Chunk(uris, hydrateBatchSize) {
		if err := r.Context().Err(); err != nil {
			return err
		}
		out, err := bsky.FeedGetPosts(r.Context(), r.appView, batch)
		if err != nil {
			return fmt.Errorf("bluesky: getPosts: %w", err)
		}
		found := map[string]bool{}
		g, _ := errgroup.WithContext(r.Context())
		g.SetLimit(importConcurrency)
		for _, pv := range out.Posts {
			if pv == nil || posts[pv.Uri] == nil {
				continue
			}
			found[pv.Uri] = true
			g.Go(func() error {
				if err := r.importPostView(pv, posts[pv.Uri]); err != nil {
					return fmt.Errorf("bluesky: importing %s: %w", pv.Uri, err)
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return err
		}
		for _, uri := range batch {
			if !found[uri] {
				// Deleted, or its author is gone or private. The like
				// or repost node still has the subject URI.
				log.Printf("bluesky: post %s not found", uri)
				if err := markDone(posts[uri]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// importPostView imports a post by someone else as returned by the
// AppView, and sets it as the content of the given like and repost nodes.
func (r *run) importPostView(pv *bsky.FeedDefs_PostView, refs []pendingRef) error {
	uri, err := syntax.ParseATURI(pv.Uri)
	if err != nil {
		return err
	}
	n, err := r.postNode(uri)
	if err != nil {
		return err
	}
	if n.Attr(attrCID) != pv.Cid {
		post, ok := pv.Record.Val.(*bsky.FeedPost)
		if !ok {
			return fmt.Errorf("record is a %T, not a post", pv.Record.Val)
		}
		did, err := uri.Authority().AsDID()
		if err != nil {
			return err
		}
		attrs := []string{
			nodeattr.Type, nodeTypePrefix + collPost,
			attrURI, pv.Uri,
			nodeattr.URL, fmt.Sprintf("https://bsky.app/profile/%s/post/%s", did, uri.RecordKey()),
		}
		if a := pv.Author; a != nil {
			attrs = append(attrs, attrDID, a.Did, attrHandle, a.Handle)
		}
		more, err := r.postAttrs(n, did, post)
		if err != nil {
			return err
		}
		attrs = append(attrs, more...)
		ref, err := r.writeJSON(pv.Record)
		if err != nil {
			return err
		}
		attrs = append(attrs,
			nodeattr.CamliContent, ref.String(),
			attrCID, pv.Cid,
		)
		if err := n.SetAttrs(attrs...); err != nil {
			return err
		}
	}
	return setContentAndTitle(refs, n)
}

// setContentAndTitle makes each pending like, repost or follow node have
// target as its content, titled after it, and marks it done.
func setContentAndTitle(refs []pendingRef, target *importer.Object) error {
	for _, p := range refs {
		var prefix string
		switch p.n.Attr(nodeattr.Type) {
		case nodeTypePrefix + collLike:
			prefix = "Like: "
		case nodeTypePrefix + collRepost:
			prefix = "Repost: "
		case nodeTypePrefix + collFollow:
			prefix = "Follow: "
		}
		if err := p.n.SetAttrs(
			nodeattr.CamliContent, target.PermanodeRef().String(),
			nodeattr.Title, prefix+target.Attr(nodeattr.Title),
			attrCID, p.cid,
		); err != nil {
			return err
		}
	}
	return nil
}

// markDone marks pending nodes whose subject couldn't be found as done.
func markDone(refs []pendingRef) error {
	for _, p := range refs {
		if err := p.n.SetAttr(attrCID, p.cid); err != nil {
			return err
		}
	}
	return nil
}

// hydrateProfiles fetches the given profiles from the AppView, imports
// them, and points the follows of them at them.
func (r *run) hydrateProfiles(profiles map[string][]pendingRef) error {
	dids := make([]string, 0, len(profiles))
	for did := range profiles {
		dids = append(dids, did)
	}
	slices.Sort(dids)
	log.Printf("bluesky: fetching %d followed profiles", len(dids))
	for batch := range slices.Chunk(dids, hydrateBatchSize) {
		if err := r.Context().Err(); err != nil {
			return err
		}
		out, err := bsky.ActorGetProfiles(r.Context(), r.appView, batch)
		if err != nil {
			return fmt.Errorf("bluesky: getProfiles: %w", err)
		}
		found := map[string]bool{}
		g, _ := errgroup.WithContext(r.Context())
		g.SetLimit(importConcurrency)
		for _, p := range out.Profiles {
			if p == nil || profiles[p.Did] == nil {
				continue
			}
			found[p.Did] = true
			g.Go(func() error {
				if err := r.importProfileView(p, profiles[p.Did]); err != nil {
					return fmt.Errorf("bluesky: importing profile %s: %w", p.Did, err)
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return err
		}
		for _, did := range batch {
			if !found[did] {
				log.Printf("bluesky: profile %s not found", did)
				if err := markDone(profiles[did]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (r *run) importProfileView(p *bsky.ActorDefs_ProfileViewDetailed, follows []pendingRef) error {
	n, err := r.profileNode(p.Did)
	if err != nil {
		return err
	}
	title := "@" + p.Handle
	if p.DisplayName != nil && *p.DisplayName != "" {
		title = *p.DisplayName + " (@" + p.Handle + ")"
	}
	attrs := []string{
		nodeattr.Type, nodeTypePrefix + "profile",
		nodeattr.Title, title,
		nodeattr.URL, "https://bsky.app/profile/" + p.Did,
		attrDID, p.Did,
		attrHandle, p.Handle,
	}
	if p.DisplayName != nil {
		attrs = append(attrs, importer.AcctAttrName, *p.DisplayName)
	}
	if p.Description != nil {
		attrs = append(attrs, nodeattr.Description, *p.Description)
	}
	// The AppView only gives CDN URLs for the avatar and banner. They
	// rarely change, so only fetch them the first time.
	if p.Avatar != nil && n.Attr(nodeattr.CamliContentImage) == "" {
		ref, err := r.fetchURL(*p.Avatar, "avatar.jpg")
		if err != nil {
			log.Printf("bluesky: fetching avatar of %s: %v", p.Did, err)
		} else {
			attrs = append(attrs, nodeattr.CamliContentImage, ref.String())
		}
	}
	if p.Banner != nil && n.Attr(attrBanner) == "" {
		ref, err := r.fetchURL(*p.Banner, "banner.jpg")
		if err != nil {
			log.Printf("bluesky: fetching banner of %s: %v", p.Did, err)
		} else {
			attrs = append(attrs, attrBanner, ref.String())
		}
	}
	ref, err := r.writeJSON(p)
	if err != nil {
		return err
	}
	attrs = append(attrs, nodeattr.CamliContent, ref.String())
	if err := n.SetAttrs(attrs...); err != nil {
		return err
	}
	return setContentAndTitle(follows, n)
}

func (r *run) fetchURL(url, filename string) (blob.Ref, error) {
	req, err := http.NewRequestWithContext(r.Context(), "GET", url, nil)
	if err != nil {
		return blob.Ref{}, err
	}
	res, err := r.Host.HTTPClient().Do(req)
	if err != nil {
		return blob.Ref{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return blob.Ref{}, fmt.Errorf("fetching %s: %v", url, res.Status)
	}
	return schema.WriteFileFromReader(r.Context(), r.Host.Target(), filename, res.Body)
}
