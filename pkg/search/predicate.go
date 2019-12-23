/*
Copyright 2014 The Perkeep Authors

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

// These are the search-atom definitions (see expr.go).

package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go4.org/types"
	"perkeep.org/internal/geocode"
	"perkeep.org/pkg/schema/nodeattr"
)

const base = "0000-01-01T00:00:00Z"

var (
	// used for width/height ranges. 10 is max length of 32-bit
	// int (strconv.Atoi on 32-bit platforms), even though a max
	// JPEG dimension is only 16-bit.
	whRangeExpr = regexp.MustCompile(`^(\d{0,10})-(\d{0,10})$`)
	whValueExpr = regexp.MustCompile(`^(\d{1,10})$`)
)

// Atoms holds the parsed words of an atom without the colons.
// Eg. tag:holiday becomes atom{"tag", []string{"holiday"}}
// Note that the form of camlisearch atoms implies that len(args) > 0
type atom struct {
	predicate string
	args      []string
}

func (a atom) String() string {
	s := bytes.NewBufferString(a.predicate)
	for _, a := range a.args {
		s.WriteRune(':')
		s.WriteString(a)
	}
	return s.String()
}

// Keyword determines by its matcher when a predicate is used.
type keyword interface {
	// Name is the part before the first colon, or the whole atom.
	Name() string
	// Description provides user documentation for this keyword.  Should
	// return documentation for max/min values, usage help, or examples.
	Description() string
	// Match gets called with the predicate and arguments that were parsed.
	// It should return true if it wishes to handle this search atom.
	// An error if the number of arguments mismatches.
	Match(a atom) (bool, error)
	// Predicates will be called with the args array from an atom instance.
	// Note that len(args) > 0 (see atom-struct comment above).
	// It should return a pointer to a Constraint object, expressing the meaning of
	// its keyword.
	Predicate(ctx context.Context, args []string) (*Constraint, error)
}

var keywords []keyword

// RegisterKeyword registers search atom types.
// TODO (sls) Export for applications? (together with keyword and atom)
func registerKeyword(k keyword) {
	keywords = append(keywords, k)
}

// SearchHelp returns JSON of an array of predicate names and descriptions.
func SearchHelp() string {
	type help struct{ Name, Description string }
	h := []help{}
	for _, p := range keywords {
		h = append(h, help{p.Name(), p.Description()})
	}
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return "Error marshalling"
	}
	return string(b)
}

func init() {
	// Core predicates
	registerKeyword(newAfter())
	registerKeyword(newBefore())
	registerKeyword(newAttribute())
	registerKeyword(newChildrenOf())
	registerKeyword(newParentOf())
	registerKeyword(newFormat())
	registerKeyword(newTag())
	registerKeyword(newTitle())
	registerKeyword(newRef())

	// Image predicates
	registerKeyword(newIsImage())
	registerKeyword(newHeight())
	registerKeyword(newIsLandscape())
	registerKeyword(newIsPano())
	registerKeyword(newIsPortait())
	registerKeyword(newWidth())

	// MediaTags predicates
	registerKeyword(newMedia())
	//registerKeyword(newArtist())
	//registerKeyword(newAlbum())

	// File predicates
	registerKeyword(newFilename())

	// Custom predicates
	registerKeyword(newIsPost())
	registerKeyword(newIsLike())
	registerKeyword(newIsCheckin())

	// Location predicates
	registerKeyword(newHasLocation())
	registerKeyword(newNamedLocation())
	registerKeyword(newLocation())

	// People predicates
	registerKeyword(newWith())
}

// Helper implementation for mixing into keyword implementations
// that match the full keyword, i.e. 'is:pano'
type matchEqual string

func (me matchEqual) Name() string {
	return string(me)
}

func (me matchEqual) Match(a atom) (bool, error) {
	return string(me) == a.String(), nil
}

// Helper implementation for mixing into keyword implementations
// that match only the beginning of the keyword, and get their parameters from
// the rest, i.e. 'width:' for searches like 'width:100-200'.
type matchPrefix struct {
	prefix string
	count  int
}

func newMatchPrefix(p string) matchPrefix {
	return matchPrefix{prefix: p, count: 1}
}

func (mp matchPrefix) Name() string {
	return mp.prefix
}
func (mp matchPrefix) Match(a atom) (bool, error) {
	if mp.prefix == a.predicate {
		if len(a.args) != mp.count {
			return true, fmt.Errorf("Wrong number of arguments for %q, given %d, expected %d", mp.prefix, len(a.args), mp.count)
		}
		return true, nil
	}
	return false, nil
}

// Core predicates

type after struct {
	matchPrefix
}

func newAfter() keyword {
	return after{newMatchPrefix("after")}
}

func (a after) Description() string {
	return "date format is RFC3339, but can be shortened as required.\n" +
		"i.e. 2011-01-01 is Jan 1 of year 2011 and \"2011\" means the same."
}

func (a after) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	t, err := parseTimePrefix(args[0])
	if err != nil {
		return nil, err
	}
	tc := &TimeConstraint{}
	tc.After = types.Time3339(t)
	c := &Constraint{
		Permanode: &PermanodeConstraint{
			Time: tc,
		},
	}
	return c, nil
}

type before struct {
	matchPrefix
}

func newBefore() keyword {
	return before{newMatchPrefix("before")}
}

func (b before) Description() string {
	return "date format is RFC3339, but can be shortened as required.\n" +
		"i.e. 2011-01-01 is Jan 1 of year 2011 and \"2011\" means the same."
}

func (b before) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	t, err := parseTimePrefix(args[0])
	if err != nil {
		return nil, err
	}
	tc := &TimeConstraint{}
	tc.Before = types.Time3339(t)
	c := &Constraint{
		Permanode: &PermanodeConstraint{
			Time: tc,
		},
	}
	return c, nil
}

type attribute struct {
	matchPrefix
}

func newAttribute() keyword {
	return attribute{matchPrefix{"attr", 2}}
}

func (a attribute) Description() string {
	return "match on attribute. Use attr:foo:bar to match nodes having their foo\n" +
		"attribute set to bar or attr:foo:~bar to do a substring\n" +
		"case-insensitive search for 'bar' in attribute foo"
}

func (a attribute) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	c := permWithAttr(args[0], args[1])
	if strings.HasPrefix(args[1], "~") {
		// Substring. Hack. Figure out better way to do this.
		c.Permanode.Value = ""
		c.Permanode.ValueMatches = &StringConstraint{
			Contains:        args[1][1:],
			CaseInsensitive: true,
		}
	}
	return c, nil
}

type childrenOf struct {
	matchPrefix
}

func newChildrenOf() keyword {
	return childrenOf{newMatchPrefix("childrenof")}
}

func (k childrenOf) Description() string {
	return "Find child permanodes of a parent permanode (or prefix of a parent\n" +
		"permanode): childrenof:sha1-527cf12 Only matches permanodes currently."
}

func (k childrenOf) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	c := &Constraint{
		Permanode: &PermanodeConstraint{
			Relation: &RelationConstraint{
				Relation: "parent",
				Any: &Constraint{
					BlobRefPrefix: args[0],
				},
			},
		},
	}
	return c, nil
}

type parentOf struct {
	matchPrefix
}

func newParentOf() keyword {
	return parentOf{newMatchPrefix("parentof")}
}

func (k parentOf) Description() string {
	return "Find parent permanodes of a child permanode (or prefix of a child\n" +
		"permanode): parentof:sha1-527cf12 Only matches permanodes currently."
}

func (k parentOf) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	c := &Constraint{
		Permanode: &PermanodeConstraint{
			Relation: &RelationConstraint{
				Relation: "child",
				Any: &Constraint{
					BlobRefPrefix: args[0],
				},
			},
		},
	}
	return c, nil
}

type format struct {
	matchPrefix
}

func newFormat() keyword {
	return format{newMatchPrefix("format")}
}

func (f format) Description() string {
	return "file's format (or MIME-type) such as jpg, pdf, tiff."
}

func (f format) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	mimeType, err := mimeFromFormat(args[0])
	if err != nil {
		return nil, err
	}
	c := permOfFile(&FileConstraint{
		MIMEType: &StringConstraint{
			Equals: mimeType,
		},
	})
	return c, nil
}

type tag struct {
	matchPrefix
}

func newTag() keyword {
	return tag{newMatchPrefix("tag")}
}

func (t tag) Description() string {
	return "match on a tag"
}

func (t tag) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	return permWithAttr("tag", args[0]), nil
}

type with struct {
	matchPrefix
}

func newWith() keyword {
	return with{newMatchPrefix("with")}
}

func (w with) Description() string {
	return "match people containing substring in their first or last name"
}

func (w with) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	// TODO(katepek): write a query optimizer or a separate matcher
	c := &Constraint{
		// TODO(katepek): Does this work with repeated values for "with"?
		// Select all permanodes where attribute "with" points to permanodes with the foursquare person type
		// and with first or last name partially matching the query string
		Permanode: &PermanodeConstraint{
			Attr: "with",
			ValueInSet: andConst(
				&Constraint{
					Permanode: &PermanodeConstraint{
						Attr:  nodeattr.Type,
						Value: "foursquare.com:person",
					},
				},
				orConst(
					permWithAttrSubstr(nodeattr.GivenName, &StringConstraint{
						Contains:        args[0],
						CaseInsensitive: true,
					}),
					permWithAttrSubstr(nodeattr.FamilyName, &StringConstraint{
						Contains:        args[0],
						CaseInsensitive: true,
					}),
				),
			),
		},
	}
	return c, nil
}

type title struct {
	matchPrefix
}

func newTitle() keyword {
	return title{newMatchPrefix("title")}
}

func (t title) Description() string {
	return "match nodes containing substring in their title"
}

func (t title) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	c := &Constraint{
		Permanode: &PermanodeConstraint{
			Attr:       nodeattr.Title,
			SkipHidden: true,
			ValueMatches: &StringConstraint{
				Contains:        args[0],
				CaseInsensitive: true,
			},
		},
	}
	return c, nil
}

type ref struct {
	matchPrefix
}

func newRef() keyword {
	return ref{newMatchPrefix("ref")}
}

func (r ref) Description() string {
	return "match nodes whose blobRef starts with the given substring"
}

func (r ref) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	return &Constraint{
		BlobRefPrefix: args[0],
	}, nil
}

// Image predicates

type isImage struct {
	matchEqual
}

func newIsImage() keyword {
	return isImage{"is:image"}
}

func (k isImage) Description() string {
	return "object is an image"
}

func (k isImage) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	c := &Constraint{
		Permanode: &PermanodeConstraint{
			Attr: nodeattr.CamliContent,
			ValueInSet: &Constraint{
				File: &FileConstraint{
					IsImage: true,
				},
			},
		},
	}
	return c, nil
}

type isLandscape struct {
	matchEqual
}

func newIsLandscape() keyword {
	return isLandscape{"is:landscape"}
}

func (k isLandscape) Description() string {
	return "the image has a landscape aspect"
}

func (k isLandscape) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	return whRatio(&FloatConstraint{Min: 1.0}), nil
}

type isPano struct {
	matchEqual
}

func newIsPano() keyword {
	return isPano{"is:pano"}
}

func (k isPano) Description() string {
	return "the image's aspect ratio is over 2 - panorama picture."
}

func (k isPano) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	return whRatio(&FloatConstraint{Min: 2.0}), nil
}

type isPortait struct {
	matchEqual
}

func newIsPortait() keyword {
	return isPortait{"is:portrait"}
}

func (k isPortait) Description() string {
	return "the image has a portrait aspect"
}

func (k isPortait) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	return whRatio(&FloatConstraint{Max: 1.0}), nil
}

type width struct {
	matchPrefix
}

func newWidth() keyword {
	return width{newMatchPrefix("width")}
}

func (w width) Description() string {
	return "use width:min-max to match images having a width of at least min\n" +
		"and at most max. Use width:min- to specify only an underbound and\n" +
		"width:-max to specify only an upperbound.\n" +
		"Exact matches should use width:640 "
}

func (w width) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	mins, maxs, err := parseWHExpression(args[0])
	if err != nil {
		return nil, err
	}
	c := permOfFile(&FileConstraint{
		IsImage: true,
		Width:   whIntConstraint(mins, maxs),
	})
	return c, nil
}

type height struct {
	matchPrefix
}

func newHeight() keyword {
	return height{newMatchPrefix("height")}
}

func (h height) Description() string {
	return "use height:min-max to match images having a height of at least min\n" +
		"and at most max. Use height:min- to specify only an underbound and\n" +
		"height:-max to specify only an upperbound.\n" +
		"Exact matches should use height:480"
}

func (h height) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	mins, maxs, err := parseWHExpression(args[0])
	if err != nil {
		return nil, err
	}
	c := permOfFile(&FileConstraint{
		IsImage: true,
		Height:  whIntConstraint(mins, maxs),
	})
	return c, nil
}

// MediaTags Predicates

type media struct {
	matchPrefix
}

func newMedia() keyword {
	return media{newMatchPrefix("media")}
}

func (t media) Description() string {
	return "match nodes containing substring in their media tags"
}

func (t media) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	c := &Constraint{
		Permanode: &PermanodeConstraint{
			Attr: nodeattr.CamliContent,
			ValueInSet: &Constraint{
				File: &FileConstraint{
					MediaTag: &MediaTagConstraint{
						String: &StringConstraint{
							Contains:				 args[0],
							CaseInsensitive: true,
						},
					},
				},
			},
		},
	}
	return c, nil
}

// Location predicates

// namedLocation matches e.g. `loc:Paris` or `loc:"New York, New York"` queries.
type namedLocation struct {
	matchPrefix
}

func newNamedLocation() keyword {
	return namedLocation{newMatchPrefix("loc")}
}

func (l namedLocation) Description() string {
	return "matches images and permanodes having a location near\n" +
		"the specified location.  Locations are resolved using\n" +
		"maps.googleapis.com. For example: loc:\"new york, new york\" "
}

func locationPredicate(ctx context.Context, rects []geocode.Rect) (*Constraint, error) {
	var c *Constraint
	for i, rect := range rects {
		loc := &LocationConstraint{
			West:  rect.SouthWest.Long,
			East:  rect.NorthEast.Long,
			North: rect.NorthEast.Lat,
			South: rect.SouthWest.Lat,
		}
		permLoc := &Constraint{
			Permanode: &PermanodeConstraint{
				Location: loc,
			},
		}
		if i == 0 {
			c = permLoc
		} else {
			c = orConst(c, permLoc)
		}
	}
	return c, nil
}

func (l namedLocation) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	where := args[0]
	rects, err := geocode.Lookup(ctx, where)
	if err != nil {
		return nil, err
	}
	if len(rects) == 0 {
		return nil, fmt.Errorf("No location found for %q", where)
	}
	return locationPredicate(ctx, rects)
}

// location matches "locrect:N,W,S,E" queries.
type location struct {
	matchPrefix
}

func newLocation() keyword {
	return location{newMatchPrefix("locrect")}
}

func (l location) Description() string {
	return "matches images and permanodes having a location within\n" +
		"the specified location area. The area is defined by its\n " +
		"North-West corner, followed and comma-separated by its\n " +
		"South-East corner. Each corner is defined by its latitude,\n " +
		"followed and comma-separated by its longitude."
}

func (l location) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	where := args[0]
	coords := strings.Split(where, ",")
	if len(coords) != 4 {
		return nil, fmt.Errorf("got %d coordinates for location area, expected 4", len(coords))
	}
	asFloat := make([]float64, 4)
	for k, v := range coords {
		coo, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("could not convert location area coordinate as a float: %v", err)
		}
		asFloat[k] = coo
	}
	rects := []geocode.Rect{
		{
			NorthEast: geocode.LatLong{
				Lat:  asFloat[0],
				Long: asFloat[3],
			},
			SouthWest: geocode.LatLong{
				Lat:  asFloat[2],
				Long: asFloat[1],
			},
		},
	}
	return locationPredicate(ctx, rects)
}

type hasLocation struct {
	matchEqual
}

func newHasLocation() keyword {
	return hasLocation{"has:location"}
}

func (h hasLocation) Description() string {
	return "matches images and permanodes that have a location (GPSLatitude\n" +
		"and GPSLongitude can be retrieved from the image's EXIF tags)."
}

func (h hasLocation) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	return &Constraint{
		Permanode: &PermanodeConstraint{
			Location: &LocationConstraint{
				Any: true,
			},
		},
	}, nil
}

// NamedSearch lets you use the search aliases you defined with SetNamed from the search handler.
type namedSearch struct {
	matchPrefix
	sh *Handler
}

func newNamedSearch(sh *Handler) keyword {
	return namedSearch{newMatchPrefix("named"), sh}
}

func (n namedSearch) Description() string {
	return "Uses substitution of a predefined search. Set with $searchRoot/camli/search/setnamed?name=foo&substitute=attr:bar:baz" +
		"\nSee what the substitute is with $searchRoot/camli/search/getnamed?named=foo"
}

func (n namedSearch) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	return n.namedConstraint(args[0])
}

func (n namedSearch) namedConstraint(name string) (*Constraint, error) {
	subst, err := n.sh.getNamed(context.TODO(), name)
	if err != nil {
		return nil, err
	}
	return evalSearchInput(subst)
}

// Helpers

func permWithAttr(attr, val string) *Constraint {
	c := &Constraint{
		Permanode: &PermanodeConstraint{
			Attr:       attr,
			SkipHidden: true,
		},
	}
	if val == "" {
		c.Permanode.ValueMatches = &StringConstraint{Empty: true}
	} else {
		c.Permanode.Value = val
	}
	return c
}

func permWithAttrSubstr(attr string, c *StringConstraint) *Constraint {
	return &Constraint{
		Permanode: &PermanodeConstraint{
			Attr:         attr,
			ValueMatches: c,
		},
	}
}

func permOfFile(fc *FileConstraint) *Constraint {
	return &Constraint{
		Permanode: &PermanodeConstraint{
			Attr:       nodeattr.CamliContent,
			ValueInSet: &Constraint{File: fc},
		},
	}
}

func whRatio(fc *FloatConstraint) *Constraint {
	return permOfFile(&FileConstraint{
		IsImage: true,
		WHRatio: fc,
	})
}

func parseWHExpression(expr string) (min, max string, err error) {
	if m := whRangeExpr.FindStringSubmatch(expr); m != nil {
		return m[1], m[2], nil
	}
	if m := whValueExpr.FindStringSubmatch(expr); m != nil {
		return m[1], m[1], nil
	}
	return "", "", fmt.Errorf("Unable to parse %q as range, wanted something like 480-1024, 480-, -1024 or 1024", expr)
}

func parseTimePrefix(when string) (time.Time, error) {
	if len(when) < len(base) {
		when += base[len(when):]
	}
	return time.Parse(time.RFC3339, when)
}

func whIntConstraint(mins, maxs string) *IntConstraint {
	ic := &IntConstraint{}
	if mins != "" {
		if mins == "0" {
			ic.ZeroMin = true
		} else {
			n, _ := strconv.Atoi(mins)
			ic.Min = int64(n)
		}
	}
	if maxs != "" {
		if maxs == "0" {
			ic.ZeroMax = true
		} else {
			n, _ := strconv.Atoi(maxs)
			ic.Max = int64(n)
		}
	}
	return ic
}

func mimeFromFormat(v string) (string, error) {
	if strings.Contains(v, "/") {
		return v, nil
	}
	switch v {
	case "jpg", "jpeg":
		return "image/jpeg", nil
	case "gif":
		return "image/gif", nil
	case "png":
		return "image/png", nil
	case "pdf":
		return "application/pdf", nil // RFC 3778
	}
	return "", fmt.Errorf("Unknown format: %s", v)
}

// Custom predicates

type isPost struct {
	matchEqual
}

func newIsPost() keyword {
	return isPost{"is:post"}
}

func (k isPost) Description() string {
	return "matches tweets, status updates, blog posts, etc"
}

func (k isPost) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	return &Constraint{
		Permanode: &PermanodeConstraint{
			Attr:  nodeattr.Type,
			Value: "twitter.com:tweet",
		},
	}, nil
}

type isLike struct {
	matchEqual
}

func newIsLike() keyword {
	return isLike{"is:like"}
}

func (k isLike) Description() string {
	return "matches liked tweets"
}

func (k isLike) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	return &Constraint{
		Permanode: &PermanodeConstraint{
			Attr:  nodeattr.Type,
			Value: "twitter.com:like",
		},
	}, nil
}

type isCheckin struct {
	matchEqual
}

func newIsCheckin() keyword {
	return isCheckin{"is:checkin"}
}

func (k isCheckin) Description() string {
	return "matches location check-ins (foursquare, etc)"
}

func (k isCheckin) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	return &Constraint{
		Permanode: &PermanodeConstraint{
			Attr:  nodeattr.Type,
			Value: "foursquare.com:checkin",
		},
	}, nil
}

type filename struct {
	matchPrefix
}

func newFilename() keyword {
	return filename{newMatchPrefix("filename")}
}

func (fn filename) Description() string {
	return "Match filename, case sensitively. Supports optional '*' wildcard at beginning, end, or both."
}

func (fn filename) Predicate(ctx context.Context, args []string) (*Constraint, error) {
	arg := args[0]
	switch {
	case !strings.Contains(arg, "*"):
		return permOfFile(&FileConstraint{FileName: &StringConstraint{Equals: arg}}), nil
	case strings.HasPrefix(arg, "*") && !strings.Contains(arg[1:], "*"):
		suffix := arg[1:]
		return permOfFile(&FileConstraint{FileName: &StringConstraint{HasSuffix: suffix}}), nil
	case strings.HasSuffix(arg, "*") && !strings.Contains(arg[:len(arg)-1], "*"):
		prefix := arg[:len(arg)-1]
		return permOfFile(&FileConstraint{FileName: &StringConstraint{
			HasPrefix: prefix,
		}}), nil
	case strings.HasSuffix(arg, "*") && strings.HasPrefix(arg, "*") && !strings.Contains(arg[1:len(arg)-1], "*"):
		sub := arg[1 : len(arg)-1]
		return permOfFile(&FileConstraint{FileName: &StringConstraint{Contains: sub}}), nil
	}
	return nil, errors.New("unsupported glob wildcard in filename search predicate")
}
