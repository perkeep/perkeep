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
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/sync/errgroup"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/importer"
	"perkeep.org/pkg/progress"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
)

// statusOptionalAttrs are the attributes of a status's node that it may
// lack, deleted from the node if it no longer has them, as when the status
// was edited. Its "camliPath:" attributes, of its media, are too.
var statusOptionalAttrs = []string{
	nodeattr.Title,
	nodeattr.Content,
	nodeattr.DateModified,
	nodeattr.CamliContentImage,
	attrContentHTML,
	attrSpoilerText,
	attrSensitive,
	attrLanguage,
	attrInReplyToID,
	attrInReplyToAccountID,
	attrInReplyTo,
	attrLinkURL,
	attrLinkTitle,
	attrAuthor,
	attrAuthorName,
	attrAuthorURI,
}

// mediumOptionalAttrs are the attributes of a medium's node that it may
// lack, as statusOptionalAttrs are of a status's.
var mediumOptionalAttrs = []string{
	nodeattr.Description,
	nodeattr.CamliContentImage,
}

// refLabels are the prefixes of the titles of the nodes of boosts,
// favourites and bookmarks, by camliNodeType.
var refLabels = map[string]string{
	nodeTypeReblog:    "Boost",
	nodeTypeFavourite: "Favourite",
	nodeTypeBookmark:  "Bookmark",
}

// importStatusList imports the list of statuses from the page at first
// on, a page at a time, stopping at a page that's unchanged if the run's
// incremental. If refType is empty, the statuses are the account's own,
// and its boosts, whose nodes are children of refs. Otherwise they're
// statuses the list refers to, with nodes of camliNodeType refType, such
// as its favourites', children of refs. name and title are of its
// progress bar.
func (r *run) importStatusList(name, title, first, refType string, refs *importer.Object) error {
	r.Logf("Importing %s of %s", name, r.userAddress)
	bar := r.Progress().Bar(name, title)
	for page := first; page != ""; {
		if err := r.Context().Err(); err != nil {
			return err
		}
		sts, next, err := r.api.statuses(r.Context(), page)
		if err != nil {
			return err
		}
		if len(sts) == 0 {
			break
		}
		changed, err := r.importStatusPage(sts, refType, refs, bar)
		if err != nil {
			return err
		}
		if r.incremental && !changed {
			r.Logf("reached the %s imported before", name)
			break
		}
		page = next
	}
	bar.Finish()
	return nil
}

// A statusPage is the import of a page of a list of statuses: of the
// statuses it has or refers to, and others' that the account's own reply
// to.
type statusPage struct {
	r       *run
	byURI   map[string]*statusImport
	targets []*statusImport // in order
}

// target returns the import of st, adding it to the page's if it has
// none.
func (p *statusPage) target(st *status) *statusImport {
	if si, ok := p.byURI[st.URI]; ok {
		return si
	}
	si := &statusImport{st: st, own: st.Account.ID == p.r.userID}
	p.byURI[st.URI] = si
	p.targets = append(p.targets, si)
	return si
}

// A statusImport is a status to import, with what's fetched of it.
type statusImport struct {
	st        *status
	own       bool // whether it's the account's, not another's
	node      *importer.Object
	skip      bool   // whether it's not to be imported, after all
	text      string // of its source, or HTML if not its own
	inReplyTo string // URI of the status it replies to, if known
	media     []*mediumImport
}

// changed reports whether batch changed the status's node or media's.
func (si *statusImport) changed(batch *importer.ClaimBatch) bool {
	if batch.Changed(si.node) {
		return true
	}
	return slices.ContainsFunc(si.media, func(m *mediumImport) bool { return batch.Changed(m.node) })
}

// A refImport is a boost, favourite or bookmark to import: a node that
// refers to a status's.
type refImport struct {
	typ   string  // its camliNodeType
	path  string  // of its node, a child of the list's refs node
	boost *status // the boost, of a reblog
	node  *importer.Object
}

// A mediumImport is a status's medium to import.
type mediumImport struct {
	att  attachment
	path string           // of the medium's node, a child of its status's
	node *importer.Object // nil until made, if the status had none of it

	file    blob.Ref // of the medium
	preview blob.Ref // of the medium's preview image, if it's not one
}

