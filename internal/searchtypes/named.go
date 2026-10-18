/*
Copyright 2026 The Perkeep Authors

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

package searchtypes

// NamedSearch returns a *SearchQuery to find the permanode of the search alias "name".
func NamedSearch(name string) *SearchQuery {
	return &SearchQuery{
		Constraint: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:  "camliNamedSearch",
				Value: name,
			},
		},
		Describe: &DescribeRequest{},
	}
}
