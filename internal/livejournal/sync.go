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
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"perkeep.org/internal/attrclaims"
	search "perkeep.org/internal/searchtypes"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/schema/nodeattr"
)

// A Journal is a journal's entries and the comments on them.
type Journal struct {
	User     string
	Entries  []*Entry
	Comments []*Comment // in order of ID
}

// Fetch returns the journal of the client's user. logf, if non-nil,
// reports its progress.
func (c *Client) Fetch(ctx context.Context, logf func(format string, args ...any)) (*Journal, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	logf("listing %s's entries...", c.User)
	items, err := c.SyncItems(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := c.Entries(ctx, items)
	if err != nil {
		return nil, err
	}
	logf("got %d entries; getting the comments on them...", len(entries))
	comments, err := c.Comments(ctx, nil)
	if err != nil {
		return nil, err
	}
	logf("got %d comments", len(comments))
	return &Journal{User: c.User, Entries: entries, Comments: comments}, nil
}

// Attributes of the permanodes of LiveJournal entries and comments,
// besides the generic ones in nodeattr. Values of LiveJournal's entry
// properties (such as current_mood) go in attributes named
// AttrPropPrefix plus the property's name.
const (
	AttrJournal   = "livejournalJournal"
	AttrItemID    = "livejournalItemId"
	AttrEventTime = "livejournalEventTime" // the entry's time, as LiveJournal has it, without a zone
	AttrSecurity  = "livejournalSecurity"
	AttrAllowMask = "livejournalAllowMask"
	AttrPoster    = "livejournalPoster"
	AttrPosterID  = "livejournalPosterId"
	AttrCommentID = "livejournalCommentId"
	AttrState     = "livejournalState"
	AttrEntry     = "livejournalEntry"  // a comment's entry's permanode
	AttrParent    = "livejournalParent" // a reply's parent comment's permanode
	// AttrRecovered is on comments whose text came from a dump of
	// LiveJournal's database rather than from LiveJournal, which no
	// longer has it. Sync keeps it, as it keeps all of what LiveJournal
	// no longer has.
	AttrRecovered = "livejournalRecovered"
	// AttrDateEstimated is "true" on a comment whose startDate is that
	// of the comment before it, as LiveJournal has no date for it.
	AttrDateEstimated = "livejournalDateEstimated"
	AttrPropPrefix    = "livejournalProp:"
	attrTag           = "tag"

	NodeTypeEntry   = "livejournal.com:entry"
	NodeTypeComment = "livejournal.com:comment"
	NodeTypeJournal = "livejournal.com:journal"
)

// ownedAttr reports whether attr is one that Sync sets, and so may change
// or remove, on its permanodes. Others, such as ones a user adds, are
// left alone.
func ownedAttr(attr string) bool {
	switch attr {
	case nodeattr.Type, nodeattr.Title, nodeattr.Content, nodeattr.StartDate,
		nodeattr.DateModified, nodeattr.URL, nodeattr.CamliMember, attrTag:
		return true
	}
	return strings.HasPrefix(attr, "livejournal")
}

// node is a permanode that Sync keeps, with the attributes it should
// have.
type node struct {
	key   string // of its planned permanode
	attrs url.Values
	pn    blob.Ref
	// textless is whether the node is of a comment that LiveJournal
	// has no text of (because it was deleted), so its permanode keeps
	// what it has (see keepLost).
	textless bool
}

// lostAttrGroups are the groups of attributes of a comment that
// LiveJournal no longer has once it's deleted. Each group is kept or
// replaced together: a kept date stays exact, or estimated.
var lostAttrGroups = [][]string{
	{nodeattr.Title, nodeattr.Content},
	{nodeattr.StartDate, AttrDateEstimated},
	{AttrPoster, AttrPosterID},
	{AttrRecovered},
}

// keepLost changes the attributes want of a textless comment's node so
// that its permanode, with attributes cur, keeps what LiveJournal no
// longer has: each group of lostAttrGroups that cur has any of, and its
// LiveJournal properties.
func keepLost(cur, want url.Values) {
	for _, group := range lostAttrGroups {
		has := false
		for _, attr := range group {
			has = has || len(cur[attr]) > 0
		}
		if !has {
			continue
		}
		for _, attr := range group {
			if vs := cur[attr]; len(vs) > 0 {
				want[attr] = vs
			} else {
				delete(want, attr)
			}
		}
	}
	for attr, vs := range cur {
		if strings.HasPrefix(attr, AttrPropPrefix) {
			want[attr] = vs
		}
	}
}

func entryKey(user string, itemID int) string {
	return "livejournal:" + user + ":entry:" + strconv.Itoa(itemID)
}
func commentKey(user string, talkID int) string {
	return "livejournal:" + user + ":comment:" + strconv.Itoa(talkID)
}
func journalKey(user string) string { return "livejournal:" + user }

type nodeStats struct {
	commentsSkipped int
	invalidUTF8     int
}

// nodes returns the permanodes to keep of j: its entries, then its
// comments, then the journal's set. The entries' times are in loc.
func (j *Journal) nodes(loc *time.Location) ([]*node, nodeStats) {
	var st nodeStats
	var nodes []*node
	user := j.User
	set := func(n *node, attr string, vals ...string) {
		for _, v := range vals {
			if v == "" {
				continue
			}
			if !utf8.ValidString(v) {
				st.invalidUTF8++
				v = strings.ToValidUTF8(v, "�")
			}
			n.attrs[attr] = append(n.attrs[attr], v)
		}
	}

	entryByID := map[int]*Entry{}
	for _, e := range j.Entries {
		entryByID[e.ItemID] = e
		n := &node{key: entryKey(user, e.ItemID), attrs: url.Values{}}
		set(n, nodeattr.Type, NodeTypeEntry)
		set(n, AttrJournal, user)
		set(n, AttrItemID, strconv.Itoa(e.ItemID))
		set(n, nodeattr.Title, e.Subject)
		set(n, nodeattr.Content, e.Event)
		set(n, nodeattr.URL, e.URL)
		set(n, AttrEventTime, e.EventTime)
		if t, err := time.ParseInLocation(syncTimeLayout, e.EventTime, loc); err == nil {
			set(n, nodeattr.StartDate, t.UTC().Format(time.RFC3339))
		}
		if !e.SyncTime.IsZero() {
			set(n, nodeattr.DateModified, e.SyncTime.UTC().Format(time.RFC3339))
		}
		set(n, AttrSecurity, e.Security)
		if e.Security == "usemask" {
			set(n, AttrAllowMask, e.AllowMask)
		}
		set(n, AttrPoster, e.Poster)
		set(n, attrTag, e.Tags()...)
		for _, name := range slices.Sorted(maps.Keys(e.Props)) {
			set(n, AttrPropPrefix+name, e.Props[name])
		}
		nodes = append(nodes, n)
	}

	commentKeys := map[int]bool{}
	for _, cm := range j.Comments {
		if entryByID[cm.JItemID] != nil {
			commentKeys[cm.ID] = true
		}
	}
	// Comments' IDs increase with time, and the comments are in order
	// of ID, so one without a date (a deleted one, which LiveJournal
	// keeps only the poster and place of) is from no earlier than the
	// comment before it that has one.
	lastDate := ""
	for _, cm := range j.Comments {
		e := entryByID[cm.JItemID]
		date, estimated := cm.Date, false
		if date != "" {
			lastDate = date
		} else if lastDate != "" {
			date, estimated = lastDate, true
		} else if e != nil && !e.SyncTime.IsZero() {
			date, estimated = e.SyncTime.UTC().Format(time.RFC3339), true
		}
		if e == nil {
			st.commentsSkipped++
			continue
		}
		n := &node{key: commentKey(user, cm.ID), attrs: url.Values{}, textless: cm.Subject == "" && cm.Body == ""}
		set(n, nodeattr.Type, NodeTypeComment)
		set(n, AttrJournal, user)
		set(n, AttrCommentID, strconv.Itoa(cm.ID))
		set(n, AttrState, cm.State)
		set(n, nodeattr.Title, cm.Subject)
		set(n, nodeattr.Content, cm.Body)
		set(n, nodeattr.StartDate, date)
		if estimated {
			set(n, AttrDateEstimated, "true")
		}
		if cm.PosterID != 0 {
			set(n, AttrPosterID, strconv.Itoa(cm.PosterID))
		}
		set(n, AttrPoster, cm.Poster)
		dtalk := strconv.Itoa(cm.DTalkID(e))
		set(n, nodeattr.URL, e.URL+"?thread="+dtalk+"#t"+dtalk)
		// The links are set later, once the permanodes' refs are known.
		n.attrs[AttrEntry] = []string{entryKey(user, e.ItemID)}
		if cm.ParentID != 0 && commentKeys[cm.ParentID] {
			n.attrs[AttrParent] = []string{commentKey(user, cm.ParentID)}
		}
		for _, name := range slices.Sorted(maps.Keys(cm.Props)) {
			set(n, AttrPropPrefix+name, cm.Props[name])
		}
		nodes = append(nodes, n)
	}

	jn := &node{key: journalKey(user), attrs: url.Values{}}
	set(jn, nodeattr.Type, NodeTypeJournal)
	set(jn, AttrJournal, user)
	set(jn, nodeattr.Title, "LiveJournal - "+user)
	set(jn, attrTag, "livejournal")
	for _, e := range j.Entries {
		jn.attrs[nodeattr.CamliMember] = append(jn.attrs[nodeattr.CamliMember], entryKey(user, e.ItemID))
	}
	nodes = append(nodes, jn)
	return nodes, st
}

// linkAttrs are the attributes whose values are permanodes' keys, until
// Sync replaces them with the permanodes' refs.
var linkAttrs = []string{AttrEntry, AttrParent, nodeattr.CamliMember}

// A Store is where Sync keeps permanodes: a Perkeep server, from a
// client, or an importer's host.
type Store interface {
	// Signer signs the permanodes and claims.
	Signer() *schema.Signer
	// Upload stores a signed schema blob.
	Upload(ctx context.Context, signed string) error
	// Describe describes blobs.
	Describe(ctx context.Context, req *search.DescribeRequest) (*search.DescribeResponse, error)
}

// SyncOptions are the options of Sync.
type SyncOptions struct {
	// Location is the time zone of the entries' times, which
	// LiveJournal records as the poster's local time, without a zone.
	Location *time.Location
	// Workers is how many permanodes to update at once.
	Workers int
	// DryRun is whether to only count the changes, making none.
	DryRun bool
	// Logf, if non-nil, reports progress.
	Logf func(format string, args ...any)
}

// SyncResult is what Sync did, or with SyncOptions.DryRun, would do.
type SyncResult struct {
	Permanodes      int // that it keeps
	Created         int // permanodes made
	Changed         int // permanodes changed
	AttrChanges     int
	CommentsSkipped int // comments of no known entry
	InvalidUTF8     int // values that weren't valid UTF-8
	Journal         blob.Ref
}

// Sync makes or updates the permanodes of j in st: one of each entry and
// of each comment (each linking to its entry and the comment it replies
// to), and a set of the entries. Their keys, and so their refs, depend
// only on the signer and their LiveJournal IDs, so running it again (or
// with another Store with the same signer) changes only what differs.
func Sync(ctx context.Context, st Store, j *Journal, opts SyncOptions) (*SyncResult, error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	loc := opts.Location
	if loc == nil {
		loc = time.Local
	}
	nodes, ns := j.nodes(loc)
	res := &SyncResult{Permanodes: len(nodes), CommentsSkipped: ns.commentsSkipped, InvalidUTF8: ns.invalidUTF8}

	refOfKey := map[string]blob.Ref{}
	planned := map[string]string{} // key to signed planned permanode
	for _, n := range nodes {
		signed, pn, err := attrclaims.PlannedPermanode(ctx, st.Signer(), n.key)
		if err != nil {
			return nil, err
		}
		n.pn = pn
		refOfKey[n.key] = pn
		planned[n.key] = signed
	}
	res.Journal = nodes[len(nodes)-1].pn
	for _, n := range nodes {
		for _, attr := range linkAttrs {
			for i, k := range n.attrs[attr] {
				pn, ok := refOfKey[k]
				if !ok {
					return nil, fmt.Errorf("livejournal: permanode %s links to unknown %s", n.key, k)
				}
				n.attrs[attr][i] = pn.String()
			}
		}
	}

	logf("reading the current attributes of %d permanodes...", len(nodes))
	cur, err := describeAttrs(ctx, st, nodes)
	if err != nil {
		return nil, err
	}

	logf("updating them...")
	var mu sync.Mutex
	done := 0
	now := time.Now()
	err = forEach(ctx, nodes, opts.Workers, func(n *node) error {
		attrs, exists := cur[n.pn]
		if n.textless {
			keepLost(attrs, n.attrs)
		}
		changes := attrChanges(attrs, n.attrs)
		mu.Lock()
		if !exists {
			res.Created++
		}
		if len(changes) > 0 {
			res.Changed++
			res.AttrChanges += len(changes)
		}
		done++
		if done%5000 == 0 {
			logf("permanodes: %d/%d", done, len(nodes))
		}
		mu.Unlock()
		if opts.DryRun {
			return nil
		}
		if !exists {
			if err := st.Upload(ctx, planned[n.key]); err != nil {
				return fmt.Errorf("uploading permanode %s: %w", n.key, err)
			}
		}
		if len(changes) == 0 {
			return nil
		}
		claims, err := attrclaims.Sign(ctx, st.Signer(), n.pn, changes, now)
		if err != nil {
			return err
		}
		for _, c := range claims {
			if err := st.Upload(ctx, c); err != nil {
				return fmt.Errorf("uploading a claim of %s: %w", n.key, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// describeAttrs returns the attributes of those of nodes' permanodes that
// st has.
func describeAttrs(ctx context.Context, st Store, nodes []*node) (map[blob.Ref]url.Values, error) {
	cur := map[blob.Ref]url.Values{}
	const batch = 500
	for i := 0; i < len(nodes); i += batch {
		var refs []blob.Ref
		for _, n := range nodes[i:min(i+batch, len(nodes))] {
			refs = append(refs, n.pn)
		}
		res, err := st.Describe(ctx, &search.DescribeRequest{BlobRefs: refs})
		if err != nil {
			return nil, err
		}
		for _, br := range refs {
			if db := res.Meta.Get(br); db != nil && db.Permanode != nil {
				cur[br] = db.Permanode.Attr
			}
		}
	}
	return cur, nil
}

// attrChanges returns the changes to make a permanode with attributes cur
// have the attributes want, among those Sync owns.
func attrChanges(cur, want url.Values) []attrclaims.Change {
	var changes []attrclaims.Change
	for _, attr := range slices.Sorted(maps.Keys(want)) {
		vals := want[attr]
		if slices.Equal(cur[attr], vals) {
			continue
		}
		if attr == nodeattr.CamliMember {
			// Change only the members that differ, as there are many.
			changes = append(changes, memberChanges(cur[attr], vals)...)
			continue
		}
		changes = append(changes, attrclaims.Change{Op: "set", Attr: attr, Value: vals[0]})
		for _, v := range vals[1:] {
			changes = append(changes, attrclaims.Change{Op: "add", Attr: attr, Value: v})
		}
	}
	for _, attr := range slices.Sorted(maps.Keys(cur)) {
		if _, ok := want[attr]; !ok && ownedAttr(attr) {
			changes = append(changes, attrclaims.Change{Op: "del", Attr: attr})
		}
	}
	return changes
}

// memberChanges returns the changes to make a set with members cur have
// the members want too. It never removes members: an entry that
// LiveJournal no longer has (because it was deleted) stays in the
// journal's set, as its permanode stays.
func memberChanges(cur, want []string) []attrclaims.Change {
	var changes []attrclaims.Change
	have := map[string]bool{}
	for _, m := range cur {
		have[m] = true
	}
	for _, m := range want {
		if !have[m] {
			changes = append(changes, attrclaims.Change{Op: "add", Attr: nodeattr.CamliMember, Value: m})
			have[m] = true
		}
	}
	return changes
}

// forEach calls fn on each of items, up to workers at once, stopping at
// the first error, which it returns.
func forEach[T any](ctx context.Context, items []T, workers int, fn func(T) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	work := make(chan T)
	var wg sync.WaitGroup
	for range max(1, min(workers, len(items))) {
		wg.Go(func() {
			for it := range work {
				if err := fn(it); err != nil {
					cancel(err)
				}
			}
		})
	}
feed:
	for _, it := range items {
		select {
		case work <- it:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	return context.Cause(ctx)
}