// importStatusPage imports sts, a page of the list importStatusList
// imports, as it says, and reports whether it changed any. The changes
// are set with as few claims as hold them, after the permanodes of all
// they lack.
func (r *run) importStatusPage(sts []*status, refType string, refs *importer.Object, bar *progress.Bar) (changed bool, err error) {
	ctx := r.Context()
	p := &statusPage{r: r, byURI: make(map[string]*statusImport)}
	type item struct {
		si  *statusImport
		ref *refImport // or nil, if the item is the status
	}
	var items []item
	for _, st := range sts {
		switch {
		case st.URI == "":
		case refType != "":
			items = append(items, item{p.target(st), &refImport{typ: refType, path: st.URI}})
		case st.Reblog != nil:
			if st.Reblog.URI != "" {
				items = append(items, item{p.target(st.Reblog), &refImport{typ: nodeTypeReblog, path: st.URI, boost: st}})
			}
		default:
			items = append(items, item{si: p.target(st)})
		}
	}
	batch := r.Host.NewClaimBatch()
	if err := r.makeStatusNodes(ctx, batch, p.targets); err != nil {
		return false, err
	}
	parents, err := p.resolveReplies(ctx)
	if err != nil {
		return false, err
	}
	if err := r.makeStatusNodes(ctx, batch, parents); err != nil {
		return false, err
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(statusesAtOnce)
	for _, si := range p.targets {
		g.Go(func() error {
			if err := r.prepareStatus(gctx, si); err != nil {
				return fmt.Errorf("importing status %s: %w", si.st.URI, err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return false, err
	}

	var made []*mediumImport
	for _, si := range p.targets {
		for _, m := range si.media {
			if m.node == nil {
				made = append(made, m)
			}
		}
	}
	if len(made) > 0 {
		objs, err := r.Host.NewObjects(ctx, len(made))
		if err != nil {
			return false, err
		}
		for i, m := range made {
			m.node = objs[i]
		}
	}
	var refPaths []string
	var refImports []*refImport
	for _, it := range items {
		if it.ref != nil && !it.si.skip {
			refPaths = append(refPaths, it.ref.path)
			refImports = append(refImports, it.ref)
		}
	}
	if len(refPaths) > 0 {
		nodes, err := r.childNodes(ctx, batch, refs, refPaths)
		if err != nil {
			return false, err
		}
		for i, ri := range refImports {
			ri.node = nodes[i]
		}
	}

	for _, si := range p.targets {
		if !si.skip {
			r.addStatus(batch, si)
		}
	}
	for _, it := range items {
		if it.ref != nil && !it.si.skip {
			addRef(batch, it.ref, it.si)
		}
	}
	if err := batch.Commit(ctx); err != nil {
		return false, err
	}

	for _, it := range items {
		if it.si.skip {
			continue
		}
		ch := it.si.changed(batch)
		if it.ref != nil {
			ch = ch || batch.Changed(it.ref.node)
		}
		if !ch {
			bar.Skip(1)
			continue
		}
		changed = true
		bar.Add(1)
		if it.ref != nil {
			r.Logf("Imported %s of %s to %s", strings.ToLower(refLabels[it.ref.typ]), it.si.st.URI, it.ref.node.PermanodeRef())
		} else {
			r.Logf("Imported status %s to %s", it.si.st.URI, it.si.node.PermanodeRef())
		}
	}
	return changed, nil
}

// makeStatusNodes sets the nodes of the statuses of sis: the account's
// own, children of the run's statuses node, and others', of its linked
// statuses node, making those it lacks as childNodes does.
func (r *run) makeStatusNodes(ctx context.Context, batch *importer.ClaimBatch, sis []*statusImport) error {
	for _, own := range []bool{true, false} {
		var uris []string
		var of []*statusImport
		for _, si := range sis {
			if si.own == own && si.node == nil {
				uris = append(uris, si.st.URI)
				of = append(of, si)
			}
		}
		if len(uris) == 0 {
			continue
		}
		parent := r.statuses
		if !own {
			parent = r.linked
		}
		nodes, err := r.childNodes(ctx, batch, parent, uris)
		if err != nil {
			return err
		}
		for i, si := range of {
			si.node = nodes[i]
		}
	}
	return nil
}

// resolveReplies sets the URIs of the statuses that the page's own
// statuses reply to, fetching those that their nodes lack, and returns
// the imports, added to the page's, of others' among them.
func (p *statusPage) resolveReplies(ctx context.Context) ([]*statusImport, error) {
	var ids []string
	need := make(map[string][]*statusImport) // by ID of the status replied to
	for _, si := range p.targets {
		id := si.st.InReplyToID
		if !si.own || id == "" {
			continue
		}
		if si.node.Attr(attrInReplyToID) == id && si.node.Attr(attrInReplyTo) != "" {
			si.inReplyTo = si.node.Attr(attrInReplyTo)
			continue
		}
		if _, ok := need[id]; !ok {
			ids = append(ids, id)
		}
		need[id] = append(need[id], si)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	parents, err := p.r.api.statusesByID(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("fetching the statuses replied to: %w", err)
	}
	var more []*statusImport
	for _, id := range ids {
		// Statuses deleted, or not shown, aren't returned.
		pst := parents[id]
		if pst == nil || pst.URI == "" {
			continue
		}
		for _, si := range need[id] {
			si.inReplyTo = pst.URI
		}
		if pst.Account.ID != p.r.userID {
			if _, ok := p.byURI[pst.URI]; !ok {
				more = append(more, p.target(pst))
			}
		}
	}
	return more, nil
}

// prepareStatus fetches what si's status needs that its node lacks: its
// source's text, if it's the account's own, unless the node has it of the
// status as last edited, and its media.
func (r *run) prepareStatus(ctx context.Context, si *statusImport) error {
	st, n := si.st, si.node
	// The node's new, or of an import before statuses' sources and their
	// media's nodes were.
	legacy := si.own && n.Attr(attrID) == ""
	switch {
	case !si.own:
		si.text = htmlText(st.Content)
	case !legacy && n.Attr(attrID) == st.ID && n.Attr(nodeattr.DateModified) == editedAt(st):
		si.text = n.Attr(nodeattr.Content)
	default:
		src, err := r.api.statusSource(ctx, st.ID)
		if isNotFound(err) {
			r.Logf("skipping status %s, of no source: %v", st.URI, err)
			si.skip = true
			return nil
		}
		if err != nil {
			return err
		}
		si.text = src.Text
	}
	for i, att := range st.MediaAttachments {
		m, err := r.prepareMedium(ctx, n, si.own, legacy, i, att)
		if err != nil {
			return err
		}
		if m != nil {
			si.media = append(si.media, m)
		}
	}
	return nil
}

// prepareMedium returns the import of att, the medium of index i of the
// status of node n, the account's own if own, which is legacy if it was
// imported before its media had nodes, fetching what n's child of it
// lacks. It returns nil, without error, if the medium's gone, or, if it's
// another's, can't be fetched.
func (r *run) prepareMedium(ctx context.Context, n *importer.Object, own, legacy bool, i int, att attachment) (*mediumImport, error) {
	if att.URL == "" && att.RemoteURL == "" {
		return nil, nil
	}
	kind := att.Type
	if kind == "" {
		kind = "unknown"
	}
	m := &mediumImport{att: att, path: fmt.Sprintf("%s%d", kind, i+1)}
	if v := n.Attr("camliPath:" + m.path); v != "" {
		br, ok := blob.Parse(v)
		if !ok {
			return nil, fmt.Errorf("invalid blobref %q at camliPath:%s", v, m.path)
		}
		var err error
		if m.node, err = r.Host.ObjectFromRef(br); err != nil {
			return nil, err
		}
		if m.node.Attr(attrID) == att.ID {
			m.file, _ = blob.Parse(m.node.Attr(nodeattr.CamliContent))
			m.preview, _ = blob.Parse(m.node.Attr(nodeattr.CamliContentImage))
		}
	}
	if !m.file.Valid() && legacy {
		// The status's node has the file, named for its URL.
		m.file, _ = blob.Parse(n.Attr("camliPath:" + path.Base(att.URL)))
	}
	if !m.file.Valid() {
		// Others' media are the server's copies, which it may have
		// dropped, or else their own servers'.
		var err error
		m.file, err = r.fetchFile(ctx, att.URL, att.RemoteURL)
		if r.skippable(ctx, err, own) {
			r.Logf("skipping medium %s of status %s: %v", att.ID, n.PermanodeRef(), err)
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		r.media().Add(1)
	}
	if att.Type != "image" && att.PreviewURL != "" && !m.preview.Valid() {
		var err error
		m.preview, err = r.fetchFile(ctx, att.PreviewURL)
		if r.skippable(ctx, err, own) {
			r.Logf("skipping the preview of medium %s of status %s: %v", att.ID, n.PermanodeRef(), err)
		} else if err != nil {
			return nil, err
		}
	}
	return m, nil
}

// skippable reports whether err, of fetching a file of the account's own
// if own, or else of another's, is to be logged and the file skipped,
// rather than failing the run: if the file's gone, or if it's another's,
// whose server, or the server's copy of it, may fail.
func (r *run) skippable(ctx context.Context, err error, own bool) bool {
	return err != nil && ctx.Err() == nil && (isNotFound(err) || !own)
}

// fetchFile fetches the file at the first of urls that it can, skipping
// empty ones, and stores it, named for its URL. Its error is the last
// URL's.
func (r *run) fetchFile(ctx context.Context, urls ...string) (br blob.Ref, err error) {
	for _, u := range urls {
		if u == "" {
			continue
		}
		br, err = r.fetchOneFile(ctx, u)
		if err == nil || ctx.Err() != nil {
			break
		}
	}
	return br, err
}

func (r *run) fetchOneFile(ctx context.Context, rawURL string) (blob.Ref, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return blob.Ref{}, err
	}
	res, err := r.api.hc.Do(req)
	if err != nil {
		return blob.Ref{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return blob.Ref{}, &apiError{URL: req.URL.Redacted(), StatusCode: res.StatusCode}
	}
	return schema.WriteFileFromReader(ctx, r.Host.Target(), path.Base(req.URL.Path), res.Body)
}

// addStatus adds setting the attributes of si's status, and of its media,
// to batch.
func (r *run) addStatus(batch *importer.ClaimBatch, si *statusImport) {
	st := si.st
	nodeType := nodeTypeStatus
	if !si.own {
		nodeType = nodeTypeLinkedStatus
	}
	attrs := []importer.KeyValue{
		importer.KV(nodeattr.Type, nodeType),
		importer.KV(attrID, st.ID),
		importer.KV(attrURI, st.URI),
		importer.KV(nodeattr.URL, st.URL),
		importer.KV(nodeattr.StartDate, schema.RFC3339FromTime(st.CreatedAt)),
		importer.KV(nodeattr.DateModified, editedAt(st)),
		importer.KV(nodeattr.Title, si.text),
		importer.KV(nodeattr.Content, si.text),
		importer.KV(attrSpoilerText, st.SpoilerText),
		importer.KV(attrVisibility, st.Visibility),
		importer.KV(attrLanguage, st.Language),
		importer.KV(attrInReplyToID, st.InReplyToID),
		importer.KV(attrInReplyToAccountID, st.InReplyToAccountID),
		importer.KV(attrInReplyTo, si.inReplyTo),
	}
	if !si.own {
		attrs = append(attrs,
			importer.KV(attrAuthor, r.qualified(st.Account.Acct)),
			importer.KV(attrAuthorName, st.Account.DisplayName),
			importer.KV(attrAuthorURI, st.Account.actor()),
		)
	}
	if st.Content != plainHTML(si.text) {
		attrs = append(attrs, importer.KV(attrContentHTML, st.Content))
	}
	if st.Sensitive {
		attrs = append(attrs, importer.KV(attrSensitive, "true"))
	}
	if c := st.Card; c != nil {
		attrs = append(attrs,
			importer.KV(attrLinkURL, c.URL),
			importer.KV(attrLinkTitle, c.Title),
		)
	}
	var image blob.Ref
	for _, m := range si.media {
		attrs = append(attrs, importer.KV("camliPath:"+m.path, m.node.PermanodeRef().String()))
		if !image.Valid() {
			if m.att.Type == "image" {
				image = m.file
			} else {
				image = m.preview
			}
		}
		mattrs := []importer.KeyValue{
			importer.KV(nodeattr.Type, nodeTypePrefix+m.att.Type),
			importer.KV(attrID, m.att.ID),
			importer.KV(nodeattr.CamliContent, m.file.String()),
			importer.KV(nodeattr.Description, m.att.Description),
		}
		if m.preview.Valid() {
			mattrs = append(mattrs, importer.KV(nodeattr.CamliContentImage, m.preview.String()))
		}
		setAttrs(batch, m.node, mattrs, mediumOptionalAttrs)
	}
	if image.Valid() {
		attrs = append(attrs, importer.KV(nodeattr.CamliContentImage, image.String()))
	}
	setAttrs(batch, si.node, attrs, statusOptionalAttrs, "camliPath:")
}

// addRef adds setting the attributes of ri, of the status of si, to
// batch.
func addRef(batch *importer.ClaimBatch, ri *refImport, si *statusImport) {
	title := refLabels[ri.typ]
	if si.text != "" {
		title += ": " + si.text
	}
	attrs := []importer.KeyValue{
		importer.KV(nodeattr.Type, ri.typ),
		importer.KV(nodeattr.CamliContent, si.node.PermanodeRef().String()),
		importer.KV(attrSubject, si.st.URI),
		importer.KV(nodeattr.Title, title),
	}
	if b := ri.boost; b != nil {
		attrs = append(attrs,
			importer.KV(attrID, b.ID),
			importer.KV(attrURI, b.URI),
			importer.KV(nodeattr.StartDate, schema.RFC3339FromTime(b.CreatedAt)),
			importer.KV(attrVisibility, b.Visibility),
		)
	}
	setAttrs(batch, ri.node, attrs, nil)
}

// childNodes returns the children of parent of paths, as
// Object.ChildPathObjects does, but making those it lacks as new objects,
// stored together, whose "camliPath:" attributes are added to batch, to
// be claimed with the rest of it. Children of paths added to batch before
// aren't found.
func (r *run) childNodes(ctx context.Context, batch *importer.ClaimBatch, parent *importer.Object, paths []string) ([]*importer.Object, error) {
	nodes := make([]*importer.Object, len(paths))
	var missing []int             // of paths, the first of each path missing
	first := make(map[string]int) // index in paths of each path's first
	var g errgroup.Group
	g.SetLimit(statusesAtOnce)
	for i, p := range paths {
		if _, ok := first[p]; ok {
			continue
		}
		first[p] = i
		v := parent.Attr("camliPath:" + p)
		if v == "" {
			missing = append(missing, i)
			continue
		}
		g.Go(func() error {
			br, ok := blob.Parse(v)
			if !ok {
				return fmt.Errorf("invalid blobref %q at camliPath:%s", v, p)
			}
			var err error
			nodes[i], err = r.Host.ObjectFromRef(br)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		made, err := r.Host.NewObjects(ctx, len(missing))
		if err != nil {
			return nil, err
		}
		attrs := make([]importer.KeyValue, len(missing))
		for j, i := range missing {
			nodes[i] = made[j]
			attrs[j] = importer.KV("camliPath:"+paths[i], made[j].PermanodeRef().String())
		}
		batch.SetAttrs(parent, attrs...)
	}
	for i, p := range paths {
		nodes[i] = nodes[first[p]]
	}
	return nodes, nil
}

// setAttrs adds setting n's attributes of attrs that aren't empty to
// batch, and deleting those of n's of optional, or with a prefix of
// prefixes, that attrs lacks.
func setAttrs(batch *importer.ClaimBatch, n *importer.Object, attrs []importer.KeyValue, optional []string, prefixes ...string) {
	set := make(map[string]bool)
	attrs = slices.DeleteFunc(attrs, func(kv importer.KeyValue) bool { return kv.Value == "" })
	for _, kv := range attrs {
		set[kv.Key] = true
	}
	var dels []string
	for _, key := range optional {
		if !set[key] {
			dels = append(dels, key)
		}
	}
	n.ForeachAttr(func(key, _ string) {
		if set[key] || slices.Contains(dels, key) {
			return
		}
		if slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(key, p) }) {
			dels = append(dels, key)
		}
	})
	batch.DelAttrs(n, dels...)
	batch.SetAttrs(n, attrs...)
}

// editedAt returns when st was last edited, as stored, or the empty
// string if it wasn't.
func editedAt(st *status) string {
	if st.EditedAt == nil {
		return ""
	}
	return schema.RFC3339FromTime(*st.EditedAt)
}

var (
	paragraphBreaks = regexp.MustCompile(`\n\n+`)

	// htmlEscaper escapes text as Mastodon (and Ruby's ERB) does.
	htmlEscaper = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
)

// plainHTML returns the HTML that a Mastodon server renders of a status's
// text when it has no links, mentions, hashtags or markup: its paragraphs,
// between blank lines, in p elements, and its other line breaks as br
// elements. A status's HTML that's that of its text isn't stored, as
// plainHTML gives it.
func plainHTML(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	var b strings.Builder
	for _, p := range paragraphBreaks.Split(text, -1) {
		b.WriteString("<p>")
		b.WriteString(strings.ReplaceAll(htmlEscaper.Replace(p), "\n", "<br />"))
		b.WriteString("</p>")
	}
	return b.String()
}

// htmlText returns the text of s, a status's (or profile's) HTML, for
// statuses whose source the API doesn't give: all its text, with its
// paragraphs between blank lines and its br elements as line breaks.
// That's the whole of its links' URLs too, as Mastodon hides, rather than
// drops, the parts of them it doesn't show.
func htmlText(s string) string {
	nodes, err := html.ParseFragment(strings.NewReader(s), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return s
	}
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
			return
		case html.ElementNode:
			switch n.DataAtom {
			case atom.Br:
				b.WriteString("\n")
				return
			case atom.P:
				if b.Len() > 0 {
					b.WriteString("\n\n")
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return strings.TrimSpace(b.String())
}
