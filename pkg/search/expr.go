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
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"camlistore.org/pkg/context"
	"camlistore.org/pkg/geocode"
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
func parseExpression(ctx *context.Context, exp string) (*SearchQuery, error) {
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

	andNot := false // whether the next and(x) is really a and(!x)
	and := func(c *Constraint) {
		old := sq.Constraint
		if andNot {
			c = &Constraint{
				Logical: &LogicalConstraint{
					Op: "not",
					A:  c,
				},
			}
		}
		sq.Constraint = &Constraint{
			Logical: &LogicalConstraint{
				Op: "and",
				A:  old,
				B:  c,
			},
		}
	}
	permOfFile := func(fc *FileConstraint) *Constraint {
		return &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       "camliContent",
				ValueInSet: &Constraint{File: fc},
			},
		}
	}
	orConst := func(a, b *Constraint) *Constraint {
		return &Constraint{
			Logical: &LogicalConstraint{
				Op: "or",
				A:  a,
				B:  b,
			},
		}
	}
	andFile := func(fc *FileConstraint) {
		and(permOfFile(fc))
	}
	andWHRatio := func(fc *FloatConstraint) {
		andFile(&FileConstraint{
			IsImage: true,
			WHRatio: fc,
		})
	}

	words := strings.Fields(exp)
	for _, word := range words {
		andNot = false
		if strings.HasPrefix(word, "-") {
			andNot = true
			word = word[1:]
		}
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
		if word == "has:location" {
			andFile(&FileConstraint{
				IsImage: true,
				Location: &LocationConstraint{
					Any: true,
				},
			})
			continue
		}
		if strings.HasPrefix(word, "format:") {
			andFile(&FileConstraint{
				MIMEType: &StringConstraint{
					Equals: mimeFromFormat(strings.TrimPrefix(word, "format:")),
				},
			})
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
			continue
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
			continue
		}
		if strings.HasPrefix(word, "loc:") {
			where := strings.TrimPrefix(word, "loc:")
			rects, err := geocode.Lookup(ctx, where)
			if err != nil {
				return nil, err
			}
			if len(rects) == 0 {
				return nil, fmt.Errorf("No location found for %q", where)
			}
			var locConstraint *Constraint
			for i, rect := range rects {
				rectConstraint := permOfFile(&FileConstraint{
					IsImage: true,
					Location: &LocationConstraint{
						West:  rect.SouthWest.Long,
						East:  rect.NorthEast.Long,
						North: rect.NorthEast.Lat,
						South: rect.SouthWest.Lat,
					},
				})
				if i == 0 {
					locConstraint = rectConstraint
				} else {
					locConstraint = orConst(locConstraint, rectConstraint)
				}
			}
			and(locConstraint)
			continue
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

func mimeFromFormat(v string) string {
	if strings.Contains(v, "/") {
		return v
	}
	switch v {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "png":
		return "image/png"
	case "pdf":
		return "application/pdf" // RFC 3778
	}
	return "???"
}
