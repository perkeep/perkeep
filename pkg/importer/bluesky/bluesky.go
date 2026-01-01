package bluesky // import "perkeep.org/pkg/importer/bluesky"

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	_ "github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/lex/util"
	"github.com/bluesky-social/indigo/repo"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/ipfs/boxo/blockstore"
	"github.com/ipfs/go-cid"
	"github.com/ipld/go-car"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
)

const (
	nodeType = "bluesky:%s"

	attrDID      = "did"
	attrRevision = "revision" // rev for &since=
	attrRootCID  = "rootcid"  // repo head CID for repo.DiffSince

	publicHost         = "https://public.api.bsky.app"
	urlDID             = publicHost + "/xrpc/com.atproto.identity.resolveHandle?handle=%s"
	urlInitialRepo     = "%s/xrpc/com.atproto.sync.getRepo?did=%s"
	urlIncrementalRepo = "%s/xrpc/com.atproto.sync.getRepo?did=%s&since=%s"
	urlPLCDirectory    = "https://plc.directory/%s"
)

func init() {
	importer.Register("bluesky", imp{})
}

type imp struct {
	importer.OAuth1 // for CallbackRequestAccount and CallbackURLParameters
}

func (imp) Properties() importer.Properties {
	return importer.Properties{
		Title:               "Bluesky",
		Description:         "Import posts from a Bluesky account",
		SupportsIncremental: true,
	}
}

func (imp) IsAccountReady(acct *importer.Object) (bool, error) {
	return acct.Attr(importer.AcctAttrUserName) != "", nil
}

func (imp) SummarizeAccount(acct *importer.Object) string {
	userID := acct.Attr(importer.AcctAttrUserName)
	if userID == "" {
		return "Not configured"
	}
	return userID
}

func (imp) ServeSetup(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) error {
	return tmpl.ExecuteTemplate(w, "serveSetup", ctx)
}

var tmpl = template.Must(template.New("root").Parse(`
{{define "serveSetup"}}
<h1>Configuring Bluesky Account</h1>
<form method="get" action="{{.CallbackURL}}">
	<input type="hidden" name="acct" value="{{.AccountNode.PermanodeRef}}">
	<!-- Add necessary fields for Bluesky authentication -->
	<table border=0 cellpadding=3>
	<tr>
		<td align=right>Username (e.g. perkeep.bsky.social, without @)</td>
		<td><input name="username" size=50 required></td>
	</tr>
	<tr><td align=right></td><td><input type="submit" value="Add"></td></tr>
	</table>
</form>
{{end}}
`))

func (im imp) AccountSetupHTML(host *importer.Host) string {
	return "<h1>Configuring Bluesky</h1>"
}

func (im imp) ServeCallback(w http.ResponseWriter, r *http.Request, ctx *importer.SetupContext) {
	username := r.FormValue("username")

	if username == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}

	resp, err := ctx.Host.HTTPClient().Get(fmt.Sprintf(urlDID, username))
	if err != nil {
		http.Error(w, fmt.Sprintf("Error fetching DID: %v", err), http.StatusInternalServerError)
		return
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error reading response body: %v", err), http.StatusInternalServerError)
		return
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf("resolveHandle: %s\n%s", resp.Status, body), 500)
		return
	}

	var data struct {
		DID string `json:"did"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		http.Error(w, fmt.Sprintf("Error unmarshalling response body: %v", err), http.StatusInternalServerError)
		return
	}
	if data.DID == "" {
		http.Error(w, "DID not found in response", http.StatusInternalServerError)
		return
	}

	if err := ctx.AccountNode.SetAttrs(
		nodeattr.Title, fmt.Sprintf("Bluesky account: %s", username),
		importer.AcctAttrUserName, username,
		attrDID, data.DID,
	); err != nil {
		http.Error(w, fmt.Sprintf("Error setting account attributes: %v", err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, ctx.AccountURL(), http.StatusFound)
}

func (im imp) Run(ctx *importer.RunContext) error {
	acctNode := ctx.AccountNode()
	r := &run{
		RunContext: ctx,
		did:        acctNode.Attr(attrDID),
		revision:   acctNode.Attr(attrRevision),
		pdsCache:   make(map[string]string),
	}

	return r.entrypoint()
}

type run struct {
	*importer.RunContext

	did      string
	revision string
	pdsCache map[string]string
}

/*
utility functions
*/

func (r *run) resolvePDS(did string) (string, error) {
	if pds, ok := r.pdsCache[did]; ok {
		return pds, nil
	}
	resp, err := r.Host.HTTPClient().Get(fmt.Sprintf(urlPLCDirectory, did))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var doc struct {
		Service []struct {
			ID string `json:"id"`
			EP string `json:"serviceEndpoint"`
		} `json:"service"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", err
	}
	for _, s := range doc.Service {
		if s.ID == "#atproto_pds" {
			r.pdsCache[did] = s.EP
			return s.EP, nil
		}
	}
	return "", errors.New("pds endpoint not found")
}

