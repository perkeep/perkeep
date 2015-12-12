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
	"fmt"
	"log"
	"strconv"
	"strings"

	"golang.org/x/net/context"
)

const seeDocs = "\nSee: https://camlistore.googlesource.com/camlistore/+/master/doc/search-ui.txt"

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
	ctx    context.Context
}

func newParser(exp string, ctx context.Context) parser {
	_, tokens := lex(exp)
	return parser{tokens: tokens, ctx: ctx}
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

func (p *parser) parseExp() (c *Constraint, err error) {
	if p.peek().typ == tokenEOF {
		return
	}
	c, err = p.parseOperand()
	if err != nil {
		return
	}
	for {
		switch p.peek().typ {
		case tokenAnd:
			p.next()
		case tokenOr:
			p.next()
			return p.parseOrRHS(c)
		case tokenClose, tokenEOF:
			return
		}
		c, err = p.parseAndRHS(c)
		if err != nil {
			return
		}
	}
}

func (p *parser) parseGroup() (c *Constraint, err error) {
	i := p.next()
	switch i.typ {
	case tokenOpen:
		c, err = p.parseExp()
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

func (p *parser) parseOrRHS(lhs *Constraint) (c *Constraint, err error) {
	var rhs *Constraint
	c = lhs
	for {
		rhs, err = p.parseAnd()
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

func (p *parser) parseAnd() (c *Constraint, err error) {
	for {
		c, err = p.parseOperand()
		if err != nil {
			return
		}
		switch p.peek().typ {
		case tokenAnd:
			p.next()
		case tokenOr, tokenClose, tokenEOF:
			return
		}
		return p.parseAndRHS(c)
	}
}

func (p *parser) parseAndRHS(lhs *Constraint) (c *Constraint, err error) {
	var rhs *Constraint
	c = lhs
	for {
		rhs, err = p.parseOperand()
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

func (p *parser) parseOperand() (c *Constraint, err error) {
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
		c, err = p.parseAtom()
	case tokenOpen:
		c, err = p.parseGroup()
	}
	if err != nil {
		return
	}
	if negated {
		c = notConst(c)
	}
	return
}

// AtomWords returns the parsed atom, the starting position of this
// atom and an error.
func (p *parser) atomWords() (a atom, start int, err error) {
	i := p.peek()
	start = i.start
	a = atom{}
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
		a.predicate = i.val
	}
	for {
		switch p.peek().typ {
		case tokenColon:
			p.next()
			continue
		case tokenArg:
			i := p.next()
			a.args = append(a.args, i.val)
			continue
		case tokenQuotedArg:
			i := p.next()
			var uq string
			uq, err = strconv.Unquote(i.val)
			if err != nil {
				return
			}
			a.args = append(a.args, uq)
			continue
		}
		return
	}
}

func (p *parser) parseAtom() (*Constraint, error) {
	a, start, err := p.atomWords()
	if err != nil {
		return nil, err
	}
	faultToken := func() token {
		return token{
			typ:   tokenError,
			val:   a.String(),
			start: start,
		}
	}
	var c *Constraint
	for _, k := range keywords {
		matched, err := k.Match(a)
		if err != nil {
			return nil, newParseExpError(err.Error(), faultToken())
		}
		if matched {
			c, err = k.Predicate(p.ctx, a.args)
			if err != nil {
				return nil, newParseExpError(err.Error(), faultToken())
			}
			return c, nil
		}
	}
	t := faultToken()
	err = newParseExpError(fmt.Sprintf("Unknown search predicate: %q", t.val), t)
	log.Printf(err.Error())
	return nil, err
}

func parseExpression(ctx context.Context, exp string) (*SearchQuery, error) {
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
	p := newParser(exp, ctx)

	c, err := p.parseExp()
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
