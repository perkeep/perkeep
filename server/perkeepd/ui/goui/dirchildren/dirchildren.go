/*
Copyright 2018 The Perkeep Authors.

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

// Package dirchildren provides a Query object suitable to send search queries
// to get the children of a directory, for use in the Directory aspect of the web
// UI. It is not concurrent safe.
package dirchildren

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/search"

	"github.com/gopherjs/gopherjs/js"
	"honnef.co/go/js/dom"
)

// Query holds the parameters for the current query.
type Query struct {
	// AuthToken is the token for authenticating with Camlistore.
	AuthToken string
	// Limit is the maximum number of search results that should be returned.
	Limit int
	// Around is the blob around which the returned results should be centered. It
	// implies that the search results can be sorted.
	Around blob.Ref
	cl     *client.Client // initialized on first send

	// UpdateSearchSession is provided by the caller, to update its search session,
	// with the provided set of results, in JSON form.
	UpdateSearchSession func(res string)
	// TriggerRender is provided by the caller, to start rerendering the DOM, after
	// the query's results have been merged with the current set of results, and the
	// caller's search session has been updated with that set.
	TriggerRender func()

	// ParentDir is the directory the query is about.
	ParentDir blob.Ref
	// Blobs is the currently known set of descendants of ParentDir. Subsequent new
	// query results, i.e. with a moving Around parameter, are merged with Blobs.
	Blobs []*search.SearchResultBlob
	// Meta is the map of descriptions for the blobs in Blobs.
	Meta search.MetaMap

	// pending makes sure there's only ever one query at most in flight.
	pending bool
	// isComplete is whether we've already gotten all the descendants of ParentDir.
	isComplete bool
}

// New returns a new Query as a javascript object, of nil if parentDir is not a
// valid blobRef.
func New(authToken, parentDir string, limit int,
	updadeSearchSession func(string), triggerRender func()) *js.Object {
	parentDirbr, ok := blob.Parse(parentDir)
	if !ok {
		dom.GetWindow().Alert(fmt.Sprintf("invalid parentDir blobRef: %q", parentDir))
		return nil
	}
	return js.MakeWrapper(&Query{
		ParentDir:           parentDirbr,
		AuthToken:           authToken,
		UpdateSearchSession: updadeSearchSession,
		TriggerRender:       triggerRender,
		Limit:               limit,
	})
}

// resultsAsJSON returns the current (cumulative) set of results, in JSON form.
func (q *Query) resultsAsJSON() string {
	res := &search.SearchResult{
		Blobs: q.Blobs,
		Describe: &search.DescribeResponse{
			Meta: q.Meta,
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := enc.Encode(res); err != nil {
		fmt.Println(fmt.Sprintf("Error marshaling search results: %v", err))
		return ""
	}
	return buf.String()
}

// IsComplete returns whether all the descendants have already been found.
func (q *Query) IsComplete() bool {
	return q.isComplete
}

// Get asynchronously sends the query, if one is not already in flight, or being
// processed. It runs q.UpdateSearchSession once the results have been received and
// merged with q.Blobs and q.Meta. It runs q.TriggerRender to refresh the DOM with
// the new results in the search session.
func (q *Query) Get() {
	if q.isComplete {
		return
	}
	if q.pending {
		return
	}
	q.pending = true
	go func() {
		if err := q.get(); err != nil {
			dom.GetWindow().Alert(fmt.Sprintf("%v", err))
			return
		}
		q.TriggerRender()
	}()
}

func (q *Query) get() error {
	defer func() {
		q.pending = false
	}()
	if q.cl == nil {
		am, err := auth.NewTokenAuth(q.AuthToken)
		if err != nil {
			return err
		}
		q.cl, err = client.New(client.OptionAuthMode(am))
		if err != nil {
			return err
		}
	}
	req := &search.SearchQuery{
		Constraint: &search.Constraint{
			Logical: &search.LogicalConstraint{
				A: &search.Constraint{File: &search.FileConstraint{
					ParentDir: &search.DirConstraint{
						BlobRefPrefix: q.ParentDir.String(),
					},
				}},
				B: &search.Constraint{Dir: &search.DirConstraint{
					ParentDir: &search.DirConstraint{
						BlobRefPrefix: q.ParentDir.String(),
					},
				}},
				Op: "or",
			},
		},
		Describe: &search.DescribeRequest{
			Depth: 1,
			Rules: []*search.DescribeRule{
				{
					Attrs: []string{"camliContent", "camliContentImage"},
				},
			},
		},
		Limit: q.Limit,
		Sort:  search.BlobRefAsc,
	}
	if q.Around.Valid() {
		req.Around = q.Around
	}

	sr, err := q.cl.Query(context.TODO(), req)
	if err != nil {
		return err
	}
	q.mergeResults(sr)
	res := q.resultsAsJSON()
	// TODO(mpl): there has to be a more efficient way for passing the results to
	// the search session through this function, than encoding the results to JSON, but
	// I haven't found it. Waiting for Paul's feedback on it.
	q.UpdateSearchSession(res)
	return nil
}

func (q *Query) mergeResults(results *search.SearchResult) {
	if q.isComplete {
		return
	}
	if results == nil || len(results.Blobs) == 0 {
		return
	}
	if results.Describe == nil || results.Describe.Meta == nil {
		return
	}
	requestedAround := q.Around
	if len(q.Blobs) == 0 {
		// first batch
		q.Blobs = results.Blobs
		q.Meta = results.Describe.Meta
		q.Around = q.Blobs[len(q.Blobs)-1].Blob
		return
	}
	lastInResults := results.Blobs[len(results.Blobs)-1].Blob

	var found bool
	var afterAroundIdx int
	// Look for merging point.
	// First jump to the middle of results.Blobs and see if that's Around. if not,
	// do slow search.
	middle := len(results.Blobs) / 2
	if results.Blobs[middle].Blob == requestedAround {
		// odd case
		found = true
		afterAroundIdx = middle + 1
	} else if results.Blobs[middle+1].Blob == requestedAround {
		// even case
		found = true
		afterAroundIdx = middle + 2
	} else {
		// slow search
		for i := len(results.Blobs) - 1; i >= 0; i-- {
			if results.Blobs[i].Blob != requestedAround {
				continue
			}
			afterAroundIdx = i + 1
			found = true
			break
		}
	}
	if !found {
		return
	}

	if requestedAround == lastInResults {
		// we don't have to worry about out of order batches, because we only "increment"
		// the q.Around when we've received the previously requested one.
		q.isComplete = true
	}
	// Reject "stale" results. they should never occur though, since we
	// suppress with q.pending, and we request everything in order.
	if _, ok := q.Meta[lastInResults.String()]; ok {
		return
	}

	q.Blobs = append(q.Blobs, results.Blobs[afterAroundIdx:]...)
	for k, v := range results.Describe.Meta {
		q.Meta[k] = v
	}
	q.Around = q.Blobs[len(q.Blobs)-1].Blob
}