func (r *run) getRecord(cid, collection, did, rkey string) (*atproto.RepoGetRecord_Output, error) {
	host, err := r.resolvePDS(did)
	if err != nil {
		return nil, err
	}
	cli := &xrpc.Client{Host: host}
	return atproto.RepoGetRecord(r.Context(), cli, cid, collection, did, rkey)
}

func (r *run) getRecordRef(ref *atproto.RepoStrongRef) (*atproto.RepoGetRecord_Output, error) {
	did, collection, rkey, err := splitAtURI(ref.Uri)
	if err != nil {
		return nil, err
	}
	return r.getRecord(ref.Cid, collection, did, rkey)
}

// splitAtURI breaks "at://<did>/<collection>/<rkey>" into its parts.
func splitAtURI(uri string) (did, collection, rkey string, err error) {
	const prefix = "at://"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", "", fmt.Errorf("invalid at:// uri: %q", uri)
	}
	parts := strings.Split(strings.TrimPrefix(uri, prefix), "/")
	if len(parts) < 3 {
		return "", "", "", fmt.Errorf("incomplete at:// uri: %q", uri)
	}
	did = parts[0]
	rkey = parts[len(parts)-1]
	collection = strings.Join(parts[1:len(parts)-1], "/") // handles nested collections
	return
}

func (r *run) uploadFile(ctx context.Context, url string) (blob.Ref, error) {
	resp, err := r.Host.HTTPClient().Get(url)
	if err != nil {
		return blob.Ref{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return blob.Ref{}, fmt.Errorf("invalid status code fetching file: %v", resp.Status)
	}
	return schema.WriteFileFromReader(r.Context(), r.Host.Target(), filepath.Base(url), resp.Body)
}

func (r *run) finalizeNode(node *importer.Object, content any, attrs ...string) error {
	json, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal content: %v", err)
	}
	ref, err := schema.WriteFileFromReader(r.Context(), r.Host.Target(), "content.json", bytes.NewReader(json))
	if err != nil {
		return fmt.Errorf("failed to write content: %v", err)
	}
	// attrs = append(attrs, "camliPath:content.json", ref.String())
	attrs = append(attrs, nodeattr.CamliContent, ref.String())

	if err := node.SetAttrs(attrs...); err != nil {
		return fmt.Errorf("failed to set attrs: %v", err)
	}
	return nil
}

func (r *run) downloadLinkedBlob(ctx context.Context, did string, lblob *util.LexBlob, filename string) (blob.Ref, error) {
	host, err := r.resolvePDS(did)
	if err != nil {
		return blob.Ref{}, err
	}
	cli := &xrpc.Client{Host: host}
	var buf bytes.Buffer
	if err := cli.Do(ctx, xrpc.Query, "",
		"com.atproto.sync.getBlob",
		map[string]any{
			"did": did,
			"cid": lblob.Ref.String(), // ← multibase CID
		}, nil, &buf); err != nil {
		return blob.Ref{}, err
	}
	return schema.WriteFileFromReader(ctx, r.Host.Target(), filename, &buf)
}

