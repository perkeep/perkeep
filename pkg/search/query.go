/*
Copyright 2013 The Perkeep Authors

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

package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"perkeep.org/internal/searchtypes"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/types/camtypes"

	"go4.org/strutil"
	"tailscale.com/util/set"
)

type SortType = searchtypes.SortType

const (
	UnspecifiedSort  = searchtypes.UnspecifiedSort
	Unsorted         = searchtypes.Unsorted
	LastModifiedDesc = searchtypes.LastModifiedDesc
	LastModifiedAsc  = searchtypes.LastModifiedAsc
	CreatedDesc      = searchtypes.CreatedDesc
	CreatedAsc       = searchtypes.CreatedAsc
	BlobRefAsc       = searchtypes.BlobRefAsc
	MapSort          = searchtypes.MapSort
	maxSortType      = searchtypes.MaxSortType
)

var sortName = searchtypes.SortName

type SearchQuery = searchtypes.SearchQuery

// exprQuery optionally specifies the *SearchQuery prototype that was generated
// by parsing the search expression.
//
// If expr is non-nil, then its Contraint/Sort/Limit are used instead of q's in
// the cloned result.
func searchQueryPlannedQuery(q *SearchQuery, expr *SearchQuery) *SearchQuery {
	pq := new(SearchQuery)
	*pq = *q
	if expr != nil {
		pq.Constraint = expr.Constraint
		if expr.Sort != 0 {
			pq.Sort = expr.Sort
		}
		if expr.Limit != 0 {
			pq.Limit = expr.Limit
		}
	}
	if pq.Sort == UnspecifiedSort {
		if constraintOnlyMatchesPermanode(pq.Constraint) {
			pq.Sort = CreatedDesc
		}
	}
	if pq.Limit == 0 {
		pq.Limit = 200 // arbitrary
	}
	if err := searchQueryAddContinueConstraint(pq); err != nil {
		log.Printf("Ignoring continue token: %v", err)
	}
	pq.Constraint = optimizePlan(pq.Constraint)
	return pq
}

// For permanodes, the continue token is (currently!)
// of form "pn:nnnnnnn:sha1-xxxxx" where "pn" is a
// literal, "nnnnnn" is the UnixNano of the time
// (modified or created) and "sha1-xxxxx" was the item
// seen in the final result set, used as a tie breaker
// if multiple permanodes had the same mod/created
// time. This format is NOT an API promise or standard and
// clients should not rely on it. It may change without notice
func parsePermanodeContinueToken(v string) (t time.Time, br blob.Ref, ok bool) {
	if !strings.HasPrefix(v, "pn:") {
		return
	}
	v = v[len("pn:"):]
	col := strings.Index(v, ":")
	if col < 0 {
		return
	}
	nano, err := strconv.ParseUint(v[:col], 10, 64)
	if err != nil {
		return
	}
	t = time.Unix(0, int64(nano))
	br, ok = blob.Parse(v[col+1:])
	return
}

// addContinueConstraint conditionally modifies q.Constraint to scroll
// past the results as indicated by q.Continue.
func searchQueryAddContinueConstraint(q *SearchQuery) error {
	cont := q.Continue
	if cont == "" {
		return nil
	}
	if constraintOnlyMatchesPermanode(q.Constraint) {
		tokent, lastbr, ok := parsePermanodeContinueToken(cont)
		if !ok {
			return errors.New("Unexpected continue token")
		}
		if q.Sort == LastModifiedDesc || q.Sort == CreatedDesc {
			var lastMod, lastCreated time.Time
			switch q.Sort {
			case LastModifiedDesc:
				lastMod = tokent
			case CreatedDesc:
				lastCreated = tokent
			}
			baseConstraint := q.Constraint
			q.Constraint = &Constraint{
				Logical: &LogicalConstraint{
					Op: "and",
					A: &Constraint{
						Permanode: &PermanodeConstraint{
							Continue: &PermanodeContinueConstraint{
								LastCreated: lastCreated,
								LastMod:     lastMod,
								Last:        lastbr,
							},
						},
					},
					B: baseConstraint,
				},
			}
		}
		return nil
	}
	return errors.New("token not valid for query type")
}

// checkValid checks the validity of the SearchQuery.
//
// If q is for an expression, it returns the compiled SearchQuery with a
// Constraint populated. If q is already a Constraint-based query, it nil and
// whether it's valid.
func searchQueryCheckValid(q *SearchQuery, ctx context.Context) (maybeConstraintQuery *SearchQuery, err error) {
	if q.Sort >= maxSortType || q.Sort < 0 {
		return nil, errors.New("invalid sort type")
	}
	if q.Continue != "" && q.Around.Valid() {
		return nil, errors.New("Continue and Around parameters are mutually exclusive")
	}
	if q.Sort == MapSort && (q.Continue != "" || q.Around.Valid()) {
		return nil, errors.New("Continue or Around parameters are not available with MapSort")
	}
	if q.Constraint != nil && q.Expression != "" {
		return nil, errors.New("Constraint and Expression are mutually exclusive in a search query")
	}
	if q.Constraint != nil {
		return nil, constraintCheckValid(q.Constraint)
	}
	expr := q.Expression
	sq, err := parseExpression(ctx, expr)
	if err != nil {
		return nil, fmt.Errorf("Error parsing search expression %q: %w", expr, err)
	}
	if err := constraintCheckValid(sq.Constraint); err != nil {
		return nil, fmt.Errorf("Internal error: parseExpression(%q) returned invalid constraint: %w", expr, err)
	}
	return sq, nil
}

// SearchResult is the result of the Search method for a given SearchQuery.
type SearchResult = searchtypes.SearchResult

type SearchResultBlob = searchtypes.SearchResultBlob

// Constraint specifies a blob matching constraint.
// A blob matches if it matches all non-zero fields' predicates.
// A zero constraint matches nothing.
type Constraint = searchtypes.Constraint

func constraintCheckValid(c *Constraint) error {
	if c.Claim != nil {
		return errors.New("TODO: implement ClaimConstraint")
	}
	if err := logicalConstraintCheckValid(c.Logical); err != nil {
		return err
	}
	if err := fileConstraintCheckValid(c.File); err != nil {
		return err
	}
	if err := dirConstraintCheckValid(c.Dir); err != nil {
		return err
	}
	if err := intConstraintCheckValid(c.BlobSize); err != nil {
		return err
	}
	return permanodeConstraintCheckValid(c.Permanode)
}

// matchesPermanodeTypes returns a set of valid permanode types that a matching
// permanode must have as its "camliNodeType" attribute.
// It returns a zero-length slice if this constraint might include things other
// things.
func constraintMatchesPermanodeTypes(c *Constraint) []string {
	if c == nil {
		return nil
	}
	if pc := c.Permanode; pc != nil && pc.Attr == "camliNodeType" && pc.Value != "" {
		return []string{pc.Value}
	}
	if lc := c.Logical; lc != nil {
		sa := constraintMatchesPermanodeTypes(lc.A)
		sb := constraintMatchesPermanodeTypes(lc.B)
		switch lc.Op {
		case "and":
			if len(sa) != 0 {
				return sa
			}
			return sb
		case "or":
			return append(sa, sb...)
		}
	}
	return nil

}

// blobCandidates reports whether c can only match blobs in a set that
// corpus can find without enumerating all blobs of some kind. If so, it
// returns a superset of the blobs c matches, which may include blobs
// that don't exist.
func constraintBlobCandidates(c *Constraint, corpus *index.Corpus) (cands set.Set[blob.Ref], ok bool) {
	if c == nil {
		return nil, false
	}
	// A blob must match all of c's fields, so any one of them that
	// limits the candidates is enough.
	if c.BlobRefPrefix != "" {
		if br, ok := blob.Parse(c.BlobRefPrefix); ok {
			return set.Of(br), true
		}
	}
	if fc := c.File; fc != nil && fc.WholeRef.Valid() {
		return set.Of(corpus.AppendFilesOfWholeRef(nil, fc.WholeRef)...), true
	}
	if pc := c.Permanode; pc != nil {
		if cands, ok := permanodeConstraintPermanodeCandidates(pc, corpus); ok {
			return cands, true
		}
	}
	if lc := c.Logical; lc != nil {
		a, aok := constraintBlobCandidates(lc.A, corpus)
		b, bok := constraintBlobCandidates(lc.B, corpus)
		switch lc.Op {
		case "and":
			if aok && (!bok || len(a) <= len(b)) {
				return a, true
			}
			if bok {
				return b, true
			}
		case "or", "xor":
			if aok && bok {
				a.AddSet(b)
				return a, true
			}
		}
	}
	return nil, false
}

// permanodeCandidates reports whether c requires a value of c.Attr to be
// one of a set of blobs that blobCandidates can find. If so, it returns
// the permanodes with a claim giving c.Attr one of those values, a
// superset of the permanodes c matches.
func permanodeConstraintPermanodeCandidates(c *PermanodeConstraint, corpus *index.Corpus) (set.Set[blob.Ref], bool) {
	if c.Attr == "" {
		return nil, false
	}
	var vals set.Set[blob.Ref]
	if c.Value != "" {
		br, ok := blob.Parse(c.Value)
		if !ok {
			return nil, false
		}
		vals = set.Of(br)
	} else if c.ValueInSet != nil {
		var ok bool
		if vals, ok = constraintBlobCandidates(c.ValueInSet, corpus); !ok {
			return nil, false
		}
	} else {
		return nil, false
	}
	pns := make(set.Set[blob.Ref])
	for v := range vals {
		corpus.ForeachClaimBack(v, c.At, func(cl *camtypes.Claim) bool {
			if cl.Attr == c.Attr {
				pns.Add(cl.Permanode)
			}
			return true
		})
	}
	return pns, true
}

func constraintOnlyMatchesPermanode(c *Constraint) bool {
	if c.Permanode != nil || c.CamliType == schema.TypePermanode {
		return true
	}

	if c.Logical != nil && c.Logical.Op == "and" {
		if constraintOnlyMatchesPermanode(c.Logical.A) || constraintOnlyMatchesPermanode(c.Logical.B) {
			return true
		}
	}

	// TODO: There are other cases we can return true here, like:
	// Logical:{Op:'or', A:PermanodeConstraint{...}, B:PermanodeConstraint{...}

	return false
}

type FileConstraint = searchtypes.FileConstraint

type MediaTagConstraint = searchtypes.MediaTagConstraint

// DirConstraint matches static directories.
type DirConstraint = searchtypes.DirConstraint

// An IntConstraint specifies constraints on an integer.
type IntConstraint = searchtypes.IntConstraint

func intConstraintHasMin(c *IntConstraint) bool { return c.Min != 0 || c.ZeroMin }
func intConstraintHasMax(c *IntConstraint) bool { return c.Max != 0 || c.ZeroMax }

func intConstraintCheckValid(c *IntConstraint) error {
	if c == nil {
		return nil
	}
	if c.ZeroMin && c.Min != 0 {
		return errors.New("in IntConstraint, can't set both ZeroMin and Min")
	}
	if c.ZeroMax && c.Max != 0 {
		return errors.New("in IntConstraint, can't set both ZeroMax and Max")
	}
	if intConstraintHasMax(c) && intConstraintHasMin(c) && c.Min > c.Max {
		return errors.New("in IntConstraint, min is greater than max")
	}
	return nil
}

func intConstraintIntMatches(c *IntConstraint, v int64) bool {
	if c.Equals != nil {
		return v == *c.Equals
	}
	if intConstraintHasMin(c) && v < c.Min {
		return false
	}
	if intConstraintHasMax(c) && v > c.Max {
		return false
	}
	return true
}

// A FloatConstraint specifies constraints on a float.
type FloatConstraint = searchtypes.FloatConstraint

func floatConstraintHasMin(c *FloatConstraint) bool { return c.Min != 0 || c.ZeroMin }
func floatConstraintHasMax(c *FloatConstraint) bool { return c.Max != 0 || c.ZeroMax }

func floatConstraintCheckValid(c *FloatConstraint) error {
	if c == nil {
		return nil
	}
	if math.IsNaN(c.Min) || math.IsInf(c.Min, 0) || math.IsNaN(c.Max) || math.IsInf(c.Max, 0) {
		return errors.New("in FloatConstraint, bounds must be finite")
	}
	if c.ZeroMin && c.Min != 0 {
		return errors.New("in FloatConstraint, can't set both ZeroMin and Min")
	}
	if c.ZeroMax && c.Max != 0 {
		return errors.New("in FloatConstraint, can't set both ZeroMax and Max")
	}
	if floatConstraintHasMax(c) && floatConstraintHasMin(c) && c.Min > c.Max {
		return errors.New("in FloatConstraint, min is greater than max")
	}
	return nil
}

func floatConstraintFloatMatches(c *FloatConstraint, v float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	if floatConstraintHasMin(c) && v < c.Min {
		return false
	}
	if floatConstraintHasMax(c) && v > c.Max {
		return false
	}
	return true
}

type EXIFConstraint = searchtypes.EXIFConstraint

type LocationConstraint = searchtypes.LocationConstraint

func locationConstraintMatchesLatLong(c *LocationConstraint, lat, long float64) bool {
	if c.Any {
		return true
	}
	if !(c.South <= lat && lat <= c.North) {
		return false
	}
	if c.West < c.East {
		return c.West <= long && long <= c.East
	}
	// boundary spanning longitude ±180°
	return c.West <= long || long <= c.East
}

// A StringConstraint specifies constraints on a string.
// All non-zero must match.
type StringConstraint = searchtypes.StringConstraint

// stringCompareFunc contains a function to get a value from a StringConstraint and a second function to compare it
// against the string s that's being matched.
type stringConstraintFunc struct {
	v  func(*StringConstraint) string
	fn func(s, v string) bool
}

// Functions to compare fields of a StringConstraint against strings in a case-sensitive manner.
var stringConstraintFuncs = []stringConstraintFunc{
	{func(c *StringConstraint) string { return c.Equals }, func(a, b string) bool { return a == b }},
	{func(c *StringConstraint) string { return c.Contains }, strings.Contains},
	{func(c *StringConstraint) string { return c.HasPrefix }, strings.HasPrefix},
	{func(c *StringConstraint) string { return c.HasSuffix }, strings.HasSuffix},
}

// Functions to compare fields of a StringConstraint against strings in a case-insensitive manner.
var stringConstraintFuncsFold = []stringConstraintFunc{
	{func(c *StringConstraint) string { return c.Equals }, strings.EqualFold},
	{func(c *StringConstraint) string { return c.Contains }, strutil.ContainsFold},
	{func(c *StringConstraint) string { return c.HasPrefix }, strutil.HasPrefixFold},
	{func(c *StringConstraint) string { return c.HasSuffix }, strutil.HasSuffixFold},
}

func stringConstraintStringMatches(c *StringConstraint, s string) bool {
	if c.Empty && len(s) > 0 {
		return false
	}
	if c.ByteLength != nil && !intConstraintIntMatches(c.ByteLength, int64(len(s))) {
		return false
	}
	if c.Regexp != "" {
		re, err := searchtypes.StringRegexp(c)
		if err != nil {
			log.Printf("Failed to parse regexp %q: %v", c.Regexp, err)
			return false
		}
		return re.MatchString(s)
	}

	funcs := stringConstraintFuncs
	if c.CaseInsensitive {
		funcs = stringConstraintFuncsFold
	}
	for _, pair := range funcs {
		if v := pair.v(c); v != "" && !pair.fn(s, v) {
			return false
		}
	}
	return true
}

type TimeConstraint = searchtypes.TimeConstraint

type ClaimConstraint = searchtypes.ClaimConstraint

type LogicalConstraint = searchtypes.LogicalConstraint

// PermanodeConstraint matches permanodes.
type PermanodeConstraint = searchtypes.PermanodeConstraint

type PermanodeContinueConstraint = searchtypes.PermanodeContinueConstraint

func permanodeContinueConstraintCheckValid(pcc *PermanodeContinueConstraint) error {
	if pcc.LastMod.IsZero() == pcc.LastCreated.IsZero() {
		return errors.New("exactly one of PermanodeContinueConstraint LastMod or LastCreated must be defined")
	}
	return nil
}

type RelationConstraint = searchtypes.RelationConstraint

func relationConstraintCheckValid(rc *RelationConstraint) error {
	if rc.Relation != "parent" && rc.Relation != "child" {
		return errors.New("only RelationConstraint.Relation of \"parent\" or \"child\" is currently supported")
	}
	if (rc.Any == nil) == (rc.All == nil) {
		return errors.New("exactly one of RelationConstraint Any or All must be defined")
	}
	return nil
}

func relationConstraintMatchesAttr(rc *RelationConstraint, attr string) bool {
	if rc.EdgeType != "" {
		return attr == rc.EdgeType
	}
	return attr == "camliMember" || strings.HasPrefix(attr, "camliPath:")
}

// The PermanodeConstraint matching of RelationConstraint.
func relationConstraintMatch(rc *RelationConstraint, ctx context.Context, s *search, pn blob.Ref, at time.Time) (ok bool, err error) {
	corpus := s.h.corpus
	if corpus == nil {
		// TODO: care?
		return false, errors.New("RelationConstraint requires an in-memory corpus")
	}

	var foreachClaim func(pn blob.Ref, at time.Time, f func(cl *camtypes.Claim) bool)
	// relationRef returns the relevant blobRef from the claim if cl defines
	// the kind of relation we are looking for, (blob.Ref{}, false) otherwise.
	var relationRef func(cl *camtypes.Claim) (blob.Ref, bool)
	switch rc.Relation {
	case "parent":
		foreachClaim = corpus.ForeachClaimBack
		relationRef = func(cl *camtypes.Claim) (blob.Ref, bool) { return cl.Permanode, true }
	case "child":
		foreachClaim = corpus.ForeachClaim
		relationRef = func(cl *camtypes.Claim) (blob.Ref, bool) { return blob.Parse(cl.Value) }
	default:
		panic("bogus")
	}

	var matcher matchFn
	if rc.Any != nil {
		matcher = constraintMatcher(rc.Any)
	} else {
		matcher = constraintMatcher(rc.All)
	}

	var anyGood bool
	var anyBad bool
	var lastChecked blob.Ref
	var permanodesChecked map[blob.Ref]bool // lazily created to optimize for common case of 1 match
	foreachClaim(pn, at, func(cl *camtypes.Claim) bool {
		if !relationConstraintMatchesAttr(rc, cl.Attr) {
			return true // skip claim
		}
		if lastChecked.Valid() {
			if permanodesChecked == nil {
				permanodesChecked = make(map[blob.Ref]bool)
			}
			permanodesChecked[lastChecked] = true
			lastChecked = blob.Ref{} // back to zero
		}
		relRef, ok := relationRef(cl)
		if !ok {
			// The claim does not define the kind of relation we're looking for
			// (e.g. it sets a tag vale), so we continue to the next claim.
			return true
		}
		if permanodesChecked[relRef] {
			return true // skip checking
		}
		if !corpus.PermanodeHasAttrValue(cl.Permanode, at, cl.Attr, cl.Value) {
			return true // claim once matched permanode, but no longer
		}

		var bm camtypes.BlobMeta
		bm, err = s.blobMeta(ctx, relRef)
		if err != nil {
			return false
		}
		ok, err = matcher(ctx, s, relRef, bm)
		if err != nil {
			return false
		}
		if ok {
			anyGood = true
			if rc.Any != nil {
				return false // done. stop searching.
			}
		} else {
			anyBad = true
			if rc.All != nil {
				return false // fail fast
			}
		}
		lastChecked = relRef
		return true
	})
	if err != nil {
		return false, err
	}
	if rc.All != nil {
		return anyGood && !anyBad, nil
	}
	return anyGood, nil
}

// search is the state of an in-progress search
type search struct {
	h   *Handler
	q   *SearchQuery
	res *SearchResult

	// ss is a scratch string slice to avoid allocations.
	// We assume (at least so far) that only 1 goroutine is used
	// for a given search, so anything can use this.
	ss []string // scratch

	// loc is a cache of calculated locations.
	//
	// TODO: if location-of-permanode were cheaper and cached in
	// the corpus instead, then we wouldn't need this. And then
	// searches would be faster anyway. This is a hack.
	loc map[blob.Ref]camtypes.Location
}

func (s *search) blobMeta(ctx context.Context, br blob.Ref) (camtypes.BlobMeta, error) {
	if c := s.h.corpus; c != nil {
		return c.GetBlobMeta(ctx, br)
	}
	return s.h.index.GetBlobMeta(ctx, br)
}

func (s *search) fileInfo(ctx context.Context, br blob.Ref) (camtypes.FileInfo, error) {
	if c := s.h.corpus; c != nil {
		return c.GetFileInfo(ctx, br)
	}
	return s.h.index.GetFileInfo(ctx, br)
}

func (s *search) dirChildren(ctx context.Context, br blob.Ref) (map[blob.Ref]struct{}, error) {
	if c := s.h.corpus; c != nil {
		return c.GetDirChildren(ctx, br)
	}

	ch := make(chan blob.Ref)
	errch := make(chan error)
	go func() {
		errch <- s.h.index.GetDirMembers(ctx, br, ch, s.q.Limit)
	}()
	children := make(map[blob.Ref]struct{})
	for child := range ch {
		children[child] = struct{}{}
	}
	if err := <-errch; err != nil {
		return nil, err
	}
	return children, nil
}

func (s *search) parentDirs(ctx context.Context, br blob.Ref) (map[blob.Ref]struct{}, error) {
	c := s.h.corpus
	if c == nil {
		return nil, errors.New("parent directory search not supported without a corpus")
	}
	return c.GetParentDirs(ctx, br)
}

// optimizePlan returns an optimized version of c which will hopefully
// execute faster than executing c literally.
func optimizePlan(c *Constraint) *Constraint {
	// TODO: what the comment above says.
	return c
}

var debugQuerySpeed, _ = strconv.ParseBool(os.Getenv("CAMLI_DEBUG_QUERY_SPEED"))

func (h *Handler) Query(ctx context.Context, rawq *SearchQuery) (ret_ *SearchResult, _ error) {
	if debugQuerySpeed {
		t0 := time.Now()
		jq, _ := json.Marshal(rawq)
		log.Printf("[search=%p] Start %v, Doing search %s... ", rawq, t0.Format(time.RFC3339), jq)
		defer func() {
			d := time.Since(t0)
			if ret_ != nil {
				log.Printf("[search=%p] Start %v + %v = %v results", rawq, t0.Format(time.RFC3339), d, len(ret_.Blobs))
			} else {
				log.Printf("[search=%p] Start %v + %v = error", rawq, t0.Format(time.RFC3339), d)
			}
		}()
	}
	exprResult, err := searchQueryCheckValid(rawq, ctx)
	if err != nil {
		return nil, fmt.Errorf("Invalid SearchQuery: %w", err)
	}
	q := searchQueryPlannedQuery(rawq, exprResult)
	res := new(SearchResult)
	s := &search{
		h:   h,
		q:   q,
		res: res,
		loc: make(map[blob.Ref]camtypes.Location),
	}

	h.index.RLock()
	defer h.index.RUnlock()

	ctx, cancelSearch := context.WithCancel(ctx)
	defer cancelSearch()

	corpus := h.corpus

	cands := searchQueryPickCandidateSource(q, s)
	if candSourceHook != nil {
		candSourceHook(cands.name)
	}
	if debugQuerySpeed {
		log.Printf("[search=%p] using candidate source set %q", rawq, cands.name)
	}

	wantAround, foundAround := false, false
	if q.Around.Valid() {
		// TODO(mpl): fail somewhere if MapSorted and wantAround at the same time.
		wantAround = true
	}
	blobMatches := constraintMatcher(q.Constraint)

	var enumErr error
	sendErr := cands.send(ctx, s, func(meta camtypes.BlobMeta) bool {
		if ctx.Err() != nil {
			return false
		}
		match, err := blobMatches(ctx, s, meta.Ref, meta)
		if err != nil {
			enumErr = err
			return false
		}
		if match {
			res.Blobs = append(res.Blobs, &SearchResultBlob{
				Blob: meta.Ref,
			})
			if q.Sort == MapSort {
				// We need all the matching blobs to apply the MapSort selection afterwards, so
				// we temporarily ignore the limit.
				// TODO(mpl): the above means that we also ignore Continue and Around here. I
				// don't think we need them for the map aspect for now though.
				return true
			}
			if q.Limit <= 0 || !cands.sorted {
				if wantAround && !foundAround && q.Around == meta.Ref {
					foundAround = true
				}
				return true
			}
			if !wantAround || foundAround {
				return len(res.Blobs) != q.Limit
			}
			if q.Around == meta.Ref {
				foundAround = true
				if len(res.Blobs)*2 > q.Limit {
					// If we've already collected more than half of the Limit when Around is found,
					// we ditch the surplus from the beginning of the slice of results.
					// If Limit is even, and the number of results before and after Around
					// are both greater than half the limit, then there will be one more result before
					// than after.
					discard := max(len(res.Blobs)-q.Limit/2-1, 0)
					res.Blobs = res.Blobs[discard:]
				}
				if len(res.Blobs) == q.Limit {
					return false
				}
				return true
			}
			if len(res.Blobs) == q.Limit {
				n := copy(res.Blobs, res.Blobs[len(res.Blobs)/2:])
				res.Blobs = res.Blobs[:n]
			}
		}
		return true
	})
	if enumErr != nil {
		return nil, enumErr
	}
	if sendErr != nil {
		return nil, sendErr
	}
	// Some corpus candidate sources do not check the context themselves.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if wantAround && !foundAround {
		// results are ignored if Around was not found
		res.Blobs = nil
	}
	if !cands.sorted {
		switch q.Sort {
		// TODO(mpl): maybe someday we'll want both a sort, and then the MapSort
		// selection, as MapSort is technically not really a sort. In which case, MapSort
		// should probably become e.g. another field of SearchQuery.
		case UnspecifiedSort, Unsorted, MapSort:
			// Nothing to do.
		case BlobRefAsc:
			sort.Sort(sortSearchResultBlobs{res.Blobs, func(a, b *SearchResultBlob) bool {
				return a.Blob.Less(b.Blob)
			}})
		case CreatedDesc, CreatedAsc:
			if corpus == nil {
				return nil, errors.New("TODO: Sorting without a corpus unsupported")
			}
			if !constraintOnlyMatchesPermanode(q.Constraint) {
				return nil, errors.New("can only sort by ctime when all results are permanodes")
			}
			var err error
			sort.Sort(sortSearchResultBlobs{res.Blobs, func(a, b *SearchResultBlob) bool {
				if err != nil {
					return false
				}
				ta, ok := corpus.PermanodeAnyTime(a.Blob)
				if !ok {
					err = fmt.Errorf("no ctime or modtime found for %v", a.Blob)
					return false
				}
				tb, ok := corpus.PermanodeAnyTime(b.Blob)
				if !ok {
					err = fmt.Errorf("no ctime or modtime found for %v", b.Blob)
					return false
				}
				if q.Sort == CreatedAsc {
					return ta.Before(tb)
				}
				return tb.Before(ta)
			}})
			if err != nil {
				return nil, err
			}
		// TODO(mpl): LastModifiedDesc, LastModifiedAsc
		default:
			return nil, errors.New("TODO: unsupported sort+query combination.")
		}
		if q.Sort != MapSort {
			if q.Limit > 0 && len(res.Blobs) > q.Limit {
				if wantAround {
					aroundPos := sort.Search(len(res.Blobs), func(i int) bool {
						return res.Blobs[i].Blob.String() >= q.Around.String()
					})
					// If we got this far, we know q.Around is in the results, so this below should
					// never happen
					if aroundPos == len(res.Blobs) || res.Blobs[aroundPos].Blob != q.Around {
						panic("q.Around blobRef should be in the results")
					}
					lowerBound := max(aroundPos-q.Limit/2, 0)
					upperBound := min(lowerBound+q.Limit, len(res.Blobs))
					res.Blobs = res.Blobs[lowerBound:upperBound]
				} else {
					res.Blobs = res.Blobs[:q.Limit]
				}
			}
		}
	}
	if corpus != nil {
		if !wantAround {
			searchQuerySetResultContinue(q, corpus, res)
		}
	}

	// Populate s.res.LocationArea
	{
		var la camtypes.LocationBounds
		for _, v := range res.Blobs {
			br := v.Blob
			loc, ok := s.loc[br]
			if !ok {
				continue
			}
			la = la.Expand(loc)
		}
		if la != (camtypes.LocationBounds{}) {
			s.res.LocationArea = &la
		}
	}

	if q.Sort == MapSort {
		bestByLocation(s.res, s.loc, q.Limit)
	}

	if q.Describe != nil {
		q.Describe.BlobRef = blob.Ref{} // zero this out, if caller set it
		blobs := make([]blob.Ref, 0, len(res.Blobs))
		for _, srb := range res.Blobs {
			blobs = append(blobs, srb.Blob)
		}
		q.Describe.BlobRefs = blobs
		t0 := time.Now()
		res, err := s.h.DescribeLocked(ctx, q.Describe)
		if debugQuerySpeed {
			log.Printf("Describe of %d blobs = %v", len(blobs), time.Since(t0))
		}
		if err != nil {
			return nil, err
		}
		s.res.Describe = res
	}

	return s.res, nil
}

// mapCell is which cell of an NxN cell grid of a map a point is in.
// The numbering is arbitrary but dense, starting with 0.
type mapCell int

// mapGrids contains 1 or 2 mapGrids, depending on whether the search
// area cross the dateline.
type mapGrids []*mapGrid

func (gs mapGrids) cellOf(loc camtypes.Location) mapCell {
	for i, g := range gs {
		cell, ok := g.cellOf(loc)
		if ok {
			return cell + mapCell(i*g.dim*g.dim)
		}
	}
	return 0 // shouldn't happen, unless loc is malformed, in which case this is fine.
}

func newMapGrids(area camtypes.LocationBounds, dim int) mapGrids {
	if !area.SpansDateLine() {
		return mapGrids{newMapGrid(area, dim)}
	}
	return mapGrids{
		newMapGrid(camtypes.LocationBounds{
			North: area.North,
			South: area.South,
			West:  area.West,
			East:  180,
		}, dim),
		newMapGrid(camtypes.LocationBounds{
			North: area.North,
			South: area.South,
			West:  -180,
			East:  area.East,
		}, dim),
	}
}

type mapGrid struct {
	dim        int // grid is dim*dim cells
	area       camtypes.LocationBounds
	cellWidth  float64
	cellHeight float64
}

// newMapGrid returns a grid matcher over an area. The area must not
// span the date line. The mapGrid maps locations to a grid of (dim *
// dim) cells.
func newMapGrid(area camtypes.LocationBounds, dim int) *mapGrid {
	if area.SpansDateLine() {
		panic("invalid use of newMapGrid: must be called with bounds not overlapping date line")
	}
	return &mapGrid{
		dim:        dim,
		area:       area,
		cellWidth:  area.Width() / float64(dim),
		cellHeight: (area.North - area.South) / float64(dim),
	}
}

func (g *mapGrid) cellOf(loc camtypes.Location) (c mapCell, ok bool) {
	if loc.Latitude > g.area.North || loc.Latitude < g.area.South ||
		loc.Longitude < g.area.West || loc.Longitude > g.area.East {
		return
	}
	x := int((loc.Longitude - g.area.West) / g.cellWidth)
	y := int((g.area.North - loc.Latitude) / g.cellHeight)
	if x >= g.dim {
		x = g.dim - 1
	}
	if y >= g.dim {
		y = g.dim - 1
	}
	return mapCell(y*g.dim + x), true
}

// bestByLocation conditionally modifies res.Blobs if the number of blobs
// is greater than limit. If so, it modifies res.Blobs so only `limit`
// blobs remain, selecting those such that the results are evenly spread
// over the result's map area.
//
// The algorithm is the following:
// 1) We know the size and position of the relevant area because
// res.LocationArea was built during blob matching
// 2) We divide the area in a grid of ~sqrt(limit) lines and columns, which is
// represented by a map[camtypes.LocationBounds][]blob.Ref
// 3) For each described blobRef we place it in the cell matching its location.
// Each cell is bounded by limit though.
// 4) We compute the max number of nodes per cell:
// N = (number of non empty cells) / limit
// 5) for each cell, append to the set of selected nodes the first N nodes of
// the cell.
func bestByLocation(res *SearchResult, locm map[blob.Ref]camtypes.Location, limit int) {
	// Calculate res.LocationArea.
	if len(res.Blobs) <= limit {
		return
	}

	if res.LocationArea == nil {
		// No even one result node with a location was found.
		return
	}

	// Divide location area in a grid of (dim * dim) map cells,
	// such that (dim * dim) is approximately the given limit,
	// then track which search results are in which cell.
	cellOccupants := make(map[mapCell][]blob.Ref)
	dim := int(math.Round(math.Sqrt(float64(limit))))
	if dim < 3 {
		dim = 3
	} else if dim > 100 {
		dim = 100
	}
	grids := newMapGrids(*res.LocationArea, dim)

	resBlob := map[blob.Ref]*SearchResultBlob{}
	for _, srb := range res.Blobs {
		br := srb.Blob
		loc, ok := locm[br]
		if !ok {
			continue
		}
		cellKey := grids.cellOf(loc)
		occupants := cellOccupants[cellKey]
		if len(occupants) >= limit {
			// no sense in filling a cell to more than our overall limit
			continue
		}
		cellOccupants[cellKey] = append(occupants, br)
		resBlob[br] = srb
	}

	var nodesKept []*SearchResultBlob
	for {
		for cellKey, occupants := range cellOccupants {
			nodesKept = append(nodesKept, resBlob[occupants[0]])
			if len(nodesKept) == limit {
				res.Blobs = nodesKept
				return
			}
			if len(occupants) == 1 {
				delete(cellOccupants, cellKey)
			} else {
				cellOccupants[cellKey] = occupants[1:]
			}
		}

	}
}

// setResultContinue sets res.Continue if q is suitable for having a continue token.
// The corpus is locked for reads.
func searchQuerySetResultContinue(q *SearchQuery, corpus *index.Corpus, res *SearchResult) {
	if !constraintOnlyMatchesPermanode(q.Constraint) {
		return
	}
	var pnTimeFunc func(blob.Ref) (t time.Time, ok bool)
	switch q.Sort {
	case LastModifiedDesc:
		pnTimeFunc = corpus.PermanodeModtime
	case CreatedDesc:
		pnTimeFunc = corpus.PermanodeAnyTime
	default:
		return
	}

	if q.Limit <= 0 || len(res.Blobs) != q.Limit {
		return
	}
	lastpn := res.Blobs[len(res.Blobs)-1].Blob
	t, ok := pnTimeFunc(lastpn)
	if !ok {
		return
	}
	res.Continue = fmt.Sprintf("pn:%d:%v", t.UnixNano(), lastpn)
}

type matchFn func(context.Context, *search, blob.Ref, camtypes.BlobMeta) (bool, error)

func alwaysMatch(context.Context, *search, blob.Ref, camtypes.BlobMeta) (bool, error) {
	return true, nil
}

func neverMatch(context.Context, *search, blob.Ref, camtypes.BlobMeta) (bool, error) {
	return false, nil
}

func anyCamliType(ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
	return bm.CamliType != "", nil
}

// Test hooks.
var (
	candSourceHook     func(string)
	expandLocationHook bool
)

type candidateSource struct {
	name   string
	sorted bool

	// send calls the func for each matched blob, stopping early if the func
	// returns false or interruption from context.Done().
	send func(context.Context, *search, func(camtypes.BlobMeta) bool) error
}

func searchQueryPickCandidateSource(q *SearchQuery, s *search) (src candidateSource) {
	c := q.Constraint
	corpus := s.h.corpus
	if corpus != nil {
		if cands, ok := constraintBlobCandidates(c, corpus); ok {
			src.name = "corpus_candidates"
			brs := cands.Slice()
			sort.Slice(brs, func(i, j int) bool { return brs[i].Less(brs[j]) })
			if constraintOnlyMatchesPermanode(c) && (q.Sort == LastModifiedDesc || q.Sort == CreatedDesc) {
				// Sorted the same as the corpus_permanode sources
				// below, so Limit, Continue and Around work the same.
				src.sorted = true
				src.send = func(ctx context.Context, s *search, fn func(camtypes.BlobMeta) bool) error {
					if q.Sort == LastModifiedDesc {
						corpus.EnumeratePermanodesLastModifiedIn(fn, brs)
					} else {
						corpus.EnumeratePermanodesCreatedIn(fn, brs, true)
					}
					return nil
				}
				return
			}
			src.send = func(ctx context.Context, s *search, fn func(camtypes.BlobMeta) bool) error {
				for _, br := range brs {
					bm, err := corpus.GetBlobMeta(ctx, br)
					if err != nil {
						continue // no such blob
					}
					if !fn(bm) {
						break
					}
				}
				return nil
			}
			return
		}
		if constraintOnlyMatchesPermanode(c) {
			src.sorted = true
			switch q.Sort {
			case LastModifiedDesc:
				src.name = "corpus_permanode_lastmod"
				src.send = func(ctx context.Context, s *search, fn func(camtypes.BlobMeta) bool) error {
					corpus.EnumeratePermanodesLastModified(fn)
					return nil
				}
				return
			case CreatedDesc:
				src.name = "corpus_permanode_created"
				src.send = func(ctx context.Context, s *search, fn func(camtypes.BlobMeta) bool) error {
					corpus.EnumeratePermanodesCreated(fn, true)
					return nil
				}
				return
			default:
				src.sorted = false
				if typs := constraintMatchesPermanodeTypes(c); len(typs) != 0 {
					src.name = "corpus_permanode_types"
					src.send = func(ctx context.Context, s *search, fn func(camtypes.BlobMeta) bool) error {
						corpus.EnumeratePermanodesByNodeTypes(fn, typs)
						return nil
					}
					return
				}
			}
		}
		if c.AnyCamliType || c.CamliType != "" {
			camType := c.CamliType // empty means all
			src.name = "corpus_blob_meta"
			src.send = func(ctx context.Context, s *search, fn func(camtypes.BlobMeta) bool) error {
				corpus.EnumerateCamliBlobs(camType, fn)
				return nil
			}
			return
		}
	}
	src.name = "index_blob_meta"
	src.send = func(ctx context.Context, s *search, fn func(camtypes.BlobMeta) bool) error {
		return s.h.index.EnumerateBlobMeta(ctx, fn)
	}
	return
}

type allMustMatch []matchFn

func (fns allMustMatch) blobMatches(ctx context.Context, s *search, br blob.Ref, blobMeta camtypes.BlobMeta) (bool, error) {
	for _, condFn := range fns {
		match, err := condFn(ctx, s, br, blobMeta)
		if !match || err != nil {
			return match, err
		}
	}
	return true, nil
}

func constraintMatcher(c *Constraint) matchFn {
	return searchtypes.CachedMatcher(c, func() any { return constraintGenMatcher(c) }).(matchFn)
}

func constraintGenMatcher(c *Constraint) matchFn {
	var ncond int
	var cond matchFn
	var conds []matchFn
	addCond := func(fn matchFn) {
		ncond++
		if ncond == 1 {
			cond = fn
			return
		} else if ncond == 2 {
			conds = append(conds, cond)
		}
		conds = append(conds, fn)
	}
	if c.Logical != nil {
		addCond(logicalConstraintMatcher(c.Logical))
	}
	if c.Anything {
		addCond(alwaysMatch)
	}
	if c.CamliType != "" {
		addCond(func(ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
			return bm.CamliType == c.CamliType, nil
		})
	}
	if c.AnyCamliType {
		addCond(anyCamliType)
	}
	if pc := c.Permanode; pc != nil {
		addCond(func(ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
			return permanodeConstraintBlobMatches(pc, ctx, s, br, bm)
		})
	}
	// TODO: ClaimConstraint
	if fc := c.File; fc != nil {
		addCond(func(ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
			return fileConstraintBlobMatches(fc, ctx, s, br, bm)
		})
	}
	if dc := c.Dir; dc != nil {
		addCond(func(ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
			return dirConstraintBlobMatches(dc, ctx, s, br, bm)
		})
	}
	if bs := c.BlobSize; bs != nil {
		addCond(func(ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
			return intConstraintIntMatches(bs, int64(bm.Size)), nil
		})
	}
	if pfx := c.BlobRefPrefix; pfx != "" {
		addCond(func(ctx context.Context, s *search, br blob.Ref, meta camtypes.BlobMeta) (bool, error) {
			return br.HasPrefix(pfx), nil
		})
	}
	switch ncond {
	case 0:
		return neverMatch
	case 1:
		return cond
	default:
		return allMustMatch(conds).blobMatches
	}
}

func logicalConstraintCheckValid(c *LogicalConstraint) error {
	if c == nil {
		return nil
	}
	if c.A == nil {
		return errors.New("In LogicalConstraint, need to set A")
	}
	if err := constraintCheckValid(c.A); err != nil {
		return err
	}
	switch c.Op {
	case "and", "xor", "or":
		if c.B == nil {
			return errors.New("In LogicalConstraint, need both A and B set")
		}
		if err := constraintCheckValid(c.B); err != nil {
			return err
		}
	case "not":
	default:
		return fmt.Errorf("In LogicalConstraint, unknown operation %q", c.Op)
	}
	return nil
}

func logicalConstraintMatcher(c *LogicalConstraint) matchFn {
	amatches := constraintMatcher(c.A)
	var bmatches matchFn
	if c.Op != "not" {
		bmatches = constraintMatcher(c.B)
	}
	return func(ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {

		// Note: not using multiple goroutines here, because
		// so far the *search type assumes it's
		// single-threaded. (e.g. the .ss scratch type).
		// Also, not using multiple goroutines means we can
		// short-circuit when Op == "and" and av is false.

		av, err := amatches(ctx, s, br, bm)
		if err != nil {
			return false, err
		}
		switch c.Op {
		case "not":
			return !av, nil
		case "and":
			if !av {
				// Short-circuit.
				return false, nil
			}
		case "or":
			if av {
				// Short-circuit.
				return true, nil
			}
		}

		bv, err := bmatches(ctx, s, br, bm)
		if err != nil {
			return false, err
		}

		switch c.Op {
		case "and", "or":
			return bv, nil
		case "xor":
			return av != bv, nil
		}
		panic("unreachable")
	}
}

func permanodeConstraintCheckValid(c *PermanodeConstraint) error {
	if c == nil {
		return nil
	}
	if c.Attr != "" {
		if c.NumValue == nil && !permanodeConstraintHasValueConstraint(c) {
			return errors.New("PermanodeConstraint with Attr requires also setting NumValue or a value-matching constraint")
		}
		if nv := c.NumValue; nv != nil {
			if nv.ZeroMin {
				return errors.New("NumValue with ZeroMin makes no sense; matches everything")
			}
			if nv.ZeroMax && permanodeConstraintHasValueConstraint(c) {
				return errors.New("NumValue with ZeroMax makes no sense in conjunction with a value-matching constraint; matches nothing")
			}
			if nv.Min < 0 || nv.Max < 0 {
				return errors.New("NumValue with negative Min or Max makes no sense")
			}
		}
	}
	if rc := c.Relation; rc != nil {
		if err := relationConstraintCheckValid(rc); err != nil {
			return err
		}
	}
	if pcc := c.Continue; pcc != nil {
		if err := permanodeContinueConstraintCheckValid(pcc); err != nil {
			return err
		}
	}
	return nil
}

var numPermanodeFields = reflect.TypeOf(PermanodeConstraint{}).NumField()

// hasValueConstraint returns true if one or more constraints that check an attribute's value are set.
func permanodeConstraintHasValueConstraint(c *PermanodeConstraint) bool {
	// If a field has been added or removed, update this after adding the new field to the return statement if necessary.
	const expectedFields = 15
	if numPermanodeFields != expectedFields {
		panic(fmt.Sprintf("PermanodeConstraint field count changed (now %v rather than %v)", numPermanodeFields, expectedFields))
	}
	return c.Value != "" ||
		c.ValueMatches != nil ||
		c.ValueMatchesInt != nil ||
		c.ValueMatchesFloat != nil ||
		c.ValueInSet != nil
}

func permanodeConstraintBlobMatches(c *PermanodeConstraint, ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (ok bool, err error) {
	if bm.CamliType != schema.TypePermanode {
		return false, nil
	}
	corpus := s.h.corpus

	var dp *DescribedPermanode
	if corpus == nil {
		dr, err := s.h.DescribeLocked(ctx, &DescribeRequest{BlobRef: br})
		if err != nil {
			return false, err
		}
		db := dr.Meta[br.String()]
		if db == nil || db.Permanode == nil {
			return false, nil
		}
		dp = db.Permanode
	}

	if c.Attr != "" {
		if !c.At.IsZero() && corpus == nil {
			panic("PermanodeConstraint.At not supported without an in-memory corpus")
		}
		var vals []string
		if corpus == nil {
			vals = dp.Attr[c.Attr]
		} else {
			s.ss = corpus.AppendPermanodeAttrValues(
				s.ss[:0], br, c.Attr, c.At, s.h.owner.KeyID())
			vals = s.ss
		}
		ok, err := permanodeConstraintPermanodeMatchesAttrVals(c, ctx, s, vals)
		if !ok || err != nil {
			return false, err
		}
	}

	if c.SkipHidden && corpus != nil {
		defVis := corpus.PermanodeAttrValue(br, "camliDefVis", c.At, s.h.owner.KeyID())
		if defVis == "hide" {
			return false, nil
		}
		nodeType := corpus.PermanodeAttrValue(br, "camliNodeType", c.At, s.h.owner.KeyID())
		if nodeType == "foursquare.com:venue" {
			// TODO: temporary. remove this, or change
			// when/where (time) we show these.  But these
			// are flooding my results and I'm about to
			// demo this.
			return false, nil
		}
	}

	if c.ModTime != nil {
		if corpus != nil {
			mt, ok := corpus.PermanodeModtime(br)
			if !ok || !timeConstraintTimeMatches(c.ModTime, mt) {
				return false, nil
			}
		} else if !timeConstraintTimeMatches(c.ModTime, dp.ModTime) {
			return false, nil
		}
	}

	if c.Time != nil {
		if corpus != nil {
			t, ok := corpus.PermanodeAnyTime(br)
			if !ok || !timeConstraintTimeMatches(c.Time, t) {
				return false, nil
			}
		} else {
			panic("TODO: not yet supported")
		}
	}

	if rc := c.Relation; rc != nil {
		ok, err := relationConstraintMatch(rc, ctx, s, br, c.At)
		if !ok || err != nil {
			return ok, err
		}
	}

	if c.Location != nil || s.q.Sort == MapSort {
		l, err := s.h.lh.PermanodeLocation(ctx, br, c.At, s.h.owner)
		if c.Location != nil {
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					log.Printf("PermanodeLocation(ref %s): %v", br, err)
				}
				return false, nil
			}
			if !locationConstraintMatchesLatLong(c.Location, l.Latitude, l.Longitude) {
				return false, nil
			}
		}
		if err == nil {
			s.loc[br] = l
		}
	}

	if cc := c.Continue; cc != nil {
		if corpus == nil {
			// Requires an in-memory index for infinite
			// scroll. At least for now.
			return false, nil
		}
		var pnTime time.Time
		var ok bool
		switch {
		case !cc.LastMod.IsZero():
			pnTime, ok = corpus.PermanodeModtime(br)
			if !ok || pnTime.After(cc.LastMod) {
				return false, nil
			}
		case !cc.LastCreated.IsZero():
			pnTime, ok = corpus.PermanodeAnyTime(br)
			if !ok || pnTime.After(cc.LastCreated) {
				return false, nil
			}
		default:
			panic("Continue constraint without a LastMod or a LastCreated")
		}
		// Blobs are sorted by modtime, and then by
		// blobref, and then reversed overall.  From
		// top of page, imagining this scenario, where
		// the user requested a page size Limit of 4:
		//     mod5, sha1-25
		//     mod4, sha1-72
		//     mod3, sha1-cc
		//     mod3, sha1-bb <--- last seen item, continue = "pn:mod3:sha1-bb"
		//     mod3, sha1-aa  <-- and we want this one next.
		// In the case above, we'll see all of cc, bb, and cc for mod3.
		if (pnTime.Equal(cc.LastMod) || pnTime.Equal(cc.LastCreated)) && !br.Less(cc.Last) {
			return false, nil
		}
	}
	return true, nil
}

// permanodeMatchesAttrVals checks that the values in vals - all of them, if c.ValueAll is set -
// match the values for c.Attr.
// vals are the current permanode values of c.Attr.
func permanodeConstraintPermanodeMatchesAttrVals(c *PermanodeConstraint, ctx context.Context, s *search, vals []string) (bool, error) {
	if c.NumValue != nil && !intConstraintIntMatches(c.NumValue, int64(len(vals))) {
		return false, nil
	}
	if permanodeConstraintHasValueConstraint(c) {
		nmatch := 0
		for _, val := range vals {
			match, err := permanodeConstraintPermanodeMatchesAttrVal(c, ctx, s, val)
			if err != nil {
				return false, err
			}
			if match {
				nmatch++
			}
		}
		if nmatch == 0 {
			return false, nil
		}
		if c.ValueAll {
			return nmatch == len(vals), nil
		}
	}
	return true, nil
}

func permanodeConstraintPermanodeMatchesAttrVal(c *PermanodeConstraint, ctx context.Context, s *search, val string) (bool, error) {
	if c.Value != "" && c.Value != val {
		return false, nil
	}
	if c.ValueMatches != nil && !stringConstraintStringMatches(c.ValueMatches, val) {
		return false, nil
	}
	if c.ValueMatchesInt != nil {
		if i, err := strconv.ParseInt(val, 10, 64); err != nil || !intConstraintIntMatches(c.ValueMatchesInt, i) {
			return false, nil
		}
	}
	if c.ValueMatchesFloat != nil {
		if f, err := strconv.ParseFloat(val, 64); err != nil || !floatConstraintFloatMatches(c.ValueMatchesFloat, f) {
			return false, nil
		}
	}
	if subc := c.ValueInSet; subc != nil {
		br, ok := blob.Parse(val) // TODO: use corpus's parse, or keep this as blob.Ref in corpus attr
		if !ok {
			return false, nil
		}
		meta, err := s.blobMeta(ctx, br)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return constraintMatcher(subc)(ctx, s, br, meta)
	}
	return true, nil
}

func fileConstraintCheckValid(c *FileConstraint) error {
	return nil
}

func fileConstraintBlobMatches(c *FileConstraint, ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (ok bool, err error) {
	if bm.CamliType != "file" {
		return false, nil
	}
	fi, err := s.fileInfo(ctx, br)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if fs := c.FileSize; fs != nil && !intConstraintIntMatches(fs, fi.Size) {
		return false, nil
	}
	if c.IsImage && !strings.HasPrefix(fi.MIMEType, "image/") {
		return false, nil
	}
	if sc := c.FileName; sc != nil && !stringConstraintStringMatches(sc, fi.FileName) {
		return false, nil
	}
	if sc := c.MIMEType; sc != nil && !stringConstraintStringMatches(sc, fi.MIMEType) {
		return false, nil
	}
	if tc := c.Time; tc != nil {
		if fi.Time == nil || !timeConstraintTimeMatches(tc, fi.Time.Time()) {
			return false, nil
		}
	}
	if tc := c.ModTime; tc != nil {
		if fi.ModTime == nil || !timeConstraintTimeMatches(tc, fi.ModTime.Time()) {
			return false, nil
		}
	}
	if pc := c.ParentDir; pc != nil {
		parents, err := s.parentDirs(ctx, br)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		matches := false
		for parent := range parents {
			meta, err := s.blobMeta(ctx, parent)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return false, err
			}
			ok, err := dirConstraintBlobMatches(pc, ctx, s, parent, meta)
			if err != nil {
				return false, err
			}
			if ok {
				matches = true
				break
			}
		}
		if !matches {
			return false, nil
		}
	}
	corpus := s.h.corpus
	if sc := c.Camera; sc != nil {
		if corpus == nil {
			return false, nil
		}
		make, model, found := corpus.FileCamera(br)
		if !found || !((make != "" && stringConstraintStringMatches(sc, make)) || (model != "" && stringConstraintStringMatches(sc, model))) {
			return false, nil
		}
	}
	if c.WholeRef.Valid() {
		if corpus == nil {
			return false, nil
		}
		wholeRef, ok := corpus.GetWholeRef(ctx, br)
		if !ok || wholeRef != c.WholeRef {
			return false, nil
		}
	}
	var width, height int64
	if c.Width != nil || c.Height != nil || c.WHRatio != nil {
		if corpus == nil {
			return false, nil
		}
		imageInfo, err := corpus.GetImageInfo(ctx, br)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		width = int64(imageInfo.Width)
		height = int64(imageInfo.Height)
	}
	if c.Width != nil && !intConstraintIntMatches(c.Width, width) {
		return false, nil
	}
	if c.Height != nil && !intConstraintIntMatches(c.Height, height) {
		return false, nil
	}
	if c.WHRatio != nil && !floatConstraintFloatMatches(c.WHRatio, float64(width)/float64(height)) {
		return false, nil
	}
	if c.Location != nil {
		if corpus == nil {
			return false, nil
		}
		lat, long, found := corpus.FileLatLong(br)
		if !found || !locationConstraintMatchesLatLong(c.Location, lat, long) {
			return false, nil
		}
		// If location was successfully matched, add the
		// location to the global location area of results so
		// a sort-by-map doesn't need to look it up again
		// later.
		s.loc[br] = camtypes.Location{
			Latitude:  lat,
			Longitude: long,
		}
	} else if s.q.Sort == MapSort {
		if lat, long, found := corpus.FileLatLong(br); found {
			s.loc[br] = camtypes.Location{
				Latitude:  lat,
				Longitude: long,
			}
		}
	}
	// this makes sure, in conjunction with TestQueryFileLocation, that we only
	// expand the location iff the location matched AND the whole constraint matched as
	// well.
	if expandLocationHook {
		return false, nil
	}
	if mt := c.MediaTag; mt != nil {
		if corpus == nil {
			return false, nil
		}
		var tagValue string
		if mediaTags, err := corpus.GetMediaTags(ctx, br); err == nil && mt.Tag != "" {
			tagValue = mediaTags[mt.Tag]
		}
		if mt.Int != nil {
			if i, err := strconv.ParseInt(tagValue, 10, 64); err != nil || !intConstraintIntMatches(mt.Int, i) {
				return false, nil
			}
		}
		if mt.String != nil && !stringConstraintStringMatches(mt.String, tagValue) {
			return false, nil
		}
	}
	// TODO: EXIF timeconstraint
	return true, nil
}

func timeConstraintTimeMatches(c *TimeConstraint, t time.Time) bool {
	if t.IsZero() {
		return false
	}
	if !c.Before.IsAnyZero() {
		if !t.Before(time.Time(c.Before)) {
			return false
		}
	}
	after := time.Time(c.After)
	if after.IsZero() && c.InLast > 0 {
		after = time.Now().Add(-c.InLast)
	}
	if !after.IsZero() {
		if !(t.Equal(after) || t.After(after)) { // after is >=
			return false
		}
	}
	return true
}

func dirConstraintCheckValid(c *DirConstraint) error {
	if c == nil {
		return nil
	}
	if c.Contains != nil && c.RecursiveContains != nil {
		return errors.New("Contains and RecursiveContains in a DirConstraint are mutually exclusive")
	}
	return nil
}

func constraintIsFileOrDirConstraint(c *Constraint) bool {
	if l := c.Logical; l != nil {
		if l.Op == "not" {
			return constraintIsFileOrDirConstraint(l.A) // l.B is nil
		}
		return constraintIsFileOrDirConstraint(l.A) && constraintIsFileOrDirConstraint(l.B)
	}
	return c.File != nil || c.Dir != nil
}

func constraintFileOrDirOrLogicalMatches(c *Constraint, ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
	if cf := c.File; cf != nil {
		return fileConstraintBlobMatches(cf, ctx, s, br, bm)
	}
	if cd := c.Dir; cd != nil {
		return dirConstraintBlobMatches(cd, ctx, s, br, bm)
	}
	if l := c.Logical; l != nil {
		return logicalConstraintMatcher(l)(ctx, s, br, bm)
	}
	return false, nil
}

func dirConstraintBlobMatches(c *DirConstraint, ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
	if bm.CamliType != schema.TypeDirectory {
		return false, nil
	}
	// TODO(mpl): I've added c.BlobRefPrefix, so that c.ParentDir can be directly
	// matched against a blobRef (instead of e.g. a filename), but I could instead make
	// ParentDir be a *Constraint, and logically enforce that it has to "be equivalent"
	// to a ParentDir matching or a BlobRefPrefix matching. I think this here below is
	// simpler, but not sure it's best in the long run.
	if pfx := c.BlobRefPrefix; pfx != "" {
		if !br.HasPrefix(pfx) {
			return false, nil
		}
	}
	fi, err := s.fileInfo(ctx, br)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if sc := c.FileName; sc != nil && !stringConstraintStringMatches(sc, fi.FileName) {
		return false, nil
	}
	if pc := c.ParentDir; pc != nil {
		parents, err := s.parentDirs(ctx, br)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		isMatch, err := dirConstraintHasMatchingParent(pc, ctx, s, parents)
		if err != nil {
			return false, err
		}
		if !isMatch {
			return false, nil
		}
	}

	// All constraints not pertaining to children must happen above
	// this point.
	children, err := s.dirChildren(ctx, br)
	if err != nil && err != os.ErrNotExist {
		return false, err
	}
	if fc := c.TopFileCount; fc != nil && !intConstraintIntMatches(fc, int64(len(children))) {
		return false, nil
	}
	cc := c.Contains
	recursive := false
	if cc == nil {
		if crc := c.RecursiveContains; crc != nil {
			recursive = true
			// RecursiveContains implies Contains
			cc = crc
		}
	}
	// First test on the direct children
	containsMatch := false
	if cc != nil {
		// Allow directly specifying the fileRef
		if cc.BlobRefPrefix != "" {
			containsMatch, err = dirConstraintHasMatchingChild(c, ctx, s, children, func(ctx context.Context, s *search, child blob.Ref, bm camtypes.BlobMeta) (bool, error) {
				return child.HasPrefix(cc.BlobRefPrefix), nil
			})
		} else {
			if !constraintIsFileOrDirConstraint(cc) {
				return false, errors.New("[Recursive]Contains constraint should have a *FileConstraint, or a *DirConstraint, or a *LogicalConstraint combination of the aforementioned.")
			}
			containsMatch, err = dirConstraintHasMatchingChild(c, ctx, s, children, func(ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
				return constraintFileOrDirOrLogicalMatches(cc, ctx, s, br, bm)
			})
		}
		if err != nil {
			return false, err
		}
		if !containsMatch && !recursive {
			return false, nil
		}
	}
	// Then if needed recurse on the next generation descendants.
	if !containsMatch && recursive {
		match, err := dirConstraintHasMatchingChild(c, ctx, s, children, func(ctx context.Context, s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
			return dirConstraintBlobMatches(c, ctx, s, br, bm)
		})
		if err != nil {
			return false, err
		}
		if !match {
			return false, nil
		}
	}

	// TODO: implement FileCount and FileSize.

	return true, nil
}

// hasMatchingParent checks all parents against c and returns true as soon as one of
// them matches, or returns false if none of them is a match.
func dirConstraintHasMatchingParent(c *DirConstraint, ctx context.Context, s *search, parents map[blob.Ref]struct{}) (bool, error) {
	for parent := range parents {
		meta, err := s.blobMeta(ctx, parent)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}
		ok, err := dirConstraintBlobMatches(c, ctx, s, parent, meta)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// hasMatchingChild runs matcher against each child and returns true as soon as
// there is a match, of false if none of them is a match.
func dirConstraintHasMatchingChild(c *DirConstraint, ctx context.Context, s *search, children map[blob.Ref]struct{},
	matcher func(context.Context, *search, blob.Ref, camtypes.BlobMeta) (bool, error)) (bool, error) {
	// TODO(mpl): See if we're guaranteed to be CPU-bound (i.e. all resources are in
	// corpus), and if not, add some concurrency to spread costly index lookups.
	for child := range children {
		meta, err := s.blobMeta(ctx, child)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}
		ok, err := matcher(ctx, s, child, meta)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

type sortSearchResultBlobs struct {
	s    []*SearchResultBlob
	less func(a, b *SearchResultBlob) bool
}

func (ss sortSearchResultBlobs) Len() int           { return len(ss.s) }
func (ss sortSearchResultBlobs) Swap(i, j int)      { ss.s[i], ss.s[j] = ss.s[j], ss.s[i] }
func (ss sortSearchResultBlobs) Less(i, j int) bool { return ss.less(ss.s[i], ss.s[j]) }
