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

// Package searchtypes holds the shared search protocol types and their
// dependency-light helpers. The search server re-exports these types as aliases.
package searchtypes

import "context"

type QueryDescriber interface {
	Query(context.Context, *SearchQuery) (*SearchResult, error)
	Describe(context.Context, *DescribeRequest) (*DescribeResponse, error)
}

type GetRecentPermanoder interface {
	// GetRecentPermanodes returns recently-modified permanodes.
	// This is a higher-level query returning more metadata than the index.GetRecentPermanodes,
	// which only scans the blobrefs but doesn't return anything about the permanodes.
	GetRecentPermanodes(context.Context, *RecentRequest) (*RecentResponse, error)
}
