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

package mastodon

import (
	"context"
	"fmt"
	"slices"

	"golang.org/x/sync/errgroup"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/progress"
	"perkeep.org/pkg/schema/nodeattr"
)

// profileOptionalAttrs are the attributes of a profile's node that it may
// lack, as statusOptionalAttrs are of a status's. Its fields' attributes
// are too.
var profileOptionalAttrs = []string{
	nodeattr.Description,
	nodeattr.CamliContentImage,
	attrContentHTML,
	attrAvatarURL,
	attrHeader,
	attrHeaderURL,
	attrLegacyURI,
}

// A profileImport is an account's profile to import.
type profileImport struct {
	a    *account
	own  bool // whether it's the run's account's
	node *importer.Object

	avatar, header blob.Ref // files of its images, if any
}

// importProfile imports the account's own profile.
func (r *run) importProfile() error {
	ctx := r.Context()
	a, err := r.api.verifyCredentials(ctx)
	if err != nil {
		return err
	}
	batch := r.Host.NewClaimBatch()
	nodes, err := r.childNodes(ctx, batch, r.RootNode(), []string{nodeProfile})
	if err != nil {
		return err
	}
	n := nodes[0]
	pi := &profileImport{a: a, own: true, node: n}
	if err := r.prepareProfile(ctx, pi); err != nil {
		return err
	}
	r.addProfile(batch, pi)
	if err := batch.Commit(ctx); err != nil {
		return err
	}
	if batch.Changed(n) {
		r.Logf("Imported profile of %s to %s", r.userAddress, n.PermanodeRef())
	}
	return nil
}

// importFollowing imports the accounts the account follows, a page at a
// time, most recently followed first, stopping at a page that's unchanged
// if the run's incremental.
func (r *run) importFollowing() error {
	r.Logf("Importing follows of %s", r.userAddress)
	bar := r.Progress().Bar("following", "Importing follows")
	for page := r.api.followingURL(r.userID); page != ""; {
		if err := r.Context().Err(); err != nil {
			return err
		}
		as, next, err := r.api.accounts(r.Context(), page)
		if err != nil {
			return err
		}
		if len(as) == 0 {
			break
		}
		changed, err := r.importFollowingPage(as, bar)
		if err != nil {
			return err
		}
		if r.incremental && !changed {
			r.Logf("reached the follows imported before")
			break
		}
		page = next
	}
	bar.Finish()
	return nil
}

// importFollowingPage imports as, a page of the accounts the account
// follows: their profiles, children of the run's accounts node, and
// follows of them, of its following node. It reports whether it changed
// any.
func (r *run) importFollowingPage(as []*account, bar *progress.Bar) (changed bool, err error) {
	ctx := r.Context()
	as = slices.DeleteFunc(slices.Clone(as), func(a *account) bool { return a.actor() == "" })
	actors := make([]string, len(as))
	for i, a := range as {
		actors[i] = a.actor()
	}
	batch := r.Host.NewClaimBatch()
	profiles, err := r.childNodes(ctx, batch, r.accounts, actors)
	if err != nil {
		return false, err
	}
	follows, err := r.childNodes(ctx, batch, r.following, actors)
	if err != nil {
		return false, err
	}
	pis := make([]*profileImport, len(as))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(statusesAtOnce)
	for i, a := range as {
		pis[i] = &profileImport{a: a, node: profiles[i]}
		g.Go(func() error { return r.prepareProfile(gctx, pis[i]) })
	}
	if err := g.Wait(); err != nil {
		return false, err
	}

	for i, pi := range pis {
		r.addProfile(batch, pi)
		setAttrs(batch, follows[i], []importer.KeyValue{
			importer.KV(nodeattr.Type, nodeTypeFollow),
			importer.KV(nodeattr.CamliContent, pi.node.PermanodeRef().String()),
			importer.KV(attrSubject, pi.a.actor()),
			importer.KV(nodeattr.Title, "Follow: "+r.profileTitle(pi.a)),
		}, nil)
	}
	if err := batch.Commit(ctx); err != nil {
		return false, err
	}
	for i, pi := range pis {
		if batch.Changed(pi.node) || batch.Changed(follows[i]) {
			changed = true
			bar.Add(1)
			r.Logf("Imported follow of %s to %s", pi.a.actor(), follows[i].PermanodeRef())
		} else {
			bar.Skip(1)
		}
	}
	return changed, nil
}

