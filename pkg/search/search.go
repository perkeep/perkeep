/*
Copyright 2011 The Camlistore Authors.

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

// Package search describes and answers Camlistore search queries.
//
// Many of the search methods or functions provide results that are
// ordered by modification time, or at least depend on modification
// times. In that context, (un)deletions (of permanodes, or attributes)
// are not considered modifications and therefore the time at which they
// occured does not affect the result.
package search

type QueryDescriber interface {
	Query(*SearchQuery) (*SearchResult, error)
	Describe(*DescribeRequest) (*DescribeResponse, error)
}
