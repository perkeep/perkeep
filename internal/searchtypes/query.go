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

package searchtypes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go4.org/types"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/types/camtypes"
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
	// MapSort requests that any limited search results are optimized
	// for rendering on a map. If there are fewer matches than the
	// requested limit, no results are pruned. When limiting results,
	// MapSort prefers results spread around the map before clustering
	// items too tightly.
	MapSort
	MaxSortType
)

var SortName = map[SortType][]byte{
	Unsorted:         []byte(`"unsorted"`),
	LastModifiedDesc: []byte(`"-mod"`),
	LastModifiedAsc:  []byte(`"mod"`),
	CreatedDesc:      []byte(`"-created"`),
	CreatedAsc:       []byte(`"created"`),
	BlobRefAsc:       []byte(`"blobref"`),
	MapSort:          []byte(`"map"`),
}

type SearchQuery struct {
	// Exactly one of Expression or Constraint must be set.
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

type SearchResult struct {
	Blobs    []*SearchResultBlob `json:"blobs"`
	Describe *DescribeResponse   `json:"description"`

	// LocationArea is non-nil if the search result mentioned any location terms. It
	// is the bounds of the locations of the matched permanodes, for the permanodes
	// with locations.
	LocationArea *camtypes.LocationBounds

	// Continue optionally specifies the continuation token to to
	// continue fetching results in this result set, if interrupted
	// by a Limit.
	Continue string `json:"continue,omitempty"`
}

type SearchResultBlob struct {
	Blob blob.Ref `json:"blob"`
	// ... file info, permanode info, blob info ... ?
}

type Constraint struct {
	// If Logical is non-nil, all other fields are ignored.
	Logical *LogicalConstraint `json:"logical,omitempty"`

	// Anything, if true, matches all blobs.
	Anything bool `json:"anything,omitempty"`

	CamliType     schema.CamliType `json:"camliType,omitempty"`    // camliType of the JSON blob
	AnyCamliType  bool             `json:"anyCamliType,omitempty"` // if true, any camli JSON blob matches
	BlobRefPrefix string           `json:"blobRefPrefix,omitempty"`

	File *FileConstraint `json:"file,omitempty"`
	Dir  *DirConstraint  `json:"dir,omitempty"`

	Claim    *ClaimConstraint `json:"claim,omitempty"`
	BlobSize *IntConstraint   `json:"blobSize,omitempty"`

	Permanode *PermanodeConstraint `json:"permanode,omitempty"`

	matcherOnce sync.Once
	matcherFn   any
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

	// ParentDir, if non-nil, constrains the file match based on properties
	// of its parent directory.
	ParentDir *DirConstraint `json:"parentDir,omitempty"`

	// For images:
	IsImage  bool                `json:"isImage,omitempty"`
	EXIF     *EXIFConstraint     `json:"exif,omitempty"` // TODO: implement
	Width    *IntConstraint      `json:"width,omitempty"`
	Height   *IntConstraint      `json:"height,omitempty"`
	WHRatio  *FloatConstraint    `json:"widthHeightRation,omitempty"`
	Location *LocationConstraint `json:"location,omitempty"`

	// IsVideo matches files with a video MIME type.
	IsVideo bool `json:"isVideo,omitempty"`

	// Camera matches either the EXIF make or model. Files without camera
	// metadata do not match. Requires an in-memory corpus.
	Camera *StringConstraint `json:"camera,omitempty"`

	// MediaTag is for ID3 (and similar) embedded metadata in files.
	MediaTag *MediaTagConstraint `json:"mediaTag,omitempty"`

	// LivePhoto, if non-empty, matches files that are half of an Apple
	// Live Photo whose other half is indexed: "still" matches the still
	// image, "video" the video, and "any" either. Requires an in-memory
	// corpus.
	LivePhoto string `json:"livePhoto,omitempty"`
}

type MediaTagConstraint struct {
	// Tag is the tag to match.
	// For ID3, this includes: title, artist, album, genre, musicbrainzalbumid, year, track, disc, mediaref, durationms.
	// For Apple photos and QuickTime videos, this includes: contentidentifier (shared by a Live Photo's
	// still image and video), burstuuid and photoidentifier (photos), and durationms, make, model,
	// software, creationdate, livephotoauto, and livephotovitalityscore (videos).
	Tag string `json:"tag"`

	String *StringConstraint `json:"string,omitempty"`
	Int    *IntConstraint    `json:"int,omitempty"`
}

type DirConstraint struct {
	// (All non-zero fields must match)

	FileName      *StringConstraint `json:"fileName,omitempty"`
	BlobRefPrefix string            `json:"blobRefPrefix,omitempty"`

	// ParentDir, if non-nil, constrains the directory match based on properties
	// of its parent directory.
	ParentDir *DirConstraint `json:"parentDir,omitempty"`

	// TODO: implement.
	// FileCount *IntConstraint
	// FileSize *IntConstraint

	// TopFileCount, if non-nil, constrains the directory match with the directory's
	// number of children (non-recursively).
	TopFileCount *IntConstraint `json:"topFileCount,omitempty"`

	// RecursiveContains, if non-nil, is like Contains, but applied to all
	// the descendants of the directory. It is mutually exclusive with Contains.
	RecursiveContains *Constraint `json:"recursiveContains,omitempty"`

	// Contains, if non-nil, constrains the directory match to just those
	// directories containing a file matched by Contains. Contains should have a
	// BlobPrefix, or a *FileConstraint, or a *DirConstraint, or a *LogicalConstraint
	// combination of the aforementioned. It is only applied to the children of the
	// directory, in a non-recursive manner. It is mutually exclusive with RecursiveContains.
	Contains *Constraint `json:"contains,omitempty"`
}

type IntConstraint struct {
	// Min and Max are both optional and inclusive bounds.
	// Zero means don't check.
	Min     int64 `json:"min,omitempty"`
	Max     int64 `json:"max,omitempty"`
	ZeroMin bool  `json:"zeroMin,omitempty"` // if true, min is actually zero
	ZeroMax bool  `json:"zeroMax,omitempty"` // if true, max is actually zero

	Equals *int64 `json:"equals,omitempty"` // if non-nil, value must equal this
}

type FloatConstraint struct {
	// Min and Max are both optional and inclusive bounds.
	// Zero means don't check.
	Min     float64 `json:"min,omitempty"`
	Max     float64 `json:"max,omitempty"`
	ZeroMin bool    `json:"zeroMin,omitempty"` // if true, min is actually zero
	ZeroMax bool    `json:"zeroMax,omitempty"` // if true, max is actually zero
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

type StringConstraint struct {
	Empty           bool           `json:"empty,omitempty"` // matches empty string
	Equals          string         `json:"equals,omitempty"`
	Contains        string         `json:"contains,omitempty"`
	HasPrefix       string         `json:"hasPrefix,omitempty"`
	HasSuffix       string         `json:"hasSuffix,omitempty"`
	ByteLength      *IntConstraint `json:"byteLength,omitempty"` // length in bytes (not chars)
	CaseInsensitive bool           `json:"caseInsensitive,omitempty"`
	// TODO: CharLength (assume UTF-8)

	// Regexp, if non-empty, specifies a regular expression that the string must match.
	// The syntax is as described by the regexp package.
	Regexp string `json:"regexp,omitempty"`

	regexp atomic.Pointer[regexp.Regexp] // lazily compiled version of Regexp
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

type LogicalConstraint struct {
	Op string      `json:"op"` // "and", "or", "xor", "not"
	A  *Constraint `json:"a"`
	B  *Constraint `json:"b"` // only valid if Op != "not"
}

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

func (t SortType) MarshalJSON() ([]byte, error) {
	v, ok := SortName[t]
	if !ok {
		panic("unnamed SortType " + strconv.Itoa(int(t)))
	}
	return v, nil
}

func (t *SortType) UnmarshalJSON(v []byte) error {
	for n, nv := range SortName {
		if bytes.Equal(v, nv) {
			*t = n
			return nil
		}
	}
	return fmt.Errorf("Bogus search sort type %q", v)
}

func (q *SearchQuery) URLSuffix() string { return "camli/search/query" }

func (q *SearchQuery) FromHTTP(req *http.Request) error {
	dec := json.NewDecoder(io.LimitReader(req.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(q)
}

func (r *SearchResultBlob) String() string {
	return fmt.Sprintf("[blob: %s]", r.Blob)
}

// CachedMatcher keeps the server's opaque compiled matcher with its constraint.
// Keeping the cache here avoids exposing server implementation types in the wire types.
func CachedMatcher(c *Constraint, init func() any) any {
	c.matcherOnce.Do(func() { c.matcherFn = init() })
	return c.matcherFn
}

// StringRegexp returns the lazily compiled regular expression for c.
func StringRegexp(c *StringConstraint) (*regexp.Regexp, error) {
	re := c.regexp.Load()
	if re == nil {
		var err error
		re, err = regexp.Compile(c.Regexp)
		if err != nil {
			return nil, err
		}
		c.regexp.Store(re)
	}
	return re, nil
}
