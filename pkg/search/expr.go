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
	"log"
	"regexp"
	"strconv"
	"strings"
)

var (
	tagExpr = regexp.MustCompile(`^tag:(\w+)$`)

	// used for width/height ranges. 10 is max length of 32-bit
	// int (strconv.Atoi on 32-bit platforms), even though a max
	// JPEG dimension is only 16-bit.
	whRangeExpr = regexp.MustCompile(`^(\d{0,10})-(\d{0,10})$`)
)

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
	andFile := func(fc *FileConstraint) {
		and(&Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       "camliContent",
				ValueInSet: &Constraint{File: fc},
			},
		})
	}
	andWHRatio := func(fc *FloatConstraint) {
		andFile(&FileConstraint{
			IsImage: true,
			WHRatio: fc,
		})
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
		if word == "is:landscape" {
			andWHRatio(&FloatConstraint{Min: 1.0})
			continue
		}
		if word == "is:portrait" {
			andWHRatio(&FloatConstraint{Max: 1.0})
			continue
		}
		if word == "is:pano" {
			andWHRatio(&FloatConstraint{Min: 1.5})
			continue
		}
		if strings.HasPrefix(word, "width:") {
			m := whRangeExpr.FindStringSubmatch(strings.TrimPrefix(word, "width:"))
			if m == nil {
				return nil, errors.New("bogus width range")
			}
			andFile(&FileConstraint{
				IsImage: true,
				Width:   whIntConstraint(m[1], m[2]),
			})
		}
		if strings.HasPrefix(word, "height:") {
			m := whRangeExpr.FindStringSubmatch(strings.TrimPrefix(word, "height:"))
			if m == nil {
				return nil, errors.New("bogus height range")
			}
			andFile(&FileConstraint{
				IsImage: true,
				Height:  whIntConstraint(m[1], m[2]),
			})
		}
		log.Printf("Unknown search expression word %q", word)
		// TODO: finish. better tokenization. non-operator tokens
		// are text searches, etc.
	}

	return sq, nil
}

func whIntConstraint(mins, maxs string) *IntConstraint {
	ic := &IntConstraint{}
	if mins != "" {
		if mins == "0" {
			ic.ZeroMin = true
		} else {
			n, _ := strconv.Atoi(mins)
			ic.Min = int64(n)
		}
	}
	if maxs != "" {
		if maxs == "0" {
			ic.ZeroMax = true
		} else {
			n, _ := strconv.Atoi(maxs)
			ic.Max = int64(n)
		}
	}
	return ic
}
