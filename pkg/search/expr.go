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
	"log"
	"regexp"
	"strings"
)

var tagExpr = regexp.MustCompile(`^tag:(\w+)$`)

// parseExpression parses a search expression (e.g. "tag:funny
// near:portland") and returns a SearchQuery for that search text. The
// Constraint field will always be set. The Limit and Sort may also be
// set.
func parseExpression(exp string) (*SearchQuery, error) {
	base := &Constraint{
		Permanode: &PermanodeConstraint{
			SkipHidden: true,
		},
	}
	sq := &SearchQuery{
		Constraint: base,
	}

	exp = strings.TrimSpace(exp)
	if exp == "" {
		return sq, nil
	}

	and := func(c *Constraint) {
		old := sq.Constraint
		sq.Constraint = &Constraint{
			Logical: &LogicalConstraint{
				Op: "and",
				A:  old,
				B:  c,
			},
		}
	}

	words := strings.Fields(exp)
	for _, word := range words {
		if m := tagExpr.FindStringSubmatch(word); m != nil {
			and(&Constraint{
				Permanode: &PermanodeConstraint{
					Attr:       "tag",
					SkipHidden: true,
					Value:      m[1],
				},
			})
			continue
		}
		if word == "is:image" {
			and(&Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "camliContent",
					ValueInSet: &Constraint{
						File: &FileConstraint{
							IsImage: true,
						},
					},
				},
			})
			continue
		}
		log.Printf("Unknown search expression word %q", word)
		// TODO: finish. better tokenization. non-operator tokens
		// are text searches, etc.
	}

	return sq, nil
}
