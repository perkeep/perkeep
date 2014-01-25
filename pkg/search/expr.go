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
	"time"
	"unicode/utf8"

	"camlistore.org/pkg/context"
	"camlistore.org/pkg/geocode"
	"camlistore.org/pkg/types"
)

var (
	tagExpr   = regexp.MustCompile(`^tag:(\w+)$`)
	titleExpr = regexp.MustCompile(`^title:(\S+)$`) // TODO: proper expr parser supporting quoting

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

	words := splitExpr(exp)
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
		if m := titleExpr.FindStringSubmatch(word); m != nil {
			and(&Constraint{
				Permanode: &PermanodeConstraint{
					Attr:       "title",
					SkipHidden: true,
					ValueMatches: &StringConstraint{
						Contains:        m[1],
						CaseInsensitive: true,
					},
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
			andWHRatio(&FloatConstraint{Min: 1.6})
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
		if strings.HasPrefix(word, "before:") || strings.HasPrefix(word, "after:") {
			before := false
			when := ""
			if strings.HasPrefix(word, "before:") {
				before = true
				when = strings.TrimPrefix(word, "before:")
			} else {
				when = strings.TrimPrefix(word, "after:")
			}
			base := "0000-01-01T00:00:00Z"
			if len(when) < len(base) {
				when += base[len(when):]
			}
			t, err := time.Parse(time.RFC3339, when)
			if err != nil {
				return nil, err
			}
			tc := &TimeConstraint{}
			if before {
				tc.Before = types.Time3339(t)
			} else {
				tc.After = types.Time3339(t)
			}
			and(&Constraint{
				Permanode: &PermanodeConstraint{
					Time: tc,
				},
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

// Tokens are:
//    literal
//    foo:     (for operators)
//    "quoted string"
//    " "  (for any amount of space)
//    "-" negative sign
func tokenizeExpr(exp string) []string {
	var tokens []string
	for len(exp) > 0 {
		var token string
		token, exp = firstToken(exp)
		tokens = append(tokens, token)
	}
	return tokens
}

func firstToken(s string) (token, rest string) {
	if s[0] == '-' {
		return "-", s[1:]
	}
	if isSpace(s[0]) {
		for len(s) > 0 && isSpace(s[0]) {
			s = s[1:]
		}
		return " ", s
	}
	if s[0] == '"' {
		quote := false
		for i, r := range s[1:] {
			if quote {
				quote = false
				continue
			}
			if r == '\\' {
				quote = true
				continue
			}
			if r == '"' {
				return s[:i+2], s[i+2:]
			}
		}
	}
	for i, r := range s {
		if r == ':' {
			return s[:i+1], s[i+1:]
		}
		if r < utf8.RuneSelf && isSpace(byte(r)) {
			return s[:i], s[i:]
		}
	}
	return s, ""
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\n', '\r', '\t':
		return true
	}
	return false
}

// Basically just strings.Fields for now but with de-quoting of quoted
// tokens after operators.
func splitExpr(exp string) []string {
	tokens := tokenizeExpr(strings.TrimSpace(exp))
	if len(tokens) == 0 {
		return nil
	}
	// Turn any pair of ("operator:", `"quoted string"`) tokens into
	// ("operator:", "quoted string"), unquoting the second.
	for i, token := range tokens[:len(tokens)-1] {
		nextToken := tokens[i+1]
		if strings.HasSuffix(token, ":") && strings.HasPrefix(nextToken, "\"") {
			if uq, err := strconv.Unquote(nextToken); err == nil {
				tokens[i+1] = uq
			}
		}
	}

	// Split on space tokens and concatenate all the other tokens.
	// Not particularly efficient, though.
	var f []string
	for i, token := range tokens {
		if i == 0 {
			f = append(f, token)
		} else if token == " " {
			f = append(f, "")
		} else {
			f[len(f)-1] += token
		}
	}
	return f
}
