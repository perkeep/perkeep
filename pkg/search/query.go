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
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/syncutil"
)

type SortType int

// TODO: extend/merge/delete this type? probably dups in this package.
type BlobMeta struct {
	Ref      blob.Ref
	Size     int
	MIMEType string
}

const (
	UnspecifiedSort SortType = iota
	LastModifiedDesc
	LastModifiedAsc
	CreatedDesc
	CreatedAsc
)

type SearchQuery struct {
	Constraint *Constraint `json:"constraint"`
	Limit      int         `json:"limit"` // optional. default is automatic.
	Sort       SortType    `json:"sort"`  // optional. default is automatic or unsorted.
}

func (q *SearchQuery) fromHTTP(req *http.Request) error {
	dec := json.NewDecoder(req.Body)
	if err := dec.Decode(q); err != nil {
		return err
	}

	if q.Constraint == nil {
		return errors.New("query must have at least a root Constraint")
	}

	return nil
}

type SearchResult struct {
	Blobs []*SearchResultBlob `json:"blobs"`
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

	// For claims:
	Claim *ClaimConstraint `json:"claim"`

	BlobSize *BlobSizeConstraint `json:"blobSize"`

	// For permanodes:
	Attribute *AttributeConstraint `json:"attribute"`
}

type FileConstraint struct {
	// (All non-zero fields must match)

	MinSize  int64 // inclusive
	MaxSize  int64 // inclusive. if zero, ignored.
	IsImage  bool
	FileName *StringConstraint
	MIMEType *StringConstraint
	Time     *TimeConstraint
	ModTime  *TimeConstraint
	EXIF     *EXIFConstraint
}

type EXIFConstraint struct {
	// TODO.  need to put this in the index probably.
	// Maybe: GPS *LocationConstraint
	// ISO, Aperature, Camera Make/Model, etc.
}

type StringConstraint struct {
	// All non-zero must match.

	// TODO: CaseInsensitive bool?
	Empty     bool // matches empty string
	Equals    string
	Contains  string
	HasPrefix string
	HasSuffix string
}

