/*
Copyright 2013 The Camlistore Authors

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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/types"
	"camlistore.org/pkg/types/camtypes"
	"golang.org/x/net/context"

	"go4.org/strutil"
)

type SortType int

const (
	UnspecifiedSort SortType = iota
	Unsorted
	LastModifiedDesc
	LastModifiedAsc
	CreatedDesc
	CreatedAsc
	BlobRefAsc
	maxSortType
)

var sortName = map[SortType][]byte{
	Unsorted:         []byte(`"unsorted"`),
	LastModifiedDesc: []byte(`"-mod"`),
	LastModifiedAsc:  []byte(`"mod"`),
	CreatedDesc:      []byte(`"-created"`),
	CreatedAsc:       []byte(`"created"`),
	BlobRefAsc:       []byte(`"blobref"`),
}

func (t SortType) MarshalJSON() ([]byte, error) {
	v, ok := sortName[t]
	if !ok {
		panic("unnamed SortType " + strconv.Itoa(int(t)))
	}
	return v, nil
}

func (t *SortType) UnmarshalJSON(v []byte) error {
	for n, nv := range sortName {
		if bytes.Equal(v, nv) {
			*t = n
			return nil
		}
	}
	return fmt.Errorf("Bogus search sort type %q", v)
}

type SearchQuery struct {
	// Exactly one of Expression or Contraint must be set.
	// If an Expression is set, it's compiled to a Constraint.

	// Expression is a textual search query in minimal form,
	// e.g. "hawaii before:2008" or "tag:foo" or "foo" or "location:portland"
	// See expr.go and expr_test.go for all the operators.
	Expression string      `json:"expression,omitempty"`
	Constraint *Constraint `json:"constraint,omitempty"`

	// Limit is the maximum number of returned results. A negative value means no
	// limit. If unspecified, a default (of 200) will be used.
	Limit int `json:"limit,omitempty"`

	// Sort specifies how the results will be sorted. It defaults to CreatedDesc when the
	// query is about permanodes only.
	Sort SortType `json:"sort,omitempty"`

	// Around specifies that the results, after sorting, should be centered around
	// this result. If Around is not found the returned results will be empty.
	// If both Continue and Around are set, an error is returned.
	Around blob.Ref `json:"around,omitempty"`

	// Continue specifies the opaque token (as returned by a
	// SearchResult) for where to continue fetching results when
	// the Limit on a previous query was interrupted.
	// Continue is only valid for the same query (Expression or Constraint),
	// Limit, and Sort values.
	// If empty, the top-most query results are returned, as given
	// by Limit and Sort.
	// Continue is not compatible with the Around option.
	Continue string `json:"continue,omitempty"`

	// If Describe is specified, the matched blobs are also described,
	// as if the Describe.BlobRefs field was populated.
	Describe *DescribeRequest `json:"describe,omitempty"`
}

func (q *SearchQuery) URLSuffix() string { return "camli/search/query" }

func (q *SearchQuery) fromHTTP(req *http.Request) error {
	dec := json.NewDecoder(io.LimitReader(req.Body, 1<<20))
	if err := dec.Decode(q); err != nil {
		return err
	}

	if q.Constraint == nil && q.Expression == "" {
		return errors.New("query must have at least a constraint or an expression")
	}

	return nil
}

// exprQuery optionally specifies the *SearchQuery prototype that was generated
// by parsing the search expression
func (q *SearchQuery) plannedQuery(expr *SearchQuery) *SearchQuery {
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
		if pq.Constraint.onlyMatchesPermanode() {
			pq.Sort = CreatedDesc
		}
	}
	if pq.Limit == 0 {
		pq.Limit = 200 // arbitrary
	}
	if err := pq.addContinueConstraint(); err != nil {
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
func (q *SearchQuery) addContinueConstraint() error {
	cont := q.Continue
	if cont == "" {
		return nil
	}
	if q.Constraint.onlyMatchesPermanode() {
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

func (q *SearchQuery) checkValid(ctx context.Context) (sq *SearchQuery, err error) {
	if q.Sort >= maxSortType || q.Sort < 0 {
		return nil, errors.New("invalid sort type")
	}
	if q.Continue != "" && q.Around.Valid() {
		return nil, errors.New("Continue and Around parameters are mutually exclusive")
	}
	if q.Constraint == nil {
		if expr := q.Expression; expr != "" {
			sq, err := parseExpression(ctx, expr)
			if err != nil {
				return nil, fmt.Errorf("Error parsing search expression %q: %v", expr, err)
			}
			if err := sq.Constraint.checkValid(); err != nil {
				return nil, fmt.Errorf("Internal error: parseExpression(%q) returned invalid constraint: %v", expr, err)
			}
			return sq, nil
		}
		return nil, errors.New("no search constraint or expression")
	}
	return nil, q.Constraint.checkValid()
}

// SearchResult is the result of the Search method for a given SearchQuery.
type SearchResult struct {
	Blobs    []*SearchResultBlob `json:"blobs"`
	Describe *DescribeResponse   `json:"description"`

	// Continue optionally specifies the continuation token to to
	// continue fetching results in this result set, if interrupted
	// by a Limit.
	Continue string `json:"continue,omitempty"`
}

type SearchResultBlob struct {
	Blob blob.Ref `json:"blob"`
	// ... file info, permanode info, blob info ... ?
}

func (r *SearchResultBlob) String() string {
	return fmt.Sprintf("[blob: %s]", r.Blob)
}

// Constraint specifies a blob matching constraint.
// A blob matches if it matches all non-zero fields' predicates.
// A zero constraint matches nothing.
type Constraint struct {
	// If Logical is non-nil, all other fields are ignored.
	Logical *LogicalConstraint `json:"logical,omitempty"`

	// Anything, if true, matches all blobs.
	Anything bool `json:"anything,omitempty"`

	CamliType     string `json:"camliType,omitempty"`    // camliType of the JSON blob
	AnyCamliType  bool   `json:"anyCamliType,omitempty"` // if true, any camli JSON blob matches
	BlobRefPrefix string `json:"blobRefPrefix,omitempty"`

	File *FileConstraint `json:"file,omitempty"`
	Dir  *DirConstraint  `json:"dir,omitempty"`

	Claim    *ClaimConstraint `json:"claim,omitempty"`
	BlobSize *IntConstraint   `json:"blobSize,omitempty"`

	Permanode *PermanodeConstraint `json:"permanode,omitempty"`

	matcherOnce sync.Once
	matcherFn   matchFn
}

func (c *Constraint) checkValid() error {
	type checker interface {
		checkValid() error
	}
	if c.Claim != nil {
		return errors.New("TODO: implement ClaimConstraint")
	}
	for _, cv := range []checker{
		c.Logical,
		c.File,
		c.Dir,
		c.BlobSize,
		c.Permanode,
	} {
		if err := cv.checkValid(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Constraint) onlyMatchesPermanode() bool {
	if c.Permanode != nil || c.CamliType == "permanode" {
		return true
	}

	if c.Logical != nil && c.Logical.Op == "and" {
		if c.Logical.A.onlyMatchesPermanode() || c.Logical.B.onlyMatchesPermanode() {
			return true
		}
	}

	// TODO: There are other cases we can return true here, like:
	// Logical:{Op:'or', A:PermanodeConstraint{...}, B:PermanodeConstraint{...}

	return false
}

type FileConstraint struct {
	// (All non-zero fields must match)

	FileSize *IntConstraint    `json:"fileSize,omitempty"`
	FileName *StringConstraint `json:"fileName,omitempty"`
	MIMEType *StringConstraint `json:"mimeType,omitempty"`
	Time     *TimeConstraint   `json:"time,omitempty"`
	ModTime  *TimeConstraint   `json:"modTime,omitempty"`

	// WholeRef if non-zero only matches if the entire checksum of the
	// file (the concatenation of all its blobs) is equal to the
	// provided blobref. The index may not have every file's digest for
	// every known hash algorithm.
	WholeRef blob.Ref `json:"wholeRef,omitempty"`

	// For images:
	IsImage  bool                `json:"isImage,omitempty"`
	EXIF     *EXIFConstraint     `json:"exif,omitempty"` // TODO: implement
	Width    *IntConstraint      `json:"width,omitempty"`
	Height   *IntConstraint      `json:"height,omitempty"`
	WHRatio  *FloatConstraint    `json:"widthHeightRation,omitempty"`
	Location *LocationConstraint `json:"location,omitempty"`

	// MediaTag is for ID3 (and similar) embedded metadata in files.
	MediaTag *MediaTagConstraint `json:"mediaTag,omitempty"`
}

type MediaTagConstraint struct {
	// Tag is the tag to match.
	// For ID3, this includes: title, artist, album, genre, musicbrainzalbumid, year, track, disc, mediaref, durationms.
	Tag string `json:"tag"`

	String *StringConstraint `json:"string,omitempty"`
	Int    *IntConstraint    `json:"int,omitempty"`
}

type DirConstraint struct {
	// (All non-zero fields must match)

	// TODO: implement. mostly need more things in the index.

	FileName *StringConstraint

	TopFileSize, // not recursive
	TopFileCount, // not recursive
	FileSize,
	FileCount *IntConstraint

	// TODO: these would need thought on how to index efficiently:
	// (Also: top-only variants?)
	// ContainsFile *FileConstraint
	// ContainsDir  *DirConstraint
}

// An IntConstraint specifies constraints on an integer.
type IntConstraint struct {
	// Min and Max are both optional and inclusive bounds.
	// Zero means don't check.
	Min     int64 `json:"min,omitempty"`
	Max     int64 `json:"max,omitempty"`
	ZeroMin bool  `json:"zeroMin,omitempty"` // if true, min is actually zero
	ZeroMax bool  `json:"zeroMax,omitempty"` // if true, max is actually zero
}

func (c *IntConstraint) hasMin() bool { return c.Min != 0 || c.ZeroMin }
func (c *IntConstraint) hasMax() bool { return c.Max != 0 || c.ZeroMax }

func (c *IntConstraint) checkValid() error {
	if c == nil {
		return nil
	}
	if c.ZeroMin && c.Min != 0 {
		return errors.New("in IntConstraint, can't set both ZeroMin and Min")
	}
	if c.ZeroMax && c.Max != 0 {
		return errors.New("in IntConstraint, can't set both ZeroMax and Max")
	}
	if c.hasMax() && c.hasMin() && c.Min > c.Max {
		return errors.New("in IntConstraint, min is greater than max")
	}
	return nil
}

func (c *IntConstraint) intMatches(v int64) bool {
	if c.hasMin() && v < c.Min {
		return false
	}
	if c.hasMax() && v > c.Max {
		return false
	}
	return true
}

// A FloatConstraint specifies constraints on a float.
type FloatConstraint struct {
	// Min and Max are both optional and inclusive bounds.
	// Zero means don't check.
	Min     float64 `json:"min,omitempty"`
	Max     float64 `json:"max,omitempty"`
	ZeroMin bool    `json:"zeroMin,omitempty"` // if true, min is actually zero
	ZeroMax bool    `json:"zeroMax,omitempty"` // if true, max is actually zero
}

func (c *FloatConstraint) hasMin() bool { return c.Min != 0 || c.ZeroMin }
func (c *FloatConstraint) hasMax() bool { return c.Max != 0 || c.ZeroMax }

func (c *FloatConstraint) checkValid() error {
	if c == nil {
		return nil
	}
	if c.ZeroMin && c.Min != 0 {
		return errors.New("in FloatConstraint, can't set both ZeroMin and Min")
	}
	if c.ZeroMax && c.Max != 0 {
		return errors.New("in FloatConstraint, can't set both ZeroMax and Max")
	}
	if c.hasMax() && c.hasMin() && c.Min > c.Max {
		return errors.New("in FloatConstraint, min is greater than max")
	}
	return nil
}

func (c *FloatConstraint) floatMatches(v float64) bool {
	if c.hasMin() && v < c.Min {
		return false
	}
	if c.hasMax() && v > c.Max {
		return false
	}
	return true
}

type EXIFConstraint struct {
	// TODO.  need to put this in the index probably.
	// Maybe: GPS *LocationConstraint
	// ISO, Aperature, Camera Make/Model, etc.
}

type LocationConstraint struct {
	// Any, if true, matches any photo with a known location.
	Any bool

	// North, West, East, and South define a region in which a photo
	// must be in order to match.
	North float64
	West  float64
	East  float64
	South float64
}

func (c *LocationConstraint) matchesLatLong(lat, long float64) bool {
	return c.Any || (c.West <= long && long <= c.East && c.South <= lat && lat <= c.North)
}

// A StringConstraint specifies constraints on a string.
// All non-zero must match.
type StringConstraint struct {
	Empty           bool           `json:"empty,omitempty"` // matches empty string
	Equals          string         `json:"equals,omitempty"`
	Contains        string         `json:"contains,omitempty"`
	HasPrefix       string         `json:"hasPrefix,omitempty"`
	HasSuffix       string         `json:"hasSuffix,omitempty"`
	ByteLength      *IntConstraint `json:"byteLength,omitempty"` // length in bytes (not chars)
	CaseInsensitive bool           `json:"caseInsensitive,omitempty"`

	// TODO: CharLength (assume UTF-8)
}

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

func (c *StringConstraint) stringMatches(s string) bool {
	if c.Empty && len(s) > 0 {
		return false
	}
	if c.ByteLength != nil && !c.ByteLength.intMatches(int64(len(s))) {
		return false
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

type TimeConstraint struct {
	Before types.Time3339 `json:"before"` // <
	After  types.Time3339 `json:"after"`  // >=

	// TODO: this won't JSON-marshal/unmarshal well. Make a time.Duration marshal type?
	// Likewise with time that supports omitempty?
	InLast time.Duration `json:"inLast"` // >=
}

type ClaimConstraint struct {
	SignedBy     string    `json:"signedBy"` // identity
	SignedAfter  time.Time `json:"signedAfter"`
	SignedBefore time.Time `json:"signedBefore"`
}

func (c *ClaimConstraint) checkValid() error {
	return errors.New("TODO: implement blobMatches and checkValid on ClaimConstraint")
}

type LogicalConstraint struct {
	Op string      `json:"op"` // "and", "or", "xor", "not"
	A  *Constraint `json:"a"`
	B  *Constraint `json:"b"` // only valid if Op != "not"
}

// PermanodeConstraint matches permanodes.
type PermanodeConstraint struct {
	// At specifies the time at which to pretend we're resolving attributes.
	// Attribute claims after this point in time are ignored.
	// If zero, the current time is used.
	At time.Time `json:"at,omitempty"`

	// ModTime optionally matches on the last modtime of the permanode.
	ModTime *TimeConstraint `json:"modTime,omitempty"`

	// Time optionally matches the permanode's time. A Permanode
	// may not have a known time. If the permanode does not have a
	// known time, one may be guessed if the top-level search
	// parameters request so.
	Time *TimeConstraint `json:"time,omitempty"`

	// Attr optionally specifies the attribute to match.
	// e.g. "camliContent", "camliMember", "tag"
	// This is required if any of the items below are used.
	Attr string `json:"attr,omitempty"`

	// SkipHidden skips hidden or other boring files.
	SkipHidden bool `json:"skipHidden,omitempty"`

	// NumValue optionally tests the number of values this
	// permanode has for Attr.
	NumValue *IntConstraint `json:"numValue,omitempty"`

	// ValueAll modifies the matching behavior when an attribute
	// is multi-valued.  By default, when ValueAll is false, only
	// one value of a multi-valued attribute needs to match. If
	// ValueAll is true, all attributes must match.
	ValueAll bool `json:"valueAllMatch,omitempty"`

	// Value specifies an exact string to match.
	// This is a convenience form for the simple case of exact
	// equality. The same can be accomplished with ValueMatches.
	Value string `json:"value,omitempty"` // if non-zero, absolute match

	// ValueMatches optionally specifies a StringConstraint to
	// match the value against.
	ValueMatches *StringConstraint `json:"valueMatches,omitempty"`

	// ValueMatchesInt optionally specifies an IntConstraint to match
	// the value against. Non-integer values will not match.
	ValueMatchesInt *IntConstraint `json:"valueMatchesInt,omitempty"`

	// ValueMatchesFloat optionally specifies a FloatConstraint to match
	// the value against. Non-float values will not match.
	ValueMatchesFloat *FloatConstraint `json:"valueMatchesFloat,omitempty"`

	// ValueInSet optionally specifies a sub-query which the value
	// (which must be a blobref) must be a part of.
	ValueInSet *Constraint `json:"valueInSet,omitempty"`

	// Relation optionally specifies a constraint based on relations
	// to other permanodes (e.g. camliMember or camliPath sets).
	// You can use it to test the properties of a parent, ancestor,
	// child, or progeny.
	Relation *RelationConstraint `json:"relation,omitempty"`

	// Location optionally restricts matches to permanodes having
	// this location. This only affects permanodes with a known
	// type to have an lat/long location.
	Location *LocationConstraint `json:"location,omitempty"`

	// Continue is for internal use.
	Continue *PermanodeContinueConstraint `json:"-"`

	// TODO:
	// NumClaims *IntConstraint  // by owner
	// Owner  blob.Ref // search for permanodes by an owner

	// Note: When adding a field, update hasValueConstraint.
}

type PermanodeContinueConstraint struct {
	// LastMod if non-zero is the modtime of the last item
	// that was seen. One of this or LastCreated will be set.
	LastMod time.Time

	// LastCreated if non-zero is the creation time of the last
	// item that was seen.
	LastCreated time.Time

	// Last is the last blobref that was shown at the time
	// given in ModLessEqual or CreateLessEqual.
	// This is used as a tie-breaker.
	// If the time is equal, permanodes <= this are not matched.
	// If the time is past this in the scroll position, then this
	// field is ignored.
	Last blob.Ref
}

func (pcc *PermanodeContinueConstraint) checkValid() error {
	if pcc.LastMod.IsZero() == pcc.LastCreated.IsZero() {
		return errors.New("exactly one of PermanodeContinueConstraint LastMod or LastCreated must be defined")
	}
	return nil
}

type RelationConstraint struct {
	// Relation must be one of:
	//   * "child"
	//   * "parent" (immediate parent only)
	//   * "progeny" (any level down)
	//   * "ancestor" (any level up)
	Relation string

	// EdgeType optionally specifies an edge type.
	// By default it matches "camliMember" and "camliPath:*".
	EdgeType string

	// After finding all the nodes matching the Relation and
	// EdgeType, either one or all (depending on whether Any or
	// All is set) must then match for the RelationConstraint
	// itself to match.
	//
	// It is an error to set both.
	Any, All *Constraint
}

func (rc *RelationConstraint) checkValid() error {
	if rc.Relation != "parent" {
		return errors.New("only RelationConstraint.Relation of \"parent\" is currently supported")
	}
	if (rc.Any == nil) == (rc.All == nil) {
		return errors.New("exactly one of RelationConstraint Any or All must be defined")
	}
	return nil
}

func (rc *RelationConstraint) matchesAttr(attr string) bool {
	if rc.EdgeType != "" {
		return attr == rc.EdgeType
	}
	return attr == "camliMember" || strings.HasPrefix(attr, "camliPath:")
}

// The PermanodeConstraint matching of RelationConstraint.
func (rc *RelationConstraint) match(s *search, pn blob.Ref, at time.Time) (ok bool, err error) {
	corpus := s.h.corpus
	if corpus == nil {
		// TODO: care?
		return false, errors.New("RelationConstraint requires an in-memory corpus")
	}

	if rc.Relation != "parent" {
		panic("bogus")
	}

	var matcher matchFn
	if rc.Any != nil {
		matcher = rc.Any.matcher()
	} else {
		matcher = rc.All.matcher()
	}

	var anyGood bool
	var anyBad bool
	var lastChecked blob.Ref
	var permanodesChecked map[blob.Ref]bool // lazily created to optimize for common case of 1 match
	corpus.ForeachClaimBackLocked(pn, at, func(cl *camtypes.Claim) bool {
		if !rc.matchesAttr(cl.Attr) {
			return true // skip claim
		}
		if lastChecked.Valid() {
			if permanodesChecked == nil {
				permanodesChecked = make(map[blob.Ref]bool)
			}
			permanodesChecked[lastChecked] = true
			lastChecked = blob.Ref{} // back to zero
		}
		if permanodesChecked[cl.Permanode] {
			return true // skip checking
		}
		if !corpus.PermanodeHasAttrValueLocked(cl.Permanode, at, cl.Attr, cl.Value) {
			return true // claim once matched permanode, but no longer
		}

		var bm camtypes.BlobMeta
		bm, err = s.blobMeta(cl.Permanode)
		if err != nil {
			return false
		}
		var ok bool
		ok, err = matcher(s, cl.Permanode, bm)
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
		lastChecked = cl.Permanode
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
}

func (s *search) blobMeta(br blob.Ref) (camtypes.BlobMeta, error) {
	if c := s.h.corpus; c != nil {
		return c.GetBlobMetaLocked(br)
	} else {
		return s.h.index.GetBlobMeta(br)
	}
}

func (s *search) fileInfo(br blob.Ref) (camtypes.FileInfo, error) {
	if c := s.h.corpus; c != nil {
		return c.GetFileInfoLocked(br)
	} else {
		return s.h.index.GetFileInfo(br)
	}
}

// optimizePlan returns an optimized version of c which will hopefully
// execute faster than executing c literally.
func optimizePlan(c *Constraint) *Constraint {
	// TODO: what the comment above says.
	return c
}

func (h *Handler) Query(rawq *SearchQuery) (*SearchResult, error) {
	ctx := context.TODO() // TODO: set from rawq
	exprResult, err := rawq.checkValid(ctx)
	if err != nil {
		return nil, fmt.Errorf("Invalid SearchQuery: %v", err)
	}
	q := rawq.plannedQuery(exprResult)
	res := new(SearchResult)
	s := &search{
		h:   h,
		q:   q,
		res: res,
	}
	ctx, cancelSearch := context.WithCancel(context.TODO())
	defer cancelSearch()

	corpus := h.corpus
	var unlockOnce sync.Once
	if corpus != nil {
		corpus.RLock()
		defer unlockOnce.Do(corpus.RUnlock)
	}

	ch := make(chan camtypes.BlobMeta, buffered)
	errc := make(chan error, 1)

	cands := q.pickCandidateSource(s)
	if candSourceHook != nil {
		candSourceHook(cands.name)
	}

	sendCtx, cancelSend := context.WithCancel(ctx)
	defer cancelSend()
	go func() { errc <- cands.send(sendCtx, s, ch) }()

	wantAround, foundAround := false, false
	if q.Around.Valid() {
		wantAround = true
	}
	blobMatches := q.Constraint.matcher()
	for meta := range ch {
		match, err := blobMatches(s, meta.Ref, meta)
		if err != nil {
			return nil, err
		}
		if match {
			res.Blobs = append(res.Blobs, &SearchResultBlob{
				Blob: meta.Ref,
			})
			if q.Limit <= 0 || !cands.sorted {
				continue
			}
			if !wantAround || foundAround {
				if len(res.Blobs) == q.Limit {
					cancelSend()
					break
				}
				continue
			}
			if q.Around == meta.Ref {
				foundAround = true
				if len(res.Blobs)*2 > q.Limit {
					// If we've already collected more than half of the Limit when Around is found,
					// we ditch the surplus from the beginning of the slice of results.
					// If Limit is even, and the number of results before and after Around
					// are both greater than half the limit, then there will be one more result before
					// than after.
					discard := len(res.Blobs) - q.Limit/2 - 1
					if discard < 0 {
						discard = 0
					}
					res.Blobs = res.Blobs[discard:]
				}
				if len(res.Blobs) == q.Limit {
					cancelSend()
					break
				}
				continue
			}
			if len(res.Blobs) == q.Limit {
				n := copy(res.Blobs, res.Blobs[len(res.Blobs)/2:])
				res.Blobs = res.Blobs[:n]
			}
		}
	}
	if err := <-errc; err != nil && err != context.Canceled {
		return nil, err
	}
	if q.Limit > 0 && cands.sorted && wantAround && !foundAround {
		// results are ignored if Around was not found
		res.Blobs = nil
	}
	if !cands.sorted {
		switch q.Sort {
		case UnspecifiedSort, Unsorted:
			// Nothing to do.
		case BlobRefAsc:
			sort.Sort(sortSearchResultBlobs{res.Blobs, func(a, b *SearchResultBlob) bool {
				return a.Blob.Less(b.Blob)
			}})
		case CreatedDesc, CreatedAsc:
			if corpus == nil {
				return nil, errors.New("TODO: Sorting without a corpus unsupported")
			}
			var err error
			corpus.RLock()
			sort.Sort(sortSearchResultBlobs{res.Blobs, func(a, b *SearchResultBlob) bool {
				if err != nil {
					return false
				}
				ta, ok := corpus.PermanodeAnyTimeLocked(a.Blob)
				if !ok {
					err = fmt.Errorf("no ctime or modtime found for %v", a.Blob)
					return false
				}
				tb, ok := corpus.PermanodeAnyTimeLocked(b.Blob)
				if !ok {
					err = fmt.Errorf("no ctime or modtime found for %v", b.Blob)
					return false
				}
				if q.Sort == CreatedAsc {
					return ta.Before(tb)
				}
				return tb.Before(ta)
			}})
			corpus.RUnlock()
			if err != nil {
				return nil, err
			}
		// TODO(mpl): LastModifiedDesc, LastModifiedAsc
		default:
			return nil, errors.New("TODO: unsupported sort+query combination.")
		}
		if q.Limit > 0 && len(res.Blobs) > q.Limit {
			res.Blobs = res.Blobs[:q.Limit]
		}
	}
	if corpus != nil {
		if !wantAround {
			q.setResultContinue(corpus, res)
		}
		unlockOnce.Do(corpus.RUnlock)
	}

	if q.Describe != nil {
		q.Describe.BlobRef = blob.Ref{} // zero this out, if caller set it
		blobs := make([]blob.Ref, 0, len(res.Blobs))
		for _, srb := range res.Blobs {
			blobs = append(blobs, srb.Blob)
		}
		q.Describe.BlobRefs = blobs
		res, err := s.h.Describe(q.Describe)
		if err != nil {
			return nil, err
		}
		s.res.Describe = res
	}
	return s.res, nil
}

// setResultContinue sets res.Continue if q is suitable for having a continue token.
// The corpus is locked for reads.
func (q *SearchQuery) setResultContinue(corpus *index.Corpus, res *SearchResult) {
	if !q.Constraint.onlyMatchesPermanode() {
		return
	}
	var pnTimeFunc func(blob.Ref) (t time.Time, ok bool)
	switch q.Sort {
	case LastModifiedDesc:
		pnTimeFunc = corpus.PermanodeModtimeLocked
	case CreatedDesc:
		pnTimeFunc = corpus.PermanodeAnyTimeLocked
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

const camliTypeMIME = "application/json; camliType="

type matchFn func(*search, blob.Ref, camtypes.BlobMeta) (bool, error)

func alwaysMatch(*search, blob.Ref, camtypes.BlobMeta) (bool, error) {
	return true, nil
}

func neverMatch(*search, blob.Ref, camtypes.BlobMeta) (bool, error) {
	return false, nil
}

func anyCamliType(s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
	return bm.CamliType != "", nil
}

// Test hook.
var candSourceHook func(string)

type candidateSource struct {
	name   string
	sorted bool

	// sends sends to the channel and must close it, regardless of error
	// or interruption from context.Done().
	send func(context.Context, *search, chan<- camtypes.BlobMeta) error
}

func (q *SearchQuery) pickCandidateSource(s *search) (src candidateSource) {
	c := q.Constraint
	corpus := s.h.corpus
	if corpus != nil {
		if c.onlyMatchesPermanode() {
			src.sorted = true
			switch q.Sort {
			case LastModifiedDesc:
				src.name = "corpus_permanode_lastmod"
				src.send = func(ctx context.Context, s *search, dst chan<- camtypes.BlobMeta) error {
					return corpus.EnumeratePermanodesLastModifiedLocked(ctx, dst)
				}
				return
			case CreatedDesc:
				src.name = "corpus_permanode_created"
				src.send = func(ctx context.Context, s *search, dst chan<- camtypes.BlobMeta) error {
					return corpus.EnumeratePermanodesCreatedLocked(ctx, dst, true)
				}
				return
			default:
				src.sorted = false
			}
		}
		if c.AnyCamliType || c.CamliType != "" {
			camType := c.CamliType // empty means all
			src.name = "corpus_blob_meta"
			src.send = func(ctx context.Context, s *search, dst chan<- camtypes.BlobMeta) error {
				return corpus.EnumerateCamliBlobsLocked(ctx, camType, dst)
			}
			return
		}
	}
	src.name = "index_blob_meta"
	src.send = func(ctx context.Context, s *search, dst chan<- camtypes.BlobMeta) error {
		return s.h.index.EnumerateBlobMeta(ctx, dst)
	}
	return
}

type allMustMatch []matchFn

func (fns allMustMatch) blobMatches(s *search, br blob.Ref, blobMeta camtypes.BlobMeta) (bool, error) {
	for _, condFn := range fns {
		match, err := condFn(s, br, blobMeta)
		if !match || err != nil {
			return match, err
		}
	}
	return true, nil
}

func (c *Constraint) matcher() func(s *search, br blob.Ref, blobMeta camtypes.BlobMeta) (bool, error) {
	c.matcherOnce.Do(c.initMatcherFn)
	return c.matcherFn
}

func (c *Constraint) initMatcherFn() {
	c.matcherFn = c.genMatcher()
}

func (c *Constraint) genMatcher() matchFn {
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
		addCond(c.Logical.matcher())
	}
	if c.Anything {
		addCond(alwaysMatch)
	}
	if c.CamliType != "" {
		addCond(func(s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
			return bm.CamliType == c.CamliType, nil
		})
	}
	if c.AnyCamliType {
		addCond(anyCamliType)
	}
	if c.Permanode != nil {
		addCond(c.Permanode.blobMatches)
	}
	// TODO: ClaimConstraint
	if c.File != nil {
		addCond(c.File.blobMatches)
	}
	if c.Dir != nil {
		addCond(c.Dir.blobMatches)
	}
	if bs := c.BlobSize; bs != nil {
		addCond(func(s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
			return bs.intMatches(int64(bm.Size)), nil
		})
	}
	if pfx := c.BlobRefPrefix; pfx != "" {
		addCond(func(s *search, br blob.Ref, meta camtypes.BlobMeta) (bool, error) {
			return strings.HasPrefix(br.String(), pfx), nil
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

func (c *LogicalConstraint) checkValid() error {
	if c == nil {
		return nil
	}
	if c.A == nil {
		return errors.New("In LogicalConstraint, need to set A")
	}
	if err := c.A.checkValid(); err != nil {
		return err
	}
	switch c.Op {
	case "and", "xor", "or":
		if c.B == nil {
			return errors.New("In LogicalConstraint, need both A and B set")
		}
		if err := c.B.checkValid(); err != nil {
			return err
		}
	case "not":
	default:
		return fmt.Errorf("In LogicalConstraint, unknown operation %q", c.Op)
	}
	return nil
}

func (c *LogicalConstraint) matcher() matchFn {
	amatches := c.A.matcher()
	var bmatches matchFn
	if c.Op != "not" {
		bmatches = c.B.matcher()
	}
	return func(s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {

		// Note: not using multiple goroutines here, because
		// so far the *search type assumes it's
		// single-threaded. (e.g. the .ss scratch type).
		// Also, not using multiple goroutines means we can
		// short-circuit when Op == "and" and av is false.

		av, err := amatches(s, br, bm)
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

		bv, err := bmatches(s, br, bm)
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

func (c *PermanodeConstraint) checkValid() error {
	if c == nil {
		return nil
	}
	if c.Attr != "" {
		if c.NumValue == nil && !c.hasValueConstraint() {
			return errors.New("PermanodeConstraint with Attr requires also setting NumValue or a value-matching constraint")
		}
		if nv := c.NumValue; nv != nil {
			if nv.ZeroMin {
				return errors.New("NumValue with ZeroMin makes no sense; matches everything")
			}
			if nv.ZeroMax && c.hasValueConstraint() {
				return errors.New("NumValue with ZeroMax makes no sense in conjunction with a value-matching constraint; matches nothing")
			}
			if nv.Min < 0 || nv.Max < 0 {
				return errors.New("NumValue with negative Min or Max makes no sense")
			}
		}
	}
	if rc := c.Relation; rc != nil {
		if err := rc.checkValid(); err != nil {
			return err
		}
	}
	if pcc := c.Continue; pcc != nil {
		if err := pcc.checkValid(); err != nil {
			return err
		}
	}
	return nil
}

var numPermanodeFields = reflect.TypeOf(PermanodeConstraint{}).NumField()

// hasValueConstraint returns true if one or more constraints that check an attribute's value are set.
func (c *PermanodeConstraint) hasValueConstraint() bool {
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

func (c *PermanodeConstraint) blobMatches(s *search, br blob.Ref, bm camtypes.BlobMeta) (ok bool, err error) {
	if bm.CamliType != "permanode" {
		return false, nil
	}
	corpus := s.h.corpus

	var dp *DescribedPermanode
	if corpus == nil {
		dr, err := s.h.Describe(&DescribeRequest{BlobRef: br})
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
			s.ss = corpus.AppendPermanodeAttrValuesLocked(
				s.ss[:0], br, c.Attr, c.At, s.h.owner)
			vals = s.ss
		}
		ok, err := c.permanodeMatchesAttrVals(s, vals)
		if !ok || err != nil {
			return false, err
		}
	}

	if c.SkipHidden && corpus != nil {
		defVis := corpus.PermanodeAttrValueLocked(br, "camliDefVis", c.At, s.h.owner)
		if defVis == "hide" {
			return false, nil
		}
		nodeType := corpus.PermanodeAttrValueLocked(br, "camliNodeType", c.At, s.h.owner)
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
			mt, ok := corpus.PermanodeModtimeLocked(br)
			if !ok || !c.ModTime.timeMatches(mt) {
				return false, nil
			}
		} else if !c.ModTime.timeMatches(dp.ModTime) {
			return false, nil
		}
	}

	if c.Time != nil {
		if corpus != nil {
			t, ok := corpus.PermanodeAnyTimeLocked(br)
			if !ok || !c.Time.timeMatches(t) {
				return false, nil
			}
		} else {
			panic("TODO: not yet supported")
		}
	}

	if rc := c.Relation; rc != nil {
		ok, err := rc.match(s, br, c.At)
		if !ok || err != nil {
			return ok, err
		}
	}

	if c.Location != nil {
		if corpus == nil {
			return false, nil
		}
		lat, long, ok := corpus.PermanodeLatLongLocked(br, c.At)
		if !ok || !c.Location.matchesLatLong(lat, long) {
			return false, nil
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
			pnTime, ok = corpus.PermanodeModtimeLocked(br)
			if !ok || pnTime.After(cc.LastMod) {
				return false, nil
			}
		case !cc.LastCreated.IsZero():
			pnTime, ok = corpus.PermanodeAnyTimeLocked(br)
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
func (c *PermanodeConstraint) permanodeMatchesAttrVals(s *search, vals []string) (bool, error) {
	if c.NumValue != nil && !c.NumValue.intMatches(int64(len(vals))) {
		return false, nil
	}
	if c.hasValueConstraint() {
		nmatch := 0
		for _, val := range vals {
			match, err := c.permanodeMatchesAttrVal(s, val)
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

func (c *PermanodeConstraint) permanodeMatchesAttrVal(s *search, val string) (bool, error) {
	if c.Value != "" && c.Value != val {
		return false, nil
	}
	if c.ValueMatches != nil && !c.ValueMatches.stringMatches(val) {
		return false, nil
	}
	if c.ValueMatchesInt != nil {
		if i, err := strconv.ParseInt(val, 10, 64); err != nil || !c.ValueMatchesInt.intMatches(i) {
			return false, nil
		}
	}
	if c.ValueMatchesFloat != nil {
		if f, err := strconv.ParseFloat(val, 64); err != nil || !c.ValueMatchesFloat.floatMatches(f) {
			return false, nil
		}
	}
	if subc := c.ValueInSet; subc != nil {
		br, ok := blob.Parse(val) // TODO: use corpus's parse, or keep this as blob.Ref in corpus attr
		if !ok {
			return false, nil
		}
		meta, err := s.blobMeta(br)
		if err == os.ErrNotExist {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return subc.matcher()(s, br, meta)
	}
	return true, nil
}

func (c *FileConstraint) checkValid() error {
	return nil
}

func (c *FileConstraint) blobMatches(s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
	if bm.CamliType != "file" {
		return false, nil
	}
	fi, err := s.fileInfo(br)
	if err == os.ErrNotExist {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if fs := c.FileSize; fs != nil && !fs.intMatches(fi.Size) {
		return false, nil
	}
	if c.IsImage && !strings.HasPrefix(fi.MIMEType, "image/") {
		return false, nil
	}
	if sc := c.FileName; sc != nil && !sc.stringMatches(fi.FileName) {
		return false, nil
	}
	if sc := c.MIMEType; sc != nil && !sc.stringMatches(fi.MIMEType) {
		return false, nil
	}
	if tc := c.Time; tc != nil {
		if fi.Time == nil || !tc.timeMatches(fi.Time.Time()) {
			return false, nil
		}
	}
	if tc := c.ModTime; tc != nil {
		if fi.ModTime == nil || !tc.timeMatches(fi.ModTime.Time()) {
			return false, nil
		}
	}
	corpus := s.h.corpus
	if c.WholeRef.Valid() {
		if corpus == nil {
			return false, nil
		}
		wholeRef, ok := corpus.GetWholeRefLocked(br)
		if !ok || wholeRef != c.WholeRef {
			return false, nil
		}
	}
	var width, height int64
	if c.Width != nil || c.Height != nil || c.WHRatio != nil {
		if corpus == nil {
			return false, nil
		}
		imageInfo, err := corpus.GetImageInfoLocked(br)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		width = int64(imageInfo.Width)
		height = int64(imageInfo.Height)
	}
	if c.Width != nil && !c.Width.intMatches(width) {
		return false, nil
	}
	if c.Height != nil && !c.Height.intMatches(height) {
		return false, nil
	}
	if c.WHRatio != nil && !c.WHRatio.floatMatches(float64(width)/float64(height)) {
		return false, nil
	}
	if c.Location != nil {
		if corpus == nil {
			return false, nil
		}
		lat, long, ok := corpus.FileLatLongLocked(br)
		if ok && c.Location.Any {
			// Pass.
		} else if !ok || !c.Location.matchesLatLong(lat, long) {
			return false, nil
		}
	}
	if mt := c.MediaTag; mt != nil {
		if corpus == nil {
			return false, nil
		}
		var tagValue string
		if mediaTags, err := corpus.GetMediaTagsLocked(br); err == nil && mt.Tag != "" {
			tagValue = mediaTags[mt.Tag]
		}
		if mt.Int != nil {
			if i, err := strconv.ParseInt(tagValue, 10, 64); err != nil || !mt.Int.intMatches(i) {
				return false, nil
			}
		}
		if mt.String != nil && !mt.String.stringMatches(tagValue) {
			return false, nil
		}
	}
	// TOOD: EXIF timeconstraint
	return true, nil
}

func (c *TimeConstraint) timeMatches(t time.Time) bool {
	if t.IsZero() {
		return false
	}
	if !c.Before.IsZero() {
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

func (c *DirConstraint) checkValid() error {
	return nil
}

func (c *DirConstraint) blobMatches(s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
	if bm.CamliType != "directory" {
		return false, nil
	}

	// TODO: implement
	panic("TODO: implement DirConstraint.blobMatches")
}

type sortSearchResultBlobs struct {
	s    []*SearchResultBlob
	less func(a, b *SearchResultBlob) bool
}

func (ss sortSearchResultBlobs) Len() int           { return len(ss.s) }
func (ss sortSearchResultBlobs) Swap(i, j int)      { ss.s[i], ss.s[j] = ss.s[j], ss.s[i] }
func (ss sortSearchResultBlobs) Less(i, j int) bool { return ss.less(ss.s[i], ss.s[j]) }
