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
	tagExpr   = regexp.MustCompile(`^tag:(.+)$`)
	titleExpr = regexp.MustCompile(`^title:(.+)$`)
	attrExpr  = regexp.MustCompile(`^attr:(\w+):(.+)$`)

	// childrenof:sha1-xxxx where xxxx is a full blobref or even
	// just a prefix of one. only matches permanodes currently.
	childrenOfExpr = regexp.MustCompile(`^childrenof:(\S+)$`)

	// used for width/height ranges. 10 is max length of 32-bit
	// int (strconv.Atoi on 32-bit platforms), even though a max
	// JPEG dimension is only 16-bit.
	whRangeExpr = regexp.MustCompile(`^(\d{0,10})-(\d{0,10})$`)
	whValueExpr = regexp.MustCompile(`^(\d{0,10})$`)
)

var (
	errNoMatchingOpening   = errors.New("No matching opening parenthesis")
	errNoMatchingClosing   = errors.New("No matching closing parenthesis")
	errCannotStartBinaryOp = errors.New("Expression cannot start with a binary operator")
	errExpectedAtom        = errors.New("Expected an atom")
)

func andConst(a, b *Constraint) *Constraint {
	return &Constraint{
		Logical: &LogicalConstraint{
			Op: "and",
			A:  a,
			B:  b,
		},
	}
}

func orConst(a, b *Constraint) *Constraint {
	return &Constraint{
		Logical: &LogicalConstraint{
			Op: "or",
			A:  a,
			B:  b,
		},
	}
}

func notConst(a *Constraint) *Constraint {
	return &Constraint{
		Logical: &LogicalConstraint{
			Op: "not",
			A:  a,
		},
	}
}

func stripNot(tokens []string) (negated bool, rest []string) {
	rest = tokens
	for len(rest) > 0 {
		if rest[0] != "-" {
			return negated, rest
		} else {
			negated = !negated
			rest = rest[1:]
		}
	}
	return
}

func parseExp(ctx *context.Context, tokens []string) (c *Constraint, rest []string, err error) {
	if len(tokens) == 0 {
		return
	}
	rest = tokens
	c, rest, err = parseOperand(ctx, rest)
	if err != nil {
		return
	}
	for len(rest) > 0 {
		switch rest[0] {
		case "and":
			c, rest, err = parseConjunction(ctx, c, rest[1:])
			if err != nil {
				return
			}
			continue
		case "or":
			return parseDisjunction(ctx, c, rest[1:])
		case ")":
			return
		}
		c, rest, err = parseConjunction(ctx, c, rest)
		if err != nil {
			return
		}
	}
	return
}

func parseGroup(ctx *context.Context, tokens []string) (c *Constraint, rest []string, err error) {
	rest = tokens
	if rest[0] == "(" {
		c, rest, err = parseExp(ctx, rest[1:])
		if err != nil {
			return
		}
		if len(rest) > 0 && rest[0] == ")" {
			rest = rest[1:]
		} else {
			err = errNoMatchingClosing
			return
		}
	} else {
		err = errNoMatchingOpening
		return
	}
	return
}

func parseDisjunction(ctx *context.Context, lhs *Constraint, tokens []string) (c *Constraint, rest []string, err error) {
	var rhs *Constraint
	c = lhs
	rest = tokens
	for {
		rhs, rest, err = parseEntireConjunction(ctx, rest)
		if err != nil {
			return
		}
		c = orConst(c, rhs)
		if len(rest) > 0 {
			switch rest[0] {
			case "or":
				rest = rest[1:]
				continue
			case "and", ")":
				return
			}
			return
		} else {
			return
		}
	}
	return
}

func parseEntireConjunction(ctx *context.Context, tokens []string) (c *Constraint, rest []string, err error) {
	rest = tokens
	for {
		c, rest, err = parseOperand(ctx, rest)
		if err != nil {
			return
		}
		if len(rest) > 0 {
			switch rest[0] {
			case "and":
				return parseConjunction(ctx, c, rest[1:])
			case ")", "or":
				return
			}
			return parseConjunction(ctx, c, rest)
		} else {
			return
		}
	}
	return
}

func parseConjunction(ctx *context.Context, lhs *Constraint, tokens []string) (c *Constraint, rest []string, err error) {
	var rhs *Constraint
	c = lhs
	rest = tokens
	for {
		rhs, rest, err = parseOperand(ctx, rest)
		if err != nil {
			return
		}
		c = andConst(c, rhs)
		if len(rest) > 0 {
			switch rest[0] {
			case "or", ")":
				return
			case "and":
				rest = rest[1:]
				continue
			}
		} else {
			return
		}
	}
	return
}

func parseOperand(ctx *context.Context, tokens []string) (c *Constraint, rest []string, err error) {
	var negated bool
	negated, rest = stripNot(tokens)
	if len(rest) > 0 {
		if rest[0] == "(" {
			c, rest, err = parseGroup(ctx, rest)
			if err != nil {
				return
			}
		} else {
			switch rest[0] {
			case "and", "or":
				err = errCannotStartBinaryOp
				return
			case ")":
				err = errNoMatchingOpening
				return
			}
			c, err = parseAtom(ctx, rest[0])
			if err != nil {
				return
			}
			rest = rest[1:]
		}
	} else {
		return nil, nil, errExpectedAtom
	}
	if negated {
		c = notConst(c)
	}
	return
}

func permOfFile(fc *FileConstraint) *Constraint {
	return &Constraint{
		Permanode: &PermanodeConstraint{
			Attr:       "camliContent",
			ValueInSet: &Constraint{File: fc},
		},
	}
}

