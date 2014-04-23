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

	"camlistore.org/pkg/context"
	"camlistore.org/pkg/geocode"
	"camlistore.org/pkg/types"
)

const seeDocs = "\nSee: https://camlistore.googlesource.com/camlistore/+/master/doc/search-ui.txt"

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
	noMatchingOpening      = "No matching opening parenthesis"
	noMatchingClosing      = "No matching closing parenthesis"
	noLiteralSupport       = "No support for literals yet"
	noQuotedLiteralSupport = "No support for quoted literals yet"
	expectedAtom           = "Expected an atom"
	predicateError         = "Predicates do not start with a colon"
	trailingTokens         = "After parsing finished there is still input left"
)

type parseExpError struct {
	mesg string
	t    token
}

func (e parseExpError) Error() string {
	return fmt.Sprintf("%s at position %d, token: %q %s", e.mesg, e.t.start, e.t.val, seeDocs)
}

func newParseExpError(mesg string, t token) error {
	return parseExpError{mesg: mesg, t: t}
}

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

type parser struct {
	tokens chan token
	peeked *token
}

func newParser(exp string) parser {
	_, tokens := lex(exp)
	return parser{tokens: tokens}
}

func (p *parser) next() *token {
	if p.peeked != nil {
		t := p.peeked
		p.peeked = nil
		return t
	}
	return p.readInternal()
}

func (p *parser) peek() *token {
	if p.peeked == nil {
		p.peeked = p.readInternal()
	}
	return p.peeked
}

// ReadInternal should not be called directly, use 'next' or 'peek'
func (p *parser) readInternal() *token {
	for t := range p.tokens {
		return &t
	}
	return &token{tokenEOF, "", -1}
}

func (p *parser) stripNot() (negated bool) {
	for {
		switch p.peek().typ {
		case tokenNot:
			p.next()
			negated = !negated
			continue
		}
		return negated
	}
}

func (p *parser) parseExp(ctx *context.Context) (c *Constraint, err error) {
	if p.peek().typ == tokenEOF {
		return
	}
	c, err = p.parseOperand(ctx)
	if err != nil {
		return
	}
	for {
		switch p.peek().typ {
		case tokenAnd:
			p.next()
		case tokenOr:
			p.next()
			return p.parseOrRHS(ctx, c)
		case tokenClose, tokenEOF:
			return
		}
		c, err = p.parseAndRHS(ctx, c)
		if err != nil {
			return
		}
	}
}

func (p *parser) parseGroup(ctx *context.Context) (c *Constraint, err error) {
	i := p.next()
	switch i.typ {
	case tokenOpen:
		c, err = p.parseExp(ctx)
		if err != nil {
			return
		}
		if p.peek().typ == tokenClose {
			p.next()
			return
		} else {
			err = newParseExpError(noMatchingClosing, *i)
			return
		}
	}
	err = newParseExpError("internal: do not call parseGroup when not on a '('", *i)
	return
}

func (p *parser) parseOrRHS(ctx *context.Context, lhs *Constraint) (c *Constraint, err error) {
	var rhs *Constraint
	c = lhs
	for {
		rhs, err = p.parseAnd(ctx)
		if err != nil {
			return
		}
		c = orConst(c, rhs)
		switch p.peek().typ {
		case tokenOr:
			p.next()
		case tokenAnd, tokenClose, tokenEOF:
			return
		}
	}
}

func (p *parser) parseAnd(ctx *context.Context) (c *Constraint, err error) {
	for {
		c, err = p.parseOperand(ctx)
		if err != nil {
			return
		}
		switch p.peek().typ {
		case tokenAnd:
			p.next()
		case tokenOr, tokenClose, tokenEOF:
			return
		}
		return p.parseAndRHS(ctx, c)
	}
}

func (p *parser) parseAndRHS(ctx *context.Context, lhs *Constraint) (c *Constraint, err error) {
	var rhs *Constraint
	c = lhs
	for {
		rhs, err = p.parseOperand(ctx)
		if err != nil {
			return
		}
		c = andConst(c, rhs)
		switch p.peek().typ {
		case tokenOr, tokenClose, tokenEOF:
			return
		case tokenAnd:
			p.next()
			continue
		}
		return
	}
}

func (p *parser) parseOperand(ctx *context.Context) (c *Constraint, err error) {
	negated := p.stripNot()
	i := p.peek()
	switch i.typ {
	case tokenError:
		err = newParseExpError(i.val, *i)
		return
	case tokenEOF:
		err = newParseExpError(expectedAtom, *i)
		return
	case tokenClose:
		err = newParseExpError(noMatchingOpening, *i)
		return
	case tokenLiteral, tokenQuotedLiteral, tokenPredicate, tokenColon, tokenArg:
		c, err = p.parseAtom(ctx)
	case tokenOpen:
		c, err = p.parseGroup(ctx)
	}
	if err != nil {
		return
	}
	if negated {
		c = notConst(c)
	}
	return
}

func (p *parser) atomWord() (word string, err error) {
	i := p.peek()
	switch i.typ {
	case tokenLiteral:
		err = newParseExpError(noLiteralSupport, *i)
		return
	case tokenQuotedLiteral:
		err = newParseExpError(noQuotedLiteralSupport, *i)
		return
	case tokenColon:
		err = newParseExpError(predicateError, *i)
		return
	case tokenPredicate:
		i := p.next()
		word += i.val
	}
	for {
		switch p.peek().typ {
		case tokenColon:
			p.next()
			word += ":"
			continue
		case tokenArg:
			i := p.next()
			word += i.val
			continue
		case tokenQuotedArg:
			i := p.next()
			uq, err := strconv.Unquote(i.val)
			if err != nil {
				return "", err
			}
			word += uq
			continue
		}
		return
	}
}

func (p *parser) parseAtom(ctx *context.Context) (c *Constraint, err error) {
	word, err := p.atomWord()
	if err != nil {
		return
	}
	c, err = parseCoreAtom(ctx, word)
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
	log.Printf("Unknown search predicate %q", word)
	return nil, errors.New(fmt.Sprintf("Unknown search predicate: %q", word))
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
		var c *Constraint
		for i, rect := range rects {
			loc := &LocationConstraint{
				West:  rect.SouthWest.Long,
				East:  rect.NorthEast.Long,
				North: rect.NorthEast.Lat,
				South: rect.SouthWest.Lat,
			}
			fileLoc := permOfFile(&FileConstraint{
				IsImage:  true,
				Location: loc,
			})
			permLoc := &Constraint{
				Permanode: &PermanodeConstraint{
					Location: loc,
				},
			}
			rectConstraint := orConst(fileLoc, permLoc)
			if i == 0 {
				c = rectConstraint
			} else {
				c = orConst(c, rectConstraint)
			}
		}
		return c, nil
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
	_, tokens := lex(exp)
	p := parser{tokens: tokens}

	c, err := p.parseExp(ctx)
	if err != nil {
		return nil, err
	}
	lastToken := p.next()
	if lastToken.typ != tokenEOF {
		switch lastToken.typ {
		case tokenClose:
			return nil, newParseExpError(noMatchingOpening, *lastToken)
		}
		return nil, newParseExpError(trailingTokens, *lastToken)
	}
	if c != nil {
		sq.Constraint = andConst(base, c)
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
