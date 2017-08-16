/*
Copyright 2017 The Camlistore Authors.

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
// the map aspect of the web UI.
package mapquery

import (
	"fmt"
	"strings"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/search"
	"camlistore.org/pkg/types/camtypes"
	"camlistore.org/server/camlistored/ui/goui/geo"

	"github.com/gopherjs/gopherjs/js"
	"honnef.co/go/js/dom"
)

// Query holds the parameters for the current query.
type Query struct {
	// AuthToken is the token for authenticating with Camlistore.
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
}

// New returns a new query as a javascript object.
func New(authToken string, expr string, callback func(searchResults string)) *js.Object {
	return js.MakeWrapper(&Query{
		AuthToken: authToken,
		Expr:      expr,
		Callback:  callback,
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

// Send sends the search query, and runs the Query's callback on success.
func (q *Query) Send() {
	go func() {
		if err := q.send(); err != nil {
			dom.GetWindow().Alert(fmt.Sprintf("%v", err))
		}
	}()
}

func (q *Query) send() error {
	am, err := auth.NewTokenAuth(q.AuthToken)
	if err != nil {
		return err
	}
	cl := newClient(am)
	req := &search.SearchQuery{
		Expression: q.Expr,
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
	resp, err := cl.QueryRaw(req)
	if err != nil {
		return err
	}
	q.zoom = q.nextZoom
	q.Callback(string(resp))
	return nil
}

func newClient(am auth.AuthMode) *client.Client {
	cl := client.NewFromParams("", am, client.OptionSameOrigin(true))
	// Here we force the use of the http.DefaultClient. Otherwise, we'll hit
	// one of the net.Dial* calls due to custom transport we set up by default
	// in pkg/client. Which we don't want because system calls are prohibited by
	// gopherjs.
	cl.SetHTTPClient(nil)
	return cl
}

// SetZoom modifies the query's search expression: it uses the given coordinates
// in a locrect predicate to constrain the search expression to the defined area,
// effectively acting like a map zoom. It modifies the expression according to the
// following rules:
//
// If the current expression does not end with a locrect, or is
// not surrounded with parentheses, it gets surrounded with parentheses (as a
// visual cue, to make it more explicit that the locrect is an added zoom), and the
// locrect is appended. i.e: "expr" -> "(expr) locrect:n,w,s,e"
//
// Otherwise (if the expression already ends with a locrect, and the left hand side
// is already surrounded by parentheses), the current ending locrect is interpreted
// as being the current zoom level. So it gets replaced with the given coordinates.
// i.e.: "(expr) locrect:n1,w1,s1,e1" -> "(expr) locrect:n2,w2,s2,e2".
func (q *Query) SetZoom(north, west, south, east float64) {
	if west <= east && east-west > 360 {
		// we're just zoomed out very far
		west = -179.99
		east = 179.99
	}
	sq := strings.TrimSpace(q.Expr)
	lastSpace := strings.LastIndex(sq, " ")
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
	zoomAdded := fmt.Sprintf("(%v) locrect:%.6f,%.6f,%.6f,%.6f", sq, newNorth, newWest, newSouth, newEast)

	if lastSpace == -1 {
		// easiest case: one simple (as in, not logically composed) expression. so we
		// only have to append the locrect.
		q.Expr = zoomAdded
		return
	}

	// otherwise we have a logically composed expression
	lhs := sq[:lastSpace]

	// check if LHS is paren surrounded
	if !(strings.HasPrefix(lhs, "(") && strings.HasSuffix(lhs, ")")) {
		// no parens around lhs, which means the rhs is not a locrect that was
		// previously added by appendLocation (i.e. whatever it is, it was entered by the
		// user). So we don't touch it, and we append.
		q.Expr = zoomAdded
		return
	}

	// check if RHS is a locrect, i.e. a zoom level that we did previously append in
	// appendLocation.
	rhs := sq[lastSpace+1:]
	if _, err := geo.RectangleFromPredicate(rhs); err != nil {
		// not a valid locrect, so we add our own, for the same reason as above.
		q.Expr = zoomAdded
		return
	}

	// RHS is a valid zoom level that we previously added, so we replace it with the
	// new one.
	q.Expr = fmt.Sprintf("%v locrect:%.6f,%.6f,%.6f,%.6f", lhs, newNorth, newWest, newSouth, newEast)
}

// GetZoom returns the location area that was requested for the last successful
// query.
func (q *Query) GetZoom() *camtypes.LocationBounds {
	return q.zoom
}