// blockstoreNode returns the node used to back the KV store used for incremental imports.
func (r *run) blockstoreNode() (*importer.Object, error) {
	root := r.AccountNode()
	obj, err := root.ChildPathObject("carrepo")
	if err != nil {
		return nil, err
	}
	if obj.Attr(nodeattr.Title) == "" {
		if err := obj.SetAttr(nodeattr.Title, "Blockstore"); err != nil {
			return nil, err
		}
	}
	return obj, nil
}

/*
entrypoint
*/

func (r *run) entrypoint() error {
	ctx := r.Context()
	acctNode := r.AccountNode()

	// check the did
	if _, err := syntax.ParseDID(r.did); err != nil {
		return fmt.Errorf("failed to parse did: %v", err)
	}

	host, err := r.resolvePDS(r.did)
	if err != nil {
		return fmt.Errorf("failed to resolve PDS for %s: %v", r.did, err)
	}
	var importURL string
	if r.revision == "" {
		importURL = fmt.Sprintf(urlInitialRepo, host, r.did)
	} else {
		importURL = fmt.Sprintf(urlIncrementalRepo, host, r.did, r.revision)
	}

	log.Printf("Importing from %s", importURL)
	resp, err := r.Host.HTTPClient().Get(importURL)
	if err != nil {
		return fmt.Errorf("failed to fetch repo: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("invalid status code fetching repo: %v. Response body: %s", resp.Status, string(body))
	}

	repoRoot, err := r.blockstoreNode()
	if err != nil {
		return err
	}
	bs := blockstore.NewBlockstore(&Datastore{root: repoRoot, host: r.Host}, blockstore.NoPrefix())
	carHeader, err := car.LoadCar(ctx, bs, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to load car: %w", err)
	}
	newRoot := carHeader.Roots[0]

	// open repo at new head
	re, err := repo.OpenRepo(ctx, bs, newRoot)
	if err != nil {
		return fmt.Errorf("openRepo: %w", err)
	}

	var paths []string
	prev := acctNode.Attr(attrRootCID)

	if prev == "" {
		if err := re.ForEach(ctx, "", func(p string, _ cid.Cid) error {
			paths = append(paths, p)
			return nil
		}); err != nil {
			return fmt.Errorf("enumerate repo: %v", err)
		}
	} else {
		if prevCID, err := cid.Parse(prev); err == nil {
			if ops, err := re.DiffSince(ctx, prevCID); err == nil {
				for _, op := range ops {
					if op.Op == "delete" {
						continue // no local tombstone handling yet
					}
					paths = append(paths, op.Rpath)
				}
			} else {
				log.Printf("DiffSince failed, falling back to full scan: %v", err)
			}
		}
	}

	if len(paths) == 0 {
		log.Println("No new records to import")
		return nil
	}

	for _, k := range paths {
		log.Printf("Processing %s", k)
		_, rec, err := re.GetRecord(ctx, k)
		if err != nil {
			return err
		}

		parts := strings.SplitN(k, "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid key: %s", k)
		}
		rkey := parts[1]

		switch rec.(type) {
		case *bsky.ActorProfile:
			if rkey != "self" {
				return fmt.Errorf("expected rkey 'self' for actor profile, got %s", rkey)
			}
			continue // already handled separately
		case *bsky.FeedLike:
			if err := r.importLike(acctNode, rkey, rec.(*bsky.FeedLike)); err != nil {
				return err
			}
		case *bsky.FeedPost:
			if err := r.importPost(acctNode, r.did, rkey, rec.(*bsky.FeedPost)); err != nil {
				return err
			}
		case *bsky.FeedRepost:
			if err := r.importRepost(acctNode, r.did, rkey, rec.(*bsky.FeedRepost)); err != nil {
				return err
			}
		case *bsky.GraphFollow:
			if err := r.importFollow(acctNode, r.did, rkey, rec.(*bsky.GraphFollow)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown record type: %T", rec)
		}
	}

	if err := acctNode.SetAttrs(
		attrRevision, re.SignedCommit().Rev,
		attrRootCID, newRoot.String(),
	); err != nil {
		return fmt.Errorf("save checkpoints: %w", err)
	}

	return nil
}

/*
import functions
*/

func (r *run) importFollow(parent *importer.Object, did string, rkey string, follow *bsky.GraphFollow) error {
	log.Printf("Importing follow for %s", did)
	node, err := parent.ChildPathObject(rkey)
	if err != nil {
		return fmt.Errorf("failed to get child object for %s: %w", did, err)
	}

	profile, err := bsky.ActorGetProfile(r.Context(), &xrpc.Client{Host: publicHost}, follow.Subject)
	if err != nil {
		return fmt.Errorf("failed to get profile for %s: %w", follow.Subject, err)
	}
	if err := r.importProfile(node, follow.Subject, rkey, profile); err != nil {
		return err
	}

	if err := node.SetAttrs(
		attrDID, did,
		nodeattr.Title, "Follow: "+*profile.DisplayName,
		nodeattr.Type, fmt.Sprintf(nodeType, follow.LexiconTypeID),
	); err != nil {
		return fmt.Errorf("failed to set attrs for %s: %w", follow.Subject, err)
	}

	log.Printf("imported follow: %s", follow.Subject)
	return nil
}

func (r *run) importRepost(parent *importer.Object, did string, rkey string, repost *bsky.FeedRepost) error {
	log.Printf("importing repost: %s", rkey)
	node, err := parent.ChildPathObject(rkey)
	if err != nil {
		return fmt.Errorf("failed to get child object for %s: %w", did, err)
	}

	pdid, collection, prkey, err := splitAtURI(repost.Subject.Uri)
	if err != nil {
		return fmt.Errorf("failed to split URI: %w", err)
	}
	if collection != "app.bsky.feed.post" {
		return fmt.Errorf("expected collection to be 'app.bsky.feed.post', got %s", collection)
	}

	rec, err := r.getRecordRef(repost.Subject)
	if err != nil {
		return fmt.Errorf("failed to get record: %w", err)
	}

	post, ok := rec.Value.Val.(*bsky.FeedPost)
	if !ok {
		return fmt.Errorf("expected record to be a feed post, got %T", rec.Value.Val)
	}

	if err := r.importPost(node, pdid, prkey, post); err != nil {
		return err
	}

	attrs := []string{
		nodeattr.Title, fmt.Sprintf("Repost: %s", post.Text),
		nodeattr.Type, fmt.Sprintf(nodeType, repost.LexiconTypeID),
	}

	return r.finalizeNode(node, repost, attrs...)
}

func (r *run) importPost(parent *importer.Object, did string, rkey string, post *bsky.FeedPost) error {
	log.Printf("importing post: %s", rkey)
	var hasThumb bool
	var thumbRef blob.Ref
	node, err := parent.ChildPathObject(rkey)
	if err != nil {
		return fmt.Errorf("failed to get child object for %s: %w", did, err)
	}

	attrs := []string{
		nodeattr.Title, post.Text,
		nodeattr.Type, fmt.Sprintf(nodeType, post.LexiconTypeID),
	}

	if post.Embed != nil {
		if post.Embed.EmbedImages != nil {
			for i, img := range post.Embed.EmbedImages.Images {
				ref, err := r.downloadLinkedBlob(r.Context(), did, img.Image, fmt.Sprintf("EmbedImages_%d", i))
				if err != nil {
					return fmt.Errorf("failed to download thumbnail: %v", err)
				}

				hasThumb = true
				thumbRef = ref
				attrs = append(attrs, fmt.Sprintf("camliPath:EmbedImages/%d", i), ref.String())
			}
		}
		if post.Embed.EmbedVideo != nil {
			ref, err := r.downloadLinkedBlob(r.Context(), did, post.Embed.EmbedVideo.Video, "EmbedVideo_Video")
			if err != nil {
				return fmt.Errorf("failed to download video: %v", err)
			}
			attrs = append(attrs, "camliPath:EmbedVideo/Video", ref.String())
		}
		if post.Embed.EmbedExternal != nil {
			hasThumb = true
			thumbRef, err = r.downloadLinkedBlob(r.Context(), did, post.Embed.EmbedExternal.External.Thumb, "EmbedExternal_Thumb")
			if err != nil {
				return fmt.Errorf("failed to download thumbnail: %v", err)
			}

			attrs = append(attrs, "camliPath:EmbedExternal/Thumb", thumbRef.String())
		}
		if post.Embed.EmbedRecord != nil {
			return fmt.Errorf("embed record not supported")
		}
		if post.Embed.EmbedRecordWithMedia != nil {
			return fmt.Errorf("embed record with media not supported")
		}
	}

	if hasThumb {
		attrs = append(attrs, nodeattr.CamliContentImage, thumbRef.String())
	}

	return r.finalizeNode(node, post, attrs...)
}

func (r *run) importLike(parent *importer.Object, rkey string, like *bsky.FeedLike) error {
	log.Printf("importing like: %s", rkey)
	node, err := parent.ChildPathObject(rkey)
	if err != nil {
		return fmt.Errorf("failed to get child object: %w", err)
	}

	pdid, collection, prkey, err := splitAtURI(like.Subject.Uri)
	if err != nil {
		return fmt.Errorf("failed to split URI: %w", err)
	}
	if collection != "app.bsky.feed.post" {
		return fmt.Errorf("expected collection to be 'app.bsky.feed.post', got %s", collection)
	}

	rec, err := r.getRecordRef(like.Subject)
	if err != nil {
		return fmt.Errorf("failed to get record: %w", err)
	}

	post, ok := rec.Value.Val.(*bsky.FeedPost)
	if !ok {
		return fmt.Errorf("expected record to be a feed post, got %T", rec.Value.Val)
	}

	if err := r.importPost(node, pdid, prkey, post); err != nil {
		return err
	}

	attrs := []string{
		nodeattr.Title, fmt.Sprintf("Like: %s", post.Text),
		nodeattr.Type, fmt.Sprintf(nodeType, like.LexiconTypeID),
	}

	return r.finalizeNode(node, like, attrs...)
}

func (r *run) importProfile(parent *importer.Object, did string, rkey string, profile *bsky.ActorDefs_ProfileViewDetailed) error {
	log.Printf("Importing profile for %s", did)
	node, err := parent.ChildPathObject(rkey)
	if err != nil {
		return fmt.Errorf("failed to get child object for %s: %w", did, err)
	}

	attrs := []string{
		nodeattr.Title, profile.Handle,
		attrDID, did,
		nodeattr.GivenName, *profile.DisplayName,
		nodeattr.Type, fmt.Sprintf(nodeType, "app.bsky.actor.profile"),
	}

	if profile.Avatar != nil {
		ref, err := r.uploadFile(r.Context(), *profile.Avatar)
		if err != nil {
			return fmt.Errorf("failed to upload avatar: %w", err)
		}
		attrs = append(attrs, nodeattr.CamliContentImage, ref.String())
	}

	if profile.Banner != nil {
		ref, err := r.uploadFile(r.Context(), *profile.Banner)
		if err != nil {
			return fmt.Errorf("failed to upload banner: %w", err)
		}
		attrs = append(attrs, "banner", ref.String())
	}

	return r.finalizeNode(node, profile, attrs...)
}