func (c *StringConstraint) stringMatches(s string) bool {
	if c.Empty && len(s) > 0 {
		return false
	}
	if c.Equals != "" && s != c.Equals {
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

type LogicalConstraint struct {
	Op string      `json:"op"` // "and", "or", "xor", "not"
	A  *Constraint `json:"a"`
	B  *Constraint `json:"b"` // only valid if Op == "not"
}

type BlobSizeConstraint struct {
	Min int `json:"min"` // inclusive
	Max int `json:"max"` // inclusive. if zero, ignored.
}

type AttributeConstraint struct {
	// At specifies the time at which to pretend we're resolving attributes.
	// Attribute claims after this point in time are ignored.
	// If zero, the current time is used.
	At time.Time `json:"at"`

	// Attr is the attribute to match.
	// e.g. "camliContent", "camliMember", "tag"
	// TODO: field to control whether first vs. all permanode values are considered?
	Attr         string      `json:"attr"`
	Value        string      `json:"value"`        // if non-zero, absolute match
	ValueAny     []string    `json:"valueAny"`     // Value is any of these strings
	ValueMatches *Constraint `json:"valueMatches"` // if non-zero, Attr value is blobref in this set of matches
	ValueSet     bool        `json:"valueSet"`     // value is set to something non-blank
}

// search is the state of an in-progress search
type search struct {
	h   *Handler
	q   *SearchQuery
	res *SearchResult

	mu      sync.Mutex
	matches map[blob.Ref]bool
}

func (s *search) blobMeta(br blob.Ref) (BlobMeta, error) {
	mime, size, err := s.h.index.GetBlobMIMEType(br)
	return BlobMeta{Ref: br, Size: int(size), MIMEType: mime}, err
}

// optimizePlan returns an optimized version of c which will hopefully
// execute faster than executing c literally.
func optimizePlan(c *Constraint) *Constraint {
	// TODO: what the comment above says.
	return c
}

func (h *Handler) Query(q *SearchQuery) (*SearchResult, error) {
	res := new(SearchResult)
	s := &search{
		h:       h,
		q:       q,
		res:     res,
		matches: make(map[blob.Ref]bool),
	}
	ch := make(chan BlobMeta, buffered)
	errc := make(chan error, 1)
	go func() {
		errc <- h.index.EnumerateBlobMeta(ch)
	}()
	optConstraint := optimizePlan(q.Constraint)

	for meta := range ch {
		match, err := optConstraint.blobMatches(s, meta.Ref, meta)
		if err != nil {
			// drain ch
			go func() {
				for _ = range ch {
				}
			}()
			return nil, err
		}
		if match {
			res.Blobs = append(res.Blobs, &SearchResultBlob{
				Blob: meta.Ref,
			})
		}
	}
	if err := <-errc; err != nil {
		return nil, err
	}
	return s.res, nil
}

const camliTypeMIME = "application/json; camliType="

type blobMatcher interface {
	blobMatches(s *search, br blob.Ref, blobMeta BlobMeta) (bool, error)
}

type matchFn func(*search, blob.Ref, BlobMeta) (bool, error)

func alwaysMatch(*search, blob.Ref, BlobMeta) (bool, error) {
	return true, nil
}

func anyCamliType(s *search, br blob.Ref, bm BlobMeta) (bool, error) {
	return strings.HasPrefix(bm.MIMEType, camliTypeMIME), nil
}

func (c *Constraint) blobMatches(s *search, br blob.Ref, blobMeta BlobMeta) (bool, error) {
	var conds []matchFn
	addCond := func(fn matchFn) {
		conds = append(conds, fn)
	}
	if c.Logical != nil {
		addCond(c.Logical.blobMatches)
	}
	if c.Anything {
		addCond(alwaysMatch)
	}
	if c.CamliType != "" {
		addCond(func(s *search, br blob.Ref, bm BlobMeta) (bool, error) {
			return strings.TrimPrefix(bm.MIMEType, camliTypeMIME) == c.CamliType, nil
		})
	}
	if c.AnyCamliType {
		addCond(anyCamliType)
	}
	if c.Attribute != nil {
		addCond(c.Attribute.blobMatches)
	}
	// TODO: ClaimConstraint
	if c.File != nil {
		addCond(c.File.blobMatches)
	}
	if bs := c.BlobSize; bs != nil {
		addCond(func(s *search, br blob.Ref, bm BlobMeta) (bool, error) {
			if bm.Size < bs.Min {
				return false, nil
			}
			if bs.Max > 0 && bm.Size > bs.Max {
				return false, nil
			}
			return true, nil
		})

	}
	if pfx := c.BlobRefPrefix; pfx != "" {
		addCond(func(*search, blob.Ref, BlobMeta) (bool, error) {
			return strings.HasPrefix(br.String(), pfx), nil
		})
	}
	switch len(conds) {
	case 0:
		return false, nil
	case 1:
		return conds[0](s, br, blobMeta)
	default:
		panic("TODO")
	}
}

func (c *LogicalConstraint) blobMatches(s *search, br blob.Ref, bm BlobMeta) (bool, error) {
	switch c.Op {
	case "and", "xor":
		if c.A == nil || c.B == nil {
			return false, errors.New("In LogicalConstraint, need both A and B set")
		}
		var g syncutil.Group
		var av, bv bool
		g.Go(func() (err error) {
			av, err = c.A.blobMatches(s, br, bm)
			return
		})
		g.Go(func() (err error) {
			bv, err = c.B.blobMatches(s, br, bm)
			return
		})
		if err := g.Err(); err != nil {
			return false, err
		}
		switch c.Op {
		case "and":
			return av && bv, nil
		case "xor":
			return av != bv, nil
		default:
			panic("unreachable")
		}
	case "or":
		if c.A == nil || c.B == nil {
			return false, errors.New("In LogicalConstraint, need both A and B set")
		}
		av, err := c.A.blobMatches(s, br, bm)
		if err != nil {
			return false, err
		}
		if av {
			// Short-circuit.
			return true, nil
		}
		return c.B.blobMatches(s, br, bm)
	case "not":
		if c.A == nil {
			return false, errors.New("In LogicalConstraint, need to set A")
		}
		if c.B != nil {
			return false, errors.New("In LogicalConstraint, can't specify B with Op \"not\"")
		}
		v, err := c.A.blobMatches(s, br, bm)
		return !v, err
	default:
		return false, fmt.Errorf("In LogicalConstraint, unknown operation %q", c.Op)
	}
}

func (c *AttributeConstraint) blobMatches(s *search, br blob.Ref, bm BlobMeta) (bool, error) {
	if bm.MIMEType != "application/json; camliType=permanode" {
		return false, nil
	}
	dr, err := s.h.Describe(&DescribeRequest{
		BlobRef: br,
	})
	if err != nil {
		return false, err
	}
	db := dr.Meta[br.String()]
	if db == nil || db.Permanode == nil {
		return false, nil
	}
	attrs := db.Permanode.Attr // url.Values: a map[string][]string
	if c.Value != "" {
		got := attrs.Get(c.Attr)
		return got == c.Value, nil
	}
	if len(c.ValueAny) > 0 {
		for _, attr := range attrs[c.Attr] {
			for _, want := range c.ValueAny {
				if want == attr {
					return true, nil
				}
			}
		}
		return false, nil
	}
	if c.ValueSet {
		for _, attr := range attrs[c.Attr] {
			if attr != "" {
				return true, nil
			}
		}
		return false, nil
	}
	if subc := c.ValueMatches; subc != nil {
		for _, attr := range attrs[c.Attr] {
			if attrBr, ok := blob.Parse(attr); ok {
				meta, err := s.blobMeta(attrBr)
				if err == os.ErrNotExist {
					continue
				}
				if err != nil {
					return false, err
				}
				matches, err := subc.blobMatches(s, attrBr, meta)
				if err != nil {
					return false, err
				}
				if matches {
					return true, nil
				}
			}
		}
		return false, nil
	}

	log.Printf("br=%v meta=%+v: %#v", br, bm, dr)
	panic("TODO: not implemented")
	return false, nil
}

func (c *FileConstraint) blobMatches(s *search, br blob.Ref, bm BlobMeta) (bool, error) {
	if bm.MIMEType != "application/json; camliType=file" {
		return false, nil
	}
	fi, err := s.h.index.GetFileInfo(br)
	if err == os.ErrNotExist {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if fi.Size < c.MinSize {
		return false, nil
	}
	if c.MaxSize != 0 && fi.Size > c.MaxSize {
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
	// TOOD: Time timeconstraint
	// TOOD: ModTime timeconstraint
	// TOOD: EXIF timeconstraint
	return true, nil
}
