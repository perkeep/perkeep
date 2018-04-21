/*
Copyright 2017 The Perkeep Authors.

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

// Package mapquery provides a Query object suitable to send search queries from
// the map aspect of the web UI. It is not concurrent safe.
package mapquery

import (
	"context"
	"fmt"
	"strings"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/types/camtypes"
	"perkeep.org/server/perkeepd/ui/goui/geo"

	"github.com/gopherjs/gopherjs/js"
	"honnef.co/go/js/dom"
)

// Query holds the parameters for the current query.
type Query struct {
	// AuthToken is the token for authenticating with Perkeep.
	AuthToken string
	// Expr is the search query expression.
	Expr string
	// Limit is the maximum number of search results that should be returned.
	Limit int
	// zoom is the location area that was requested for the last successful query.
	zoom *camtypes.LocationBounds
	// nextZoom is the location area that is requested for the next query.
	nextZoom *camtypes.LocationBounds
	// Callback is the function to run on the JSON-ified search results, if the search
	// was successful.
	Callback func(searchResults string)
	// Cleanup is run once, right before Callback, or on any error that occurs
	// before Callback.
	Cleanup func()
	// pending makes sure there's only ever one query at most in flight.
	pending bool

	cl *client.Client // initialized on first send
}

// New returns a new query as a javascript object, or nil if expr violates the
// rules about the zoom (map:) predicate. See SetZoom for the rules.
func New(authToken string, expr string,
	callback func(searchResults string),
	cleanup func()) *js.Object {
	if err := checkZoomExpr(expr); err != nil {
		dom.GetWindow().Alert(fmt.Sprintf("%v", err))
		return nil
	}
	expr = ShiftZoomPredicate(expr)
	return js.MakeWrapper(&Query{
		AuthToken: authToken,
		Expr:      expr,
		Callback:  callback,
		Cleanup:   cleanup,
		Limit:     50,
	})
}

// GetExpr returns the search expression of the query.
func (q *Query) GetExpr() string {
	return q.Expr
}

func (q *Query) SetLimit(limit int) {
	q.Limit = limit
}

// Send sends the search query, and runs the Query's callback on success. It
// returns immediately if there's already a query in flight.
func (q *Query) Send() {
	if q.pending {
		q.Cleanup()
		return
	}
	q.pending = true
	go func() {
		resp, err := q.send()
		if err != nil {
			dom.GetWindow().Alert(fmt.Sprintf("%v", err))
		}
		q.Callback(string(resp))
	}()
}

func (q *Query) send() ([]byte, error) {
	defer q.Cleanup()
	defer func() {
		q.pending = false
	}()
	if q.cl == nil {
		am, err := auth.NewTokenAuth(q.AuthToken)
		if err != nil {
			return nil, err
		}
		q.cl, err = client.New(client.OptionAuthMode(am))
		if err != nil {
			return nil, err
		}
	}
	q.Expr = ShiftZoomPredicate(q.Expr)
	expr := mapToLocrect(q.Expr)
	req := &search.SearchQuery{
		Expression: expr,
		Describe: &search.DescribeRequest{
			Depth: 1,
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent", "camliContentImage"},
				},
				{
					IfCamliNodeType: "foursquare.com:checkin",
					Attrs:           []string{"foursquareVenuePermanode"},
				},
				{
					IfCamliNodeType: "foursquare.com:venue",
					Attrs:           []string{"camliPath:photos"},
					Rules: []*search.DescribeRule{
						{
							Attrs: []string{"camliPath:*"},
						},
					},
				},
			},
		},
		Limit: q.Limit,
		Sort:  search.MapSort,
	}
	resp, err := q.cl.QueryRaw(context.TODO(), req)
	if err != nil {
		return nil, err
	}
	q.zoom = q.nextZoom
	return resp, nil
}

// checkZoomExpr verifies that expr does not violate the rules about the map
// predicate, which are:
// 1) only one map predicate per expression
// 2) since it is interpreted as a logical "and" to the rest of the expression,
// logical "or"s around it are forbidden.
// To be complete we should strip any potential parens around the map
// predicate itself. But if we start concerning ourselves with such details, we
// should switch to using a proper parser, like it is done server-side.
func checkZoomExpr(expr string) error {
	sq := strings.TrimSpace(expr)
	if sq == "" {
		return nil
	}
	fields := strings.Fields(sq)
	if len(fields) == 1 {
		return nil
	}
	var pos []int
	for k, v := range fields {
		if geo.IsLocMapPredicate(v) {
			pos = append(pos, k)
		}
	}
	// Did we find several "map:" predicates?
	if len(pos) > 1 {
		return fmt.Errorf("map predicate should be unique. See https://camlistore.org/doc/search-ui")
	}
	for _, v := range pos {
		// does it have an "or" following?
		if v < len(fields)-1 && fields[v+1] == "or" {
			return fmt.Errorf(`map predicate with logical "or" forbidden. See https://camlistore.org/doc/search-ui`)
		}
		// does it have a preceding "or"?
		if v > 0 && fields[v-1] == "or" {
			return fmt.Errorf(`map predicate with logical "or" forbidden. See https://camlistore.org/doc/search-ui`)
		}
	}
	return nil
}

// SetZoom modifies the query's search expression: it uses the given coordinates
// in a map predicate to constrain the search expression to the defined area,
// effectively acting like a map zoom.
//
// The map predicate is defined like locrect, and it has a similar meaning.
// However, it is not defined server-side, and it is specifically meant to
// represent the area of the world that is visible in the screen when using the map
// aspect, and in particular when zooming or panning. As such, it follows stricter
// rules than the other predicates, which are:
//
// 1. only one map predicate is allowed in the whole expression.
// 2. since the map predicate is interpreted as if it were a logical 'and' with
// the rest of the whole expression (regardless of its position within the
// expression), logical 'or's around it are forbidden.
//
// The map predicate is also moved to the end of the expression, for clarity.
func (q *Query) SetZoom(north, west, south, east float64) {
	if west <= east && east-west > 360 {
		// we're just zoomed out very far
		west = -179.99
		east = 179.99
	}
	const precision = 1e-6
	// since we print the locrect at a given arbitrary precision (e-6), we need to
	// round everything "up", to make sure we don't exclude points on the boundaries.
	newNorth := north + precision
	newSouth := south - precision
	newWest := camtypes.Longitude(west - precision).WrapTo180()
	newEast := camtypes.Longitude(east + precision).WrapTo180()

	q.nextZoom = &camtypes.LocationBounds{
		North: newNorth,
		South: newSouth,
		West:  newWest,
		East:  newEast,
	}
	zoomExpr := fmt.Sprintf("map:%.6f,%.6f,%.6f,%.6f", newNorth, newWest, newSouth, newEast)

	q.Expr = handleZoomPredicate(q.Expr, false, zoomExpr)
}

// GetZoom returns the location area that was requested for the last successful
// query.
func (q *Query) GetZoom() *camtypes.LocationBounds {
	return q.zoom
}

// HasZoomParameter returns whether queryString is the "q" parameter of a search
// query, and whether that parameter contains a map zoom (map predicate).
func HasZoomParameter(queryString string) bool {
	qs := strings.TrimSpace(queryString)
	if !strings.HasPrefix(qs, "q=") {
		return false
	}
	qs = strings.TrimPrefix(qs, "q=")
	fields := strings.Fields(qs)
	for _, v := range fields {
		if geo.IsLocMapPredicate(v) {
			return true
		}
	}
	return false
}

// mapToLocrect looks for a trailing "map:" predicate and changes it to a
// "locrect:" predicate if found. It also adds parentheses around all the rest of
// the expression that is before the map predicate.
func mapToLocrect(expr string) string {
	sq := strings.TrimSpace(expr)
	if sq == "" {
		return expr
	}
	fields := strings.Fields(expr)
	lastPred := fields[len(fields)-1]
	if geo.IsLocMapPredicate(lastPred) {
		locrect := strings.Replace(lastPred, "map:", "locrect:", 1)
		if len(fields) == 1 {
			return locrect
		}
		return fmt.Sprintf("(%v) %v", strings.Join(fields[:len(fields)-1], " "), locrect)
	}
	return expr
}

// ShiftZoomPredicate looks for a "map:" predicate in expr, and if found, moves
// it at the end of the expression if necessary.
func ShiftZoomPredicate(expr string) string {
	return handleZoomPredicate(expr, false, "")
}

// DeleteZoomPredicate looks for a "map:" predicate in expr, and if found,
// removes it.
func DeleteZoomPredicate(expr string) string {
	return handleZoomPredicate(expr, true, "")
}

func handleZoomPredicate(expr string, delete bool, replacement string) string {
	if delete && replacement != "" {
		panic("deletion mode and replacement mode are mutually exclusive")
	}
	var replace bool
	if replacement != "" {
		replace = true
	}

	sq := strings.TrimSpace(expr)
	if sq == "" {
		return expr
	}
	fields := strings.Fields(expr)
	pos := -1
	for k, v := range fields {
		if geo.IsLocMapPredicate(v) {
			pos = k
			break
		}
	}

	// easiest case: there is no zoom
	if pos == -1 {
		if replace {
			return sq + " " + replacement
		}
		return sq
	}

	// there's already a zoom at the end
	if pos == len(fields)-1 {
		if delete {
			return strings.Join(fields[:pos], " ")
		}
		if replace {
			return strings.Join(fields[:pos], " ") + " " + replacement
		}
		return sq
	}

	// There's a zoom somewhere else in the expression

	// does it have a preceding "and"?
	var before int
	if pos > 0 && fields[pos-1] == "and" {
		before = pos - 1
	} else {
		before = pos
	}
	// does it have a following "and"?
	var after int
	if pos < len(fields)-1 && fields[pos+1] == "and" {
		after = pos + 2
	} else {
		after = pos + 1
	}
	// erase potential "and"s, and shift the zoom to the end of the expression
	if delete {
		return strings.Join(fields[:before], " ") + " " + strings.Join(fields[after:], " ")
	}
	if replace {
		return strings.Join(fields[:before], " ") + " " + strings.Join(fields[after:], " ") +
			" " + replacement
	}
	return strings.Join(fields[:before], " ") + " " + strings.Join(fields[after:], " ") +
		" " + fields[pos]
}
