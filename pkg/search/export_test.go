/*
Copyright 2013 The Perkeep Authors.

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
	"time"

	"perkeep.org/internal/searchtypes"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/types/camtypes"
)

// SetTestHookBug121 sets whether GetRecentPermanodes finishes describing
// each permanode before starting on the next, to reproduce
// https://perkeep.org/issue/121.
func SetTestHookBug121(wait bool) {
	if wait {
		testHookBug121 = func(dr *DescribeRequest) { searchtypes.DescribeRequestWait(dr) }
	} else {
		testHookBug121 = func(*DescribeRequest) {}
	}
}

func ExportSetCandidateSourceHook(fn func(string)) { candSourceHook = fn }

func ExportSetExpandLocationHook(val bool) { expandLocationHook = val }

func ExportBufferedConst() int { return buffered }

func ExportPlannedQuery(q *SearchQuery) *SearchQuery {
	return searchQueryPlannedQuery(q, nil)
}

var SortName = sortName

func (h *Handler) ExportGetPermanodeLocation(ctx context.Context, permaNode blob.Ref,
	at time.Time) (camtypes.Location, error) {
	return h.lh.PermanodeLocation(ctx, permaNode, at, h.owner)
}

func ExportBestByLocation(res *SearchResult, loc map[blob.Ref]camtypes.Location, limit int) {
	bestByLocation(res, loc, limit)
}

var ExportUitdamLC = uitdamLC
