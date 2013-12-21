/*
Copyright 2013 The Camlistore Authors

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
	"errors"
	"regexp"
	"strings"
)

var tagExpr = regexp.MustCompile(`^tag:(\w+)$`)

// parseExpression parses a search expression (e.g. "tag:funny
// near:portland") and returns a SearchQuery for that search text. The
// Constraint field will always be set. The Limit and Sort may also be
// set.
func parseExpression(exp string) (*SearchQuery, error) {
	q := func(c Constraint) (*SearchQuery, error) {
		return &SearchQuery{Constraint: &c}, nil
	}
	exp = strings.TrimSpace(exp)
	if exp == "" {
		return q(Constraint{
			Permanode: &PermanodeConstraint{
				SkipHidden: true,
			}})
	}
	if m := tagExpr.FindStringSubmatch(exp); m != nil {
		return q(Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       "tag",
				SkipHidden: true,
				Value:      m[1],
			}})
	}
	return nil, errors.New("unknown expression")
}