func whRatio(fc *FloatConstraint) *Constraint {
	return permOfFile(&FileConstraint{
		IsImage: true,
		WHRatio: fc,
	})
}

func parseWHExpression(expr string) (min, max string, err error) {
	if m := whRangeExpr.FindStringSubmatch(expr); m != nil {
		return m[1], m[2], nil
	}
	if m := whValueExpr.FindStringSubmatch(expr); m != nil {
		return m[1], m[1], nil
	}
	return "", "", errors.New("bogus range or value")
}

func parseImageAtom(ctx *context.Context, word string) (*Constraint, error) {
	if word == "is:image" {
		c := &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
					},
				},
			},
		}
		return c, nil
	}
	if word == "is:landscape" {
		return whRatio(&FloatConstraint{Min: 1.0}), nil
	}
	if word == "is:portrait" {
		return whRatio(&FloatConstraint{Max: 1.0}), nil
	}
	if word == "is:pano" {
		return whRatio(&FloatConstraint{Min: 2.0}), nil
	}
	if strings.HasPrefix(word, "width:") {
		mins, maxs, err := parseWHExpression(strings.TrimPrefix(word, "width:"))
		if err != nil {
			return nil, err
		}
		c := permOfFile(&FileConstraint{
			IsImage: true,
			Width:   whIntConstraint(mins, maxs),
		})
		return c, nil
	}
	if strings.HasPrefix(word, "height:") {
		mins, maxs, err := parseWHExpression(strings.TrimPrefix(word, "height:"))
		if err != nil {
			return nil, err
		}
		c := permOfFile(&FileConstraint{
			IsImage: true,
			Height:  whIntConstraint(mins, maxs),
		})
		return c, nil
	}
	return nil, errors.New(fmt.Sprintf("Not an image-atom: %v", word))
}

func parseCoreAtom(ctx *context.Context, word string) (*Constraint, error) {
	if m := tagExpr.FindStringSubmatch(word); m != nil {
		c := &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       "tag",
				SkipHidden: true,
				Value:      m[1],
			},
		}
		return c, nil
	}
	if m := titleExpr.FindStringSubmatch(word); m != nil {
		c := &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       "title",
				SkipHidden: true,
				ValueMatches: &StringConstraint{
					Contains:        m[1],
					CaseInsensitive: true,
				},
			},
		}
		return c, nil
	}
	if m := attrExpr.FindStringSubmatch(word); m != nil {
		c := &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       m[1],
				SkipHidden: true,
				Value:      m[2],
			},
		}
		return c, nil
	}
	if m := childrenOfExpr.FindStringSubmatch(word); m != nil {
		c := &Constraint{
			Permanode: &PermanodeConstraint{
				Relation: &RelationConstraint{
					Relation: "parent",
					Any: &Constraint{
						BlobRefPrefix: m[1],
					},
				},
			},
		}
		return c, nil
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
		c := &Constraint{
			Permanode: &PermanodeConstraint{
				Time: tc,
			},
		}
		return c, nil
	}
	if strings.HasPrefix(word, "format:") {
		c := permOfFile(&FileConstraint{
			MIMEType: &StringConstraint{
				Equals: mimeFromFormat(strings.TrimPrefix(word, "format:")),
			},
		})
		return c, nil
	}
	return nil, errors.New(fmt.Sprintf("Not an core-atom: %v", word))
}

func parseLocationAtom(ctx *context.Context, word string) (*Constraint, error) {
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
		return locConstraint, nil
	}
	if word == "has:location" {
		c := permOfFile(&FileConstraint{
			IsImage: true,
			Location: &LocationConstraint{
				Any: true,
			},
		})
		return c, nil
	}

	return nil, errors.New(fmt.Sprintf("Not an location-atom: %v", word))
}

func parseAtom(ctx *context.Context, word string) (*Constraint, error) {
	c, err := parseCoreAtom(ctx, word)
	if err == nil {
		return c, nil
	}
	c, err = parseImageAtom(ctx, word)
	if err == nil {
		return c, nil
	}
	c, err = parseLocationAtom(ctx, word)
	if err == nil {
		return c, nil
	}
	log.Printf("Unknown search expression word %q", word)
	return nil, errors.New(fmt.Sprintf("Unknown search atom: %s", word))
}

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

	words := splitExpr(exp)
	c, rem, err := parseExp(ctx, words)
	if err != nil {
		return nil, err
	}
	if c != nil {
		sq.Constraint = andConst(base, c)
	}
	if len(rem) > 0 {
		return nil, errors.New("Trailing terms")
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
//    "("
//    ")"
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
	isWordBound := func(r byte) bool {
		if isSpace(r) {
			return true
		}
		switch r {
		case '(', ')', '-':
			return true
		}
		return false
	}
	if s[0] == '-' {
		return "-", s[1:]
	}
	if s[0] == '(' {
		return "(", s[1:]
	}
	if s[0] == ')' {
		return ")", s[1:]
	}
	if strings.HasPrefix(s, "and") && len(s) > 3 && isWordBound(s[3]) {
		return "and", s[3:]
	}
	if strings.HasPrefix(s, "or") && len(s) > 2 && isWordBound(s[2]) {
		return "or", s[2:]
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
		if r == '(' {
			return s[:i], s[i:]
		}
		if r == ')' {
			return s[:i], s[i:]
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

	// Split on space, ), ( tokens and concatenate tokens ending with :
	// Not particularly efficient, though.
	var f []string
	var nextPasted bool
	for _, token := range tokens {
		if token == " " {
			continue
		} else if nextPasted {
			f[len(f)-1] += token
			nextPasted = false
		} else {
			f = append(f, token)
		}
		if strings.HasSuffix(token, ":") {
			nextPasted = true
		}
	}
	return f
}
