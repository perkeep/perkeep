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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"perkeep.org/internal/attrclaims"
	search "perkeep.org/internal/searchtypes"
	"perkeep.org/internal/takeout"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema/nodeattr"
)

// Attributes of permanodes of Google Photos photos and albums, besides
// the generic ones in nodeattr.
const (
	// attrGooglePhotosMetadata is the JSON that Takeout exported of a
	// photo (its sidecar) or album (its metadata.json, or a JSON array of
	// them, if more than one album has its title).
	attrGooglePhotosMetadata = "googlePhotosMetadata"

	// googlePhotosAttrPrefix prefixes the names of the attributes of
	// Google Photos' values of generic attributes, such as
	// "googlePhotosTitle", used when the generic attribute already
	// has another value.
	googlePhotosAttrPrefix = "googlePhotos"

	// attrTakeoutExport is the export that last updated an album or the
	// albums' root, so an older export doesn't undo a newer one's changes.
	attrTakeoutExport = "takeoutExport"

	nodeTypeGooglePhotosAlbum = "photos.google.com:album"

	// The keys of planned permanodes of albums (plus the title) and of
	// the root of the albums.
	albumPermanodeKeyPrefix = "googlePhotosAlbum:"
	albumsRootPermanodeKey  = "googlePhotosAlbums"
)

// googleAttr returns the name of the attribute of Google Photos' value of
// the generic attribute attr, such as "googlePhotosDateCreated" for
// "dateCreated".
func googleAttr(attr string) string {
	return googlePhotosAttrPrefix + strings.ToUpper(attr[:1]) + attr[1:]
}

// attrChange is a change to a permanode's attribute.
type attrChange struct {
	op    string // "set", "add", or "del"
	attr  string
	value string
}

func takeoutTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func takeoutFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// sameAttrValue reports whether a and b are the same value of attr,
// comparing times and coordinates by value, not by format.
func sameAttrValue(attr, a, b string) bool {
	if a == b {
		return true
	}
	switch attr {
	case nodeattr.DateCreated, nodeattr.DatePublished, nodeattr.DateModified:
		ta, err1 := time.Parse(time.RFC3339Nano, a)
		tb, err2 := time.Parse(time.RFC3339Nano, b)
		return err1 == nil && err2 == nil && ta.Equal(tb)
	case nodeattr.Latitude, nodeattr.Longitude, nodeattr.Altitude:
		fa, err1 := strconv.ParseFloat(a, 64)
		fb, err2 := strconv.ParseFloat(b, 64)
		return err1 == nil && err2 == nil && math.Abs(fa-fb) < 1e-6
	}
	return false
}

// photoAttrChanges returns the changes to make to a photo's permanode,
// with attributes cur, for its Google Photos metadata m. Each of Google
// Photos' values goes in its generic attribute (such as "title") if that
// has none, or else in its Google Photos attribute (such as
// "googlePhotosTitle"), unless either attribute already has the value. So
// values that something else set, such as Immich or the user, aren't
// changed. The coordinates of a location are one value.
func photoAttrChanges(cur url.Values, m *takeout.PhotoMetadata) []attrChange {
	var changes []attrChange
	type field struct {
		attr  string
		value string
	}
	group := func(fields ...field) {
		var have []field
		for _, f := range fields {
			if f.value != "" {
				have = append(have, f)
			}
		}
		if len(have) == 0 {
			return
		}
		genericSet, genericSame, googleSame := false, true, true
		for _, f := range have {
			if cur.Get(f.attr) != "" {
				genericSet = true
			}
			genericSame = genericSame && sameAttrValue(f.attr, cur.Get(f.attr), f.value)
			googleSame = googleSame && sameAttrValue(f.attr, cur.Get(googleAttr(f.attr)), f.value)
		}
		if genericSame || googleSame {
			return
		}
		for _, f := range have {
			attr := f.attr
			if genericSet {
				attr = googleAttr(f.attr)
			}
			changes = append(changes, attrChange{"set", attr, f.value})
		}
	}
	timeValue := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return takeoutTime(t)
	}
	group(field{nodeattr.Title, m.Title})
	group(field{nodeattr.Description, m.Description})
	group(field{nodeattr.DateCreated, timeValue(m.PhotoTaken)})
	group(field{nodeattr.DatePublished, timeValue(m.Created)})
	group(field{nodeattr.DateModified, timeValue(m.Modified)})
	group(field{nodeattr.URL, m.URL})
	if m.HasLocation {
		alt := ""
		if m.Altitude != 0 {
			alt = takeoutFloat(m.Altitude)
		}
		group(field{nodeattr.Latitude, takeoutFloat(m.Latitude)}, field{nodeattr.Longitude, takeoutFloat(m.Longitude)}, field{nodeattr.Altitude, alt})
	}
	if raw := string(m.JSON); raw != "" && cur.Get(attrGooglePhotosMetadata) != raw {
		changes = append(changes, attrChange{"set", attrGooglePhotosMetadata, raw})
	}
	return changes
}

