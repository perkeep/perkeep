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
	"strconv"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/types"
	"camlistore.org/pkg/types/camtypes"
)

type SortType int

const (
	UnspecifiedSort SortType = iota
	LastModifiedDesc
	LastModifiedAsc
	CreatedDesc
	CreatedAsc
	maxSortType
)

var sortName = map[SortType][]byte{
	LastModifiedDesc: []byte(`"-mod"`),
	LastModifiedAsc:  []byte(`"mod"`),
	CreatedDesc:      []byte(`"-created"`),
	CreatedAsc:       []byte(`"created"`),
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
	// If an Expression is set, it's compiled to an Constraint.

	// Expression is a textual search query in minimal form,
	// e.g. "hawaii before:2008" or "tag:foo" or "foo" or "location:portland"
	// See expr.go and expr_test.go for all the operators.
	Expression string      `json:"expression,omitempty"`
	Constraint *Constraint `json:"constraint,omitempty"`

	Limit int      `json:"limit,omitempty"` // optional. default is automatic. negative means no limit.
	Sort  SortType `json:"sort,omitempty"`  // optional. default is automatic or unsorted.

	// Continue specifies the opaque token (as returned by a
	// SearchResult) for where to continue fetching results when
	// the Limit on a previous query was interrupted.
	// Continue is only valid for the same query (Expression or Constraint),
	// Limit, and Sort values.
	// If empty, the top-most query results are returned, as given
	// by Limit and Sort.
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
	if pq.Sort == 0 {
		if pq.Constraint.onlyMatchesPermanode() {
			pq.Sort = LastModifiedDesc
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
		if q.Sort == LastModifiedDesc {
			baseConstraint := q.Constraint
			q.Constraint = &Constraint{
				Logical: &LogicalConstraint{
					Op: "and",
					A: &Constraint{
						Permanode: &PermanodeConstraint{
							Continue: &PermanodeContinueConstraint{
								LastMod: tokent,
								Last:    lastbr,
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

func (q *SearchQuery) checkValid(ctx *context.Context) (sq *SearchQuery, err error) {
	if q.Limit < 0 {
		return nil, errors.New("negative limit")
	}
	if q.Sort >= maxSortType || q.Sort < 0 {
		return nil, errors.New("invalid sort type")
	}
	if q.Constraint == nil {
		if expr := q.Expression; expr != "" {
			sq, err := parseExpression(ctx, expr)
			if err != nil {
				return nil, fmt.Errorf("Error parsing search expression %q: %v", expr, err)
			}
			if err := sq.Constraint.checkValid(); err != nil {
				log.Fatalf("Internal error: parseExpression(%q) returned invalid constraint: %v", expr, err)
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

	FileSize *IntConstraint `json:"fileSize,omitempty"`
	FileName *StringConstraint
	MIMEType *StringConstraint
	Time     *TimeConstraint
	ModTime  *TimeConstraint

	// For images:
	IsImage  bool                `json:"isImage,omitempty"`
	EXIF     *EXIFConstraint     `json:"exif,omitempty"` // TODO: implement
	Width    *IntConstraint      `json:"width,omitempty"`
	Height   *IntConstraint      `json:"height,omitempty"`
	WHRatio  *FloatConstraint    `json:"widthHeightRation,omitempty"`
	Location *LocationConstraint `json:"location,omitempty"`
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

// A FloatConstraint specifies constraints on an integer.
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
	return c.West <= long && long <= c.East && c.South <= lat && lat <= c.North
}

// A StringConstraint specifies constraints on a string.
// All non-zero must match.
type StringConstraint struct {
	Empty      bool           `json:"empty"` // matches empty string
	Equals     string         `json:"equals"`
	Contains   string         `json:"contains"`
	HasPrefix  string         `json:"hasPrefix"`
	HasSuffix  string         `json:"hasSuffix"`
	ByteLength *IntConstraint `json:"byteLength"` // length in bytes (not chars)

	// TODO: CharLength (assume UTF-8)
	// TODO: CaseInsensitive bool?
}

func (c *StringConstraint) stringMatches(s string) bool {
	if c.Empty && len(s) > 0 {
		return false
	}
	if c.Equals != "" && s != c.Equals {
		return false
	}
	if c.ByteLength != nil && !c.ByteLength.intMatches(int64(len(s))) {
		return false
	}
	for _, pair := range []struct {
		v  string
		fn func(string, string) bool
	}{
		{c.Contains, strings.Contains},
		{c.HasPrefix, strings.HasPrefix},
		{c.HasSuffix, strings.HasSuffix},
	} {
		if pair.v != "" && !pair.fn(s, pair.v) {
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

	// ValueInSet optionally specifies a sub-query which the value
	// (which must be a blobref) must be a part of.
	ValueInSet *Constraint `json:"valueInSet,omitempty"`

	// Relation optionally specifies a constraint based on relations
	// to other permanodes (e.g. camliMember or camliPath sets).
	// You can use it to test the properties of a parent, ancestor,
	// child, or progeny.
	Relation *RelationConstraint `json:"relation,omitempty"`

	// Continue is for internal use.
	Continue *PermanodeContinueConstraint `json:"-"`

	// TODO:
	// NumClaims *IntConstraint  // by owner
	// Owner  blob.Ref // search for permanodes by an owner
}

type PermanodeContinueConstraint struct {
	// LastMod if non-zero is the modtime of the last item
	// that was seen. One of this or LastCreated will be set.
	LastMod time.Time

	// TODO: LastCreated time.Time

	// Last is the last blobref that was shown at the time
	// given in ModLessEqual or CreateLessEqual.
	// This is used as a tie-breaker.
	// If the time is equal, permanodes <= this are not matched.
	// If the time is past this in the scroll position, then this
	// field is ignored.
	Last blob.Ref
}

type RelationConstraint struct {
	// Relation must be one of:
	//
	//   * "child"
	//   * "progeny" (any level down)
	//   * "parent" (immediate parent only)
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

// search is the state of an in-progress search
type search struct {
	h   *Handler
	q   *SearchQuery
	res *SearchResult
	ctx *context.Context

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
		ctx: context.TODO(),
	}
	defer s.ctx.Cancel()

	corpus := h.corpus
	var unlockOnce sync.Once
	if corpus != nil {
		corpus.RLock()
		defer unlockOnce.Do(corpus.RUnlock)
	}

	ch := make(chan camtypes.BlobMeta, buffered)
	errc := make(chan error, 1)

	sendCtx := s.ctx.New()
	defer sendCtx.Cancel()
	go func() {
		errc <- q.sendAllCandidates(sendCtx, s, ch)
	}()

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
			if q.Limit > 0 && len(res.Blobs) == q.Limit && q.candidatesAreSorted(s) {
				sendCtx.Cancel()
				break
			}
		}
	}
	if err := <-errc; err != nil && err != context.ErrCanceled {
		return nil, err
	}
	if !q.candidatesAreSorted(s) {
		// TODO(bradfitz): sort them
		if q.Limit > 0 && len(res.Blobs) > q.Limit {
			res.Blobs = res.Blobs[:q.Limit]
		}
	}
	if corpus != nil {
		q.setResultContinue(corpus, res)
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
	if q.Sort != LastModifiedDesc {
		// Unsupported so far.
		return
	}
	if q.Limit <= 0 || len(res.Blobs) != q.Limit {
		return
	}
	lastpn := res.Blobs[len(res.Blobs)-1].Blob
	t, ok := corpus.PermanodeModtimeLocked(lastpn)
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

// sendAllCandidates sends all possible matches to dst.
// dst must be closed, regardless of error.
func (q *SearchQuery) sendAllCandidates(ctx *context.Context, s *search, dst chan<- camtypes.BlobMeta) error {
	c := q.Constraint
	corpus := s.h.corpus
	if corpus != nil {
		if c.onlyMatchesPermanode() && q.Sort == LastModifiedDesc {
			if candSourceHook != nil {
				candSourceHook("corpus_permanode_desc")
			}
			return corpus.EnumeratePermanodesLastModifiedLocked(ctx, dst)
		}
		if c.AnyCamliType || c.CamliType != "" {
			camType := c.CamliType // empty means all
			if candSourceHook != nil {
				candSourceHook("camli_blob_meta")
			}
			return corpus.EnumerateCamliBlobsLocked(ctx, camType, dst)
		}
	}
	if candSourceHook != nil {
		candSourceHook("all_blob_meta")
	}
	return s.h.index.EnumerateBlobMeta(ctx, dst)
}

func (q *SearchQuery) candidatesAreSorted(s *search) bool {
	corpus := s.h.corpus
	if corpus == nil {
		return false
	}
	if q.Constraint.onlyMatchesPermanode() && q.Sort == LastModifiedDesc {
		return true
	}
	return false
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
		if c.NumValue == nil &&
			c.Value == "" &&
			c.ValueMatches == nil &&
			c.ValueInSet == nil {
			return errors.New("PermanodeConstraint with Attr requires also setting NumValue, Value, ValueMatches, or ValueInSet")
		}
		if nv := c.NumValue; nv != nil {
			if nv.ZeroMin {
				return errors.New("NumValue with ZeroMin makes no sense; matches everything")
			}
			if nv.Min < 0 || nv.Max < 0 {
				return errors.New("NumValue with negative Min or Max makes no sense")
			}
		}
	}
	return nil
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
		vals := corpus.AppendPermanodeAttrValuesLocked(s.ss[:0], br, "camliDefVis", time.Time{}, s.h.owner)
		for _, v := range vals {
			if v == "hide" {
				return false, nil
			}
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

	if cc := c.Continue; cc != nil {
		if corpus == nil {
			// Requires an in-memory index for infinite
			// scroll. At least for now.
			return false, nil
		}
		if !cc.LastMod.IsZero() {
			mt, ok := corpus.PermanodeModtimeLocked(br)
			if !ok || mt.After(cc.LastMod) {
				return false, nil
			}
			// Blobs are sorted by modtime, and then by
			// blobref, and then reversed overall.  From
			// top of page, imagining this scenario, where
			// the user requested a page size Limit of 4:
			//     mod5, sha1-25
			//     mod4, sha1-72
			//     mod3, sha1-cc
			//     mod3, sha1-bb <--- last seen ite, continue = "pn:mod3:sha1-bb"
			//     mod3, sha1-aa  <-- and we want this one next.
			// In the case above, we'll see all of cc, bb, and cc for mod3.
			if mt.Equal(cc.LastMod) && !br.Less(cc.Last) {
				return false, nil
			}
		}
	}
	return true, nil
}

// vals are the current permanode values of c.Attr.
func (c *PermanodeConstraint) permanodeMatchesAttrVals(s *search, vals []string) (bool, error) {
	if c.NumValue != nil && !c.NumValue.intMatches(int64(len(vals))) {
		return false, nil
	}
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
	return true, nil
}

func (c *PermanodeConstraint) permanodeMatchesAttrVal(s *search, val string) (bool, error) {
	if c.Value != "" && c.Value != val {
		return false, nil
	}
	if c.ValueMatches != nil && !c.ValueMatches.stringMatches(val) {
		return false, nil
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
	var width, height int64
	if c.Width != nil || c.Height != nil || c.WHRatio != nil {
		if corpus == nil {
			return false, nil
		}
		imageInfo, err := corpus.GetImageInfoLocked(br)
		if err != nil {
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
