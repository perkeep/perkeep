/*
Copyright 2013 The Camlistore Authors.

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
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/types/camtypes"

	"golang.org/x/net/context"
)

func SetTestHookBug121(hook func()) {
	testHookBug121 = hook
}

func ExportSetCandidateSourceHook(fn func(string)) { candSourceHook = fn }

func ExportSetExpandLocationHook(val bool) { expandLocationHook = val }

func ExportBufferedConst() int { return buffered }

func (s *SearchQuery) ExportPlannedQuery() *SearchQuery {
	return s.plannedQuery(nil)
}

var SortName = sortName

func (s *Handler) ExportGetPermanodeLocation(ctx context.Context, permaNode blob.Ref,
	at time.Time) (camtypes.Location, error) {
	return s.lh.PermanodeLocation(ctx, permaNode, at, s.owner)
}

func ExportBestByLocation(res *SearchResult, limit int) {
	bestByLocation(res, limit)
}
