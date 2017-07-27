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
	"encoding/json"
	"fmt"

	"camlistore.org/pkg/auth"
	"camlistore.org/pkg/client"
	"camlistore.org/pkg/search"

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
	sr, err := cl.Query(req)
	if err != nil {
		return err
	}
	srjson, err := json.Marshal(sr)
	if err != nil {
		return err
	}
	q.Callback(string(srjson))
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
