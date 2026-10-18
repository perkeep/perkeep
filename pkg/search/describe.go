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

package search

import (
	"context"
	"log"
	"net/http"
	"time"

	"perkeep.org/internal/httputil"
	"perkeep.org/internal/searchtypes"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/types/camtypes"
)

func (h *Handler) serveDescribe(rw http.ResponseWriter, req *http.Request) {
	defer httputil.RecoverJSON(rw, req)
	var dr DescribeRequest
	searchtypes.DescribeRequestFromHTTP(&dr, req)
	ctx := context.TODO()

	res, err := h.Describe(ctx, &dr)
	if err != nil {
		httputil.ServeJSONError(rw, err)
		return
	}
	httputil.ReturnJSON(rw, res)
}

const verboseDescribe = false

// Describe returns a response for the given describe request. It acquires RLock
// on the Handler's index.
func (h *Handler) Describe(ctx context.Context, dr *DescribeRequest) (dres *DescribeResponse, err error) {
	h.index.RLock()
	defer h.index.RUnlock()

	return h.DescribeLocked(ctx, dr)
}

// DescribeLocked returns a response for the given describe request. It is the
// caller's responsibility to lock the search handler's index.
func (h *Handler) DescribeLocked(ctx context.Context, dr *DescribeRequest) (dres *DescribeResponse, err error) {
	if verboseDescribe {
		t0 := time.Now()
		defer func() {
			td := time.Since(t0)
			var num int
			if dres != nil {
				num = len(dres.Meta)
			}
			log.Printf("Described %d blobs in %v", num, td)
		}()
	}
	h.initDescribeRequest(dr)
	return searchtypes.Describe(ctx, dr)
}

// The describe protocol types and their methods live in searchtypes so clients
// do not need to import the search handler or index implementation.
type (
	DescribeRequest    = searchtypes.DescribeRequest
	DescribeRule       = searchtypes.DescribeRule
	DescribeResponse   = searchtypes.DescribeResponse
	MetaMap            = searchtypes.MetaMap
	DescribedBlob      = searchtypes.DescribedBlob
	DescribedPermanode = searchtypes.DescribedPermanode
	DescribeError      = searchtypes.DescribeError
)

// NewDescribeRequest returns a new DescribeRequest holding the state
// of blobs and their summarized descriptions.  Use DescribeBlob
// one or more times before calling Result.
func (h *Handler) NewDescribeRequest() *DescribeRequest {
	dr := new(DescribeRequest)
	h.initDescribeRequest(dr)
	return dr
}

func (h *Handler) initDescribeRequest(req *DescribeRequest) {
	searchtypes.InitDescribeRequest(req, h.index, h.owner.KeyID(),
		func(ctx context.Context, br blob.Ref, at time.Time) (camtypes.Location, error) {
			return h.lh.PermanodeLocation(ctx, br, at, h.owner)
		})
}
