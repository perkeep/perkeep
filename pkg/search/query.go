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
	"errors"
	"fmt"
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
	Constraint *Constraint
	Limit      int      // optional. default is automatic.
	Sort       SortType // optional. default is automatic or unsorted.
}

type SearchResult struct {
	Blobs []*SearchResultBlob
}

type SearchResultBlob struct {
	Blob blob.Ref
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
	Logical *LogicalConstraint

	// Anything, if true, matches all blobs.
	Anything bool

	CamliType     string // camliType of the JSON blob
	BlobRefPrefix string

	// For claims:
	Claim *ClaimConstraint

	BlobSize *BlobSizeConstraint
	Type     *BlobTypeConstraint

	// For permanodes:
	Attribute *AttributeConstraint
}

type ClaimConstraint struct {
	SignedBy     string // identity
	SignedAfter  time.Time
	SignedBefore time.Time
}

type LogicalConstraint struct {
	Op string // "and", "or", "xor", "not"
	A  *Constraint
	B  *Constraint // only valid if Op == "not"
}

type BlobTypeConstraint struct {
	IsJSON  bool
	IsImage bool // chunk header looks like an image. likely just first chunk.
}

type BlobSizeConstraint struct {
	Min int // inclusive
	Max int // inclusive. if zero, ignored.
}

type AttributeConstraint struct {
	// At specifies the time at which to pretend we're resolving attributes.
	// Attribute claims after this point in time are ignored.
	// If zero, the current time is used.
	At time.Time

	// Attr is the attribute to match.
	// e.g. "camliContent", "camliMember", "tag"
	Attr         string
	Value        string      // if non-zero, absolute match
	ValueAny     []string    // Value is any of these strings
	ValueMatches *Constraint // if non-zero, Attr value is blobref in this set of matches
}

// search is the state of an in-progress search
type search struct {
	h   *Handler
	q   *SearchQuery
	res *SearchResult

	mu      sync.Mutex
	matches map[blob.Ref]bool
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

type blobMatcher interface {
	blobMatches(s *search, br blob.Ref, blobMeta BlobMeta) (bool, error)
}

type matchFn func(*search, blob.Ref, BlobMeta) (bool, error)

func alwaysMatch(*search, blob.Ref, BlobMeta) (bool, error) {
	return true, nil
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
			const pfx = "application/json; camliType="
			return strings.TrimPrefix(bm.MIMEType, pfx) == c.CamliType, nil
		})
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