// albumAttrs returns the attributes of the permanode of album a.
func albumAttrs(a *takeout.Album) []attrChange {
	attrs := []attrChange{
		{"set", nodeattr.Type, nodeTypeGooglePhotosAlbum},
		{"set", nodeattr.Title, a.Title()},
	}
	if d := a.Description(); d != "" {
		attrs = append(attrs, attrChange{"set", nodeattr.Description, d})
	}
	var date time.Time
	var raws []json.RawMessage
	for _, m := range a.Meta {
		if !m.Date.IsZero() && (date.IsZero() || m.Date.Before(date)) {
			date = m.Date
		}
		raws = append(raws, m.JSON)
	}
	if !date.IsZero() {
		attrs = append(attrs, attrChange{"set", nodeattr.DateCreated, takeoutTime(date)})
	}
	for _, m := range a.Meta {
		if m.Location != "" {
			attrs = append(attrs, attrChange{"set", nodeattr.LocationText, m.Location})
			break
		}
	}
	for _, m := range a.Meta {
		if m.HasLocation {
			attrs = append(attrs,
				attrChange{"set", nodeattr.Latitude, takeoutFloat(m.Latitude)},
				attrChange{"set", nodeattr.Longitude, takeoutFloat(m.Longitude)})
			break
		}
	}
	raw := []byte(nil)
	if len(raws) == 1 {
		raw = raws[0]
	} else if len(raws) > 1 {
		raw, _ = json.Marshal(raws)
	}
	if len(raw) > 0 {
		attrs = append(attrs, attrChange{"set", attrGooglePhotosMetadata, string(raw)})
	}
	return attrs
}

// setChanges returns the changes to make to a permanode of a set, with
// attributes cur, for it to have the attributes attrs (which it owns,
// so they replace any other values) and exactly the members.
func setChanges(cur url.Values, attrs []attrChange, members []string) []attrChange {
	var changes []attrChange
	for _, a := range attrs {
		if vs := cur[a.attr]; len(vs) != 1 || vs[0] != a.value {
			changes = append(changes, a)
		}
	}
	want := make(map[string]bool, len(members))
	for _, m := range members {
		want[m] = true
	}
	have := make(map[string]bool)
	for _, m := range cur[nodeattr.CamliMember] {
		have[m] = true
		if !want[m] {
			changes = append(changes, attrChange{"del", nodeattr.CamliMember, m})
		}
	}
	for _, m := range members {
		if !have[m] {
			changes = append(changes, attrChange{"add", nodeattr.CamliMember, m})
			have[m] = true
		}
	}
	return changes
}

// takeoutMetaSync applies the metadata of a Library to the permanodes of
// its photos and albums.
type takeoutMetaSync struct {
	st      *takeoutStore
	exp     *takeout.Export
	lib     *takeout.Library
	workers int
	dryRun  bool // compute the changes, but make none
	verbose bool

	mu sync.Mutex // guards the following
	// photoNodes are the permanodes of lib's photos, if found or made.
	photoNodes map[*takeout.Photo]blob.Ref
	stats      takeoutMetaStats
}

type takeoutMetaStats struct {
	photosMissing      int // not on the server
	photosNoMeta       int // without a sidecar
	photoNodesCreated  int
	photosChanged      int
	photoAttrsSet      int
	albumsChanged      int
	albumMembersAdded  int
	albumMembersDelete int
	albumsSkipped      int // last updated by a newer export
}