// prepareProfile fetches the images of pi's profile that its node lacks:
// its avatar, and its header, if it's the account's own.
func (r *run) prepareProfile(ctx context.Context, pi *profileImport) error {
	var err error
	if pi.avatar, err = r.profileImage(ctx, pi, pi.a.Avatar, attrAvatarURL, nodeattr.CamliContentImage); err != nil {
		return err
	}
	if pi.own {
		if pi.header, err = r.profileImage(ctx, pi, pi.a.Header, attrHeaderURL, attrHeader); err != nil {
			return err
		}
	}
	return nil
}

// profileImage returns the file of the image at rawURL, of pi's profile:
// its node's attribute key's, if its attribute urlKey says that's of
// rawURL, or else fetched. It returns a zero ref if there's none, or it
// can't be fetched, as skippable says.
func (r *run) profileImage(ctx context.Context, pi *profileImport, rawURL, urlKey, key string) (blob.Ref, error) {
	if rawURL == "" {
		return blob.Ref{}, nil
	}
	if pi.node.Attr(urlKey) == rawURL {
		if br, ok := blob.Parse(pi.node.Attr(key)); ok {
			return br, nil
		}
	}
	br, err := r.fetchFile(ctx, rawURL)
	if r.skippable(ctx, err, pi.own) {
		r.Logf("skipping profile image %s: %v", rawURL, err)
		return blob.Ref{}, nil
	}
	return br, err
}

// profileTitle returns the title of the profile of a.
func (r *run) profileTitle(a *account) string {
	name := a.DisplayName
	if name == "" {
		name = a.Username
	}
	return fmt.Sprintf("%s (@%s)", name, r.qualified(a.Acct))
}

// addProfile adds setting the attributes of pi's profile to batch.
func (r *run) addProfile(batch *importer.ClaimBatch, pi *profileImport) {
	a := pi.a
	note := htmlText(a.Note)
	if pi.own && a.Source != nil {
		note = a.Source.Note
	}
	nodeType := nodeTypeLinkedProfile
	if pi.own {
		nodeType = nodeTypeProfile
	}
	attrs := []importer.KeyValue{
		importer.KV(nodeattr.Type, nodeType),
		importer.KV(attrID, a.ID),
		importer.KV(attrActivityPubID, a.actor()),
		importer.KV(nodeattr.URL, a.URL),
		importer.KV(nodeattr.Title, r.profileTitle(a)),
		importer.KV(attrAccount, r.qualified(a.Acct)),
		importer.KV(nodeattr.Description, note),
	}
	if a.Note != plainHTML(note) {
		attrs = append(attrs, importer.KV(attrContentHTML, a.Note))
	}
	if pi.avatar.Valid() {
		attrs = append(attrs,
			importer.KV(nodeattr.CamliContentImage, pi.avatar.String()),
			importer.KV(attrAvatarURL, a.Avatar),
		)
	}
	if pi.header.Valid() {
		attrs = append(attrs,
			importer.KV(attrHeader, pi.header.String()),
			importer.KV(attrHeaderURL, a.Header),
		)
	}
	if pi.own && a.Source != nil {
		for _, f := range a.Source.Fields {
			attrs = append(attrs, importer.KV(attrFieldPrefix+f.Name, f.Value))
		}
	}
	setAttrs(batch, pi.node, attrs, profileOptionalAttrs, attrFieldPrefix)
}
