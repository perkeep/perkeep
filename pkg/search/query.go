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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/types/camtypes"
)

type SortType int

// TODO: add MarshalJSON and UnmarshalJSON to SortType
const (
	UnspecifiedSort SortType = iota
	LastModifiedDesc
	LastModifiedAsc
	CreatedDesc
	CreatedAsc
	maxSortType
)

type SearchQuery struct {
	Constraint *Constraint `json:"constraint"`
	Limit      int         `json:"limit"` // optional. default is automatic. negative means no limit.
	Sort       SortType    `json:"sort"`  // optional. default is automatic or unsorted.

	// If Describe is specified, the matched blobs are also described,
	// as if the Describe.BlobRefs field was populated.
	Describe *DescribeRequest `json:"describe"`
}

func (q *SearchQuery) fromHTTP(req *http.Request) error {
	dec := json.NewDecoder(io.LimitReader(req.Body, 1<<20))
	if err := dec.Decode(q); err != nil {
		return err
	}

	if q.Constraint == nil {
		return errors.New("query must have at least a root Constraint")
	}

	return nil
}

func (q *SearchQuery) plannedQuery() *SearchQuery {
	pq := new(SearchQuery)
	*pq = *q

	if pq.Sort == 0 {
		if pq.Constraint.onlyMatchesPermanode() {
			pq.Sort = LastModifiedDesc
		}
	}
	if pq.Limit == 0 {
		pq.Limit = 200 // arbitrary
	}
	pq.Constraint = optimizePlan(q.Constraint)
	return pq
}

func (q *SearchQuery) checkValid() error {
	if q.Limit < 0 {
		return errors.New("negative limit")
	}
	if q.Sort >= maxSortType || q.Sort < 0 {
		return errors.New("invalid sort type")
	}
	if q.Constraint == nil {
		return errors.New("no constraint")
	}
	if err := q.Constraint.checkValid(); err != nil {
		return err
	}
	return nil
}

type SearchResult struct {
	Blobs    []*SearchResultBlob `json:"blobs"`
	Describe *DescribeResponse   `json:"description"`
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
	Logical *LogicalConstraint `json:"logical"`

	// Anything, if true, matches all blobs.
	Anything bool `json:"anything"`

	CamliType     string `json:"camliType"`    // camliType of the JSON blob
	AnyCamliType  bool   `json:"anyCamliType"` // if true, any camli JSON blob matches
	BlobRefPrefix string `json:"blobRefPrefix"`

	File *FileConstraint
	Dir  *DirConstraint

	Claim    *ClaimConstraint `json:"claim"`
	BlobSize *IntConstraint   `json:"blobSize"`

	Permanode *PermanodeConstraint `json:"permanode"`

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

	FileSize *IntConstraint `json:"fileSize"`
	IsImage  bool
	FileName *StringConstraint
	MIMEType *StringConstraint
	Time     *TimeConstraint
	ModTime  *TimeConstraint
	EXIF     *EXIFConstraint
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
	Min     int64 `json:"min"`
	Max     int64 `json:"max"`
	ZeroMin bool  `json:"zeroMin"` // if true, min is actually zero
	ZeroMax bool  `json:"zeroMax"` // if true, max is actually zero
}

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
	if c.Min > c.Max {
		return errors.New("in InConstraint, min is greater than max")
	}
	return nil
}

func (c *IntConstraint) intMatches(v int64) bool {
	if (c.Min != 0 || c.ZeroMin) && v < c.Min {
		return false
	}
	if (c.Max != 0 || c.ZeroMax) && v > c.Max {
		return false
	}
	return true
}

type EXIFConstraint struct {
	// TODO.  need to put this in the index probably.
	// Maybe: GPS *LocationConstraint
	// ISO, Aperature, Camera Make/Model, etc.
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
	Before time.Time     // <
	After  time.Time     // >=
	InLast time.Duration // >=
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
	At time.Time `json:"at"`

	// ModTime optionally matches on the last modtime of the permanode.
	ModTime *TimeConstraint

	// Attr optionally specifies the attribute to match.
	// e.g. "camliContent", "camliMember", "tag"
	// This is required if any of the items below are used.
	Attr string `json:"attr"`

	// SkipHidden skips hidden or other boring files.
	SkipHidden bool `json:"skipHidden"`

	// NumValue optionally tests the number of values this
	// permanode has for Attr.
	NumValue *IntConstraint `json:"numValue"`

	// ValueAll modifies the matching behavior when an attribute
	// is multi-valued.  By default, when ValueAll is false, only
	// one value of a multi-valued attribute needs to match. If
	// ValueAll is true, all attributes must match.
	ValueAll bool `json:"valueAllMatch"`

	// Value specifies an exact string to match.
	// This is a convenience form for the simple case of exact
	// equality. The same can be accomplished with ValueMatches.
	Value string `json:"value"` // if non-zero, absolute match

	// ValueMatches optionally specifies a StringConstraint to
	// match the value against.
	ValueMatches *StringConstraint `json:"valueMatches"`

	// ValueInSet optionally specifies a sub-query which the value
	// (which must be a blobref) must be a part of.
	ValueInSet *Constraint `json:"valueInSet"`

	// Relation optionally specifies a constraint based on relations
	// to other permanodes (e.g. camliMember or camliPath sets).
	// You can use it to test the properties of a parent, ancestor,
	// child, or progeny.
	Relation *RelationConstraint

	// TODO:
	// NumClaims *IntConstraint  // by owner
	// Owner  blob.Ref // search for permanodes by an owner
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
	return s.h.index.GetBlobMeta(br)
}

// optimizePlan returns an optimized version of c which will hopefully
// execute faster than executing c literally.
func optimizePlan(c *Constraint) *Constraint {
	// TODO: what the comment above says.
	return c
}

func (h *Handler) Query(rawq *SearchQuery) (*SearchResult, error) {
	if err := rawq.checkValid(); err != nil {
		return nil, fmt.Errorf("Invalid SearchQuery: %v", err)
	}
	q := rawq.plannedQuery()
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

func (c *PermanodeConstraint) blobMatches(s *search, br blob.Ref, bm camtypes.BlobMeta) (bool, error) {
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
	fi, err := s.h.index.GetFileInfo(br)
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
	// TOOD: EXIF timeconstraint
	return true, nil
}

func (c *TimeConstraint) timeMatches(t time.Time) bool {
	if t.IsZero() {
		return false
	}
	if !c.Before.IsZero() {
		if !t.Before(c.Before) {
			return false
		}
	}
	after := c.After
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