func (s *takeoutMetaSync) run(ctx context.Context) error {
	s.photoNodes = make(map[*takeout.Photo]blob.Ref)
	log.Printf("Updating the permanodes of %d photos...", len(s.lib.Photos))
	pr := newTakeoutProgress(len(s.lib.Photos), 0)
	done := 0
	err := takeoutForEach(ctx, s.lib.Photos, s.workers, func(p *takeout.Photo) error {
		if err := s.syncPhoto(ctx, p); err != nil {
			return fmt.Errorf("%s: %w", p.Content.Entries[0].File.Name, err)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		done++
		pr.update(done, 0)
		return nil
	})
	if err != nil {
		return err
	}
	log.Printf("Updating %d albums...", len(s.lib.Albums))
	var albumNodes []string
	for _, a := range s.lib.Albums {
		pn, err := s.syncAlbum(ctx, a)
		if err != nil {
			return fmt.Errorf("album %q: %w", a.Title(), err)
		}
		albumNodes = append(albumNodes, pn.String())
	}
	_, err = s.syncSet(ctx, albumsRootPermanodeKey, []attrChange{{"set", nodeattr.Title, "Google Photos albums"}}, albumNodes)
	return err
}

// syncPhoto finds or makes the permanode of p, and sets its attributes.
func (s *takeoutMetaSync) syncPhoto(ctx context.Context, p *takeout.Photo) error {
	pn, cur, fileRef, err := s.findPhotoPermanode(ctx, p.Content)
	if err != nil {
		return err
	}
	if !pn.Valid() && !fileRef.Valid() {
		s.mu.Lock()
		s.stats.photosMissing++
		s.mu.Unlock()
		return nil
	}
	claimTime := s.exp.Time
	var changes []attrChange
	if !pn.Valid() {
		// The contents are on the server without a permanode, so make
		// the one that pk takeout upload and pk-put -filenodes make.
		pn, err = s.st.plannedPermanode(ctx, fileRef.wholeRef.String(), s.dryRun)
		if err != nil {
			return err
		}
		changes = append(changes, attrChange{"set", nodeattr.CamliContent, fileRef.ref.String()})
		cur = url.Values{}
	}
	if p.Meta != nil {
		changes = append(changes, photoAttrChanges(cur, p.Meta)...)
		if !p.Meta.Modified.IsZero() {
			claimTime = p.Meta.Modified
		}
	}
	s.mu.Lock()
	s.photoNodes[p] = pn
	if p.Meta == nil {
		s.stats.photosNoMeta++
	}
	if fileRef.Valid() {
		s.stats.photoNodesCreated++
	}
	if len(changes) > 0 {
		s.stats.photosChanged++
		s.stats.photoAttrsSet += len(changes)
	}
	s.mu.Unlock()
	return s.st.applyChanges(ctx, pn, changes, claimTime, s.dryRun)
}

// contentFile is a file of some contents on the server.
type contentFile struct {
	ref      blob.Ref
	wholeRef blob.Ref
}

func (f contentFile) Valid() bool { return f.ref.Valid() }

// findPhotoPermanode returns the permanode of contents c, and its
// attributes: the permanode whose camliContent is a file with them that
// pk takeout upload (or pk-put -filenodes) makes, if it exists, or else
// any. If there's none, it returns a file with the contents, if any.
func (s *takeoutMetaSync) findPhotoPermanode(ctx context.Context, c *takeout.Content) (pn blob.Ref, attrs url.Values, file contentFile, err error) {
	size := c.Size
	fileC := &search.Constraint{File: &search.FileConstraint{
		FileSize: &search.IntConstraint{Equals: &size},
		Digest:   c.Digest(),
	}}
	res, err := s.st.c.Query(ctx, &search.SearchQuery{
		Constraint: &search.Constraint{Permanode: &search.PermanodeConstraint{
			Attr:       nodeattr.CamliContent,
			ValueInSet: fileC,
		}},
		Describe: &search.DescribeRequest{Rules: []*search.DescribeRule{{Attrs: []string{nodeattr.CamliContent}}}},
	})
	if err != nil {
		return blob.Ref{}, nil, contentFile{}, err
	}
	if len(res.Blobs) > 0 {
		var candidates []blob.Ref
		for _, b := range res.Blobs {
			candidates = append(candidates, b.Blob)
		}
		slices.SortFunc(candidates, func(a, b blob.Ref) int { return strings.Compare(a.String(), b.String()) })
		pn = candidates[0]
		// Prefer the planned permanode of the contents.
		for _, db := range res.Describe.Meta {
			if db.File == nil || !db.File.WholeRef.Valid() {
				continue
			}
			planned, err := s.st.plannedPermanodeRef(ctx, db.File.WholeRef.String())
			if err != nil {
				return blob.Ref{}, nil, contentFile{}, err
			}
			if slices.Contains(candidates, planned) {
				pn = planned
				break
			}
		}
		attrs = url.Values{}
		if db := res.Describe.Meta.Get(pn); db != nil && db.Permanode != nil {
			attrs = db.Permanode.Attr
		}
		return pn, attrs, contentFile{}, nil
	}
	res, err = s.st.c.Query(ctx, &search.SearchQuery{Constraint: fileC, Limit: 1, Describe: &search.DescribeRequest{}})
	if err != nil || len(res.Blobs) == 0 {
		return blob.Ref{}, nil, contentFile{}, err
	}
	br := res.Blobs[0].Blob
	if db := res.Describe.Meta.Get(br); db != nil && db.File != nil && db.File.WholeRef.Valid() {
		return blob.Ref{}, nil, contentFile{ref: br, wholeRef: db.File.WholeRef}, nil
	}
	return blob.Ref{}, nil, contentFile{}, nil
}

// syncAlbum makes or updates the permanode of album a.
func (s *takeoutMetaSync) syncAlbum(ctx context.Context, a *takeout.Album) (blob.Ref, error) {
	var members []string
	s.mu.Lock()
	for _, p := range a.Photos {
		if pn, ok := s.photoNodes[p]; ok {
			members = append(members, pn.String())
		}
	}
	s.mu.Unlock()
	return s.syncSet(ctx, albumPermanodeKeyPrefix+a.Title(), albumAttrs(a), members)
}

// syncSet makes or updates the planned permanode with key, of a set with
// attributes attrs and exactly the given members, unless a newer export
// last updated it.
func (s *takeoutMetaSync) syncSet(ctx context.Context, key string, attrs []attrChange, members []string) (blob.Ref, error) {
	pn, err := s.st.plannedPermanode(ctx, key, s.dryRun)
	if err != nil {
		return blob.Ref{}, err
	}
	cur := url.Values{}
	res, err := s.st.c.Describe(ctx, &search.DescribeRequest{BlobRef: pn})
	if err != nil {
		return blob.Ref{}, err
	}
	if db := res.Meta.Get(pn); db != nil && db.Permanode != nil {
		cur = db.Permanode.Attr
	}
	if last := cur.Get(attrTakeoutExport); last > s.exp.Name {
		s.mu.Lock()
		s.stats.albumsSkipped++
		s.mu.Unlock()
		log.Printf("Not updating %q: export %s updated it last.", key, last)
		return pn, nil
	}
	attrs = append(slices.Clone(attrs), attrChange{"set", attrTakeoutExport, s.exp.Name})
	changes := setChanges(cur, attrs, members)
	if len(changes) == 0 {
		return pn, nil
	}
	s.mu.Lock()
	s.stats.albumsChanged++
	for _, c := range changes {
		switch c.op {
		case "add":
			s.stats.albumMembersAdded++
		case "del":
			s.stats.albumMembersDelete++
		}
	}
	s.mu.Unlock()
	if s.verbose {
		log.Printf("Updating %q: %d changes.", key, len(changes))
	}
	return pn, s.st.applyChanges(ctx, pn, changes, s.exp.Time, s.dryRun)
}

func (s *takeoutMetaSync) report() string {
	st := s.stats
	verb := "updated"
	if s.dryRun {
		verb = "would update"
	}
	return fmt.Sprintf("metadata: %s %d photos' permanodes (%d attributes; %d permanodes new), %d without sidecars, %d not on the server; %s %d sets (albums and their root): %d members added, %d removed; %d skipped as newer",
		verb, st.photosChanged, st.photoAttrsSet, st.photoNodesCreated, st.photosNoMeta, st.photosMissing,
		verb, st.albumsChanged, st.albumMembersAdded, st.albumMembersDelete, st.albumsSkipped)
}

// plannedPermanodeRef returns the ref of the planned permanode with key,
// signed as pk-put -filenodes signs it.
func (s *takeoutStore) plannedPermanodeRef(ctx context.Context, key string) (blob.Ref, error) {
	_, ref, err := attrclaims.PlannedPermanode(ctx, s.signer, key)
	return ref, err
}

// plannedPermanode uploads, unless dryRun, and returns the planned
// permanode with key.
func (s *takeoutStore) plannedPermanode(ctx context.Context, key string, dryRun bool) (blob.Ref, error) {
	signed, ref, err := attrclaims.PlannedPermanode(ctx, s.signer, key)
	if err != nil || dryRun {
		return ref, err
	}
	res, err := s.c.Upload(ctx, client.NewUploadHandleFromString(signed))
	if err != nil {
		return blob.Ref{}, fmt.Errorf("uploading planned permanode: %w", err)
	}
	return res.BlobRef, nil
}

// applyChanges uploads the claims, dated claimTime, of changes to pn,
// unless dryRun. (See attrclaims.Sign.)
func (s *takeoutStore) applyChanges(ctx context.Context, pn blob.Ref, changes []attrChange, claimTime time.Time, dryRun bool) error {
	if len(changes) == 0 || dryRun {
		return nil
	}
	cs := make([]attrclaims.Change, len(changes))
	for i, c := range changes {
		cs[i] = attrclaims.Change{Op: c.op, Attr: c.attr, Value: c.value}
	}
	signed, err := attrclaims.Sign(ctx, s.signer, pn, cs, claimTime)
	if err != nil {
		return err
	}
	for _, claim := range signed {
		if _, err := s.c.Upload(ctx, client.NewUploadHandleFromString(claim)); err != nil {
			return fmt.Errorf("uploading claim: %w", err)
		}
	}
	return nil
}

// takeoutForEach calls fn on each of items, up to workers at once,
// stopping at the first error, which it returns.
func takeoutForEach[T any](ctx context.Context, items []T, workers int, fn func(T) error) error {
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
