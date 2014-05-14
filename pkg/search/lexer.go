/*
Copyright 2014 The Camlistore Authors

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

// This is the lexer for search expressions (see expr.go).

package search

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokenType int

const (
	tokenAnd tokenType = iota
	tokenArg
	tokenClose
	tokenColon
	tokenEOF
	tokenError
	tokenLiteral
	tokenNot
	tokenOpen
	tokenOr
	tokenPredicate
	tokenQuotedArg
	tokenQuotedLiteral
)

const (
	eof        = -1 // -1 is unused in utf8
	whitespace = "\t\n\f\v\r "
	opBound    = whitespace + "("
)

// IsSearchWordRune defines the runes that can be used in unquoted predicate arguments
// or unquoted literals. These are all non-space unicode characters except ':' which is
// used for predicate marking,  and '(', ')', which are used for predicate grouping.
func isSearchWordRune(r rune) bool {
	switch r {
	case ':', ')', '(', eof:
		return false
	}
	return !unicode.IsSpace(r)
}

type token struct {
	typ   tokenType
	val   string
	start int
}

func (t token) String() string {
	switch t.typ {
	case tokenEOF:
		return "EOF"
	case tokenError:
		return fmt.Sprintf("{err:%q at pos: %d}", t.val, t.start)
	}
	return fmt.Sprintf("{t:%v,%q (col: %d)}", t.typ, t.val, t.start)
}

type lexer struct {
	input  string
	start  int
	pos    int
	width  int
	tokens chan token
	state  stateFn
}

func (l *lexer) emit(typ tokenType) {
	l.tokens <- token{typ, l.input[l.start:l.pos], l.start}
	l.start = l.pos
}

func (l *lexer) next() (r rune) {
	if l.pos >= len(l.input) {
		l.width = 0
		return eof
	}
	r, l.width = utf8.DecodeRuneInString(l.input[l.pos:])
	l.pos += l.width
	return
}

func (l *lexer) ignore() {
	l.start = l.pos
}

func (l *lexer) backup() {
	l.pos -= l.width
}

func (l *lexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

func (l *lexer) accept(valid string) bool {
	if strings.IndexRune(valid, l.next()) >= 0 {
		return true
	}
	l.backup()
	return false
}

func (l *lexer) acceptString(s string) bool {
	for _, r := range s {
		if l.next() != r {
			l.backup()
			return false
		}
	}
	return true
}

func (l *lexer) acceptRun(valid string) {
	for strings.IndexRune(valid, l.next()) >= 0 {
	}
	l.backup()
}

func (l *lexer) acceptRunFn(valid func(rune) bool) {
	for valid(l.next()) {
	}
	l.backup()
}

func (l *lexer) errorf(format string, args ...interface{}) stateFn {
	l.tokens <- token{
		typ:   tokenError,
		val:   fmt.Sprintf(format, args...),
		start: l.start,
	}
	return nil
}

func lex(input string) (*lexer, chan token) {
	l := &lexer{
		input:  input,
		tokens: make(chan token),
		state:  readExp,
	}
	go l.run()
	return l, l.tokens
}

func (l *lexer) run() {
	for {
		if l.state == nil {
			close(l.tokens)
			return
		}
		l.state = l.state(l)
	}
}

//
// State functions
//
type stateFn func(*lexer) stateFn

func readNeg(l *lexer) stateFn {
	l.accept("-")
	l.emit(tokenNot)
	return readExp
}

func readClose(l *lexer) stateFn {
	l.accept(")")
	l.emit(tokenClose)
	return readOperator
}

func readOpen(l *lexer) stateFn {
	l.accept("(")
	l.emit(tokenOpen)
	return readExp
}

func readColon(l *lexer) stateFn {
	l.accept(":")
	l.emit(tokenColon)
	return readArg
}

func readPredicate(l *lexer) stateFn {
	l.acceptRunFn(unicode.IsLetter)
	switch l.peek() {
	case ':':
		l.emit(tokenPredicate)
		return readColon
	}
	return readLiteral
}

func readLiteral(l *lexer) stateFn {
	l.acceptRunFn(isSearchWordRune)
	l.emit(tokenLiteral)
	return readOperator
}

func readArg(l *lexer) stateFn {
	if l.peek() == '"' {
		return readQuotedArg
	}
	l.acceptRunFn(isSearchWordRune)
	l.emit(tokenArg)
	if l.peek() == ':' {
		return readColon
	}
	return readOperator
}

func readAND(l *lexer) stateFn {
	if l.acceptString("and") && l.accept(opBound) {
		l.backup()
		l.emit(tokenAnd)
		return readExp
	} else {
		return readPredicate
	}
}

func readOR(l *lexer) stateFn {
	if l.acceptString("or") && l.accept(opBound) {
		l.backup()
		l.emit(tokenOr)
		return readExp
	} else {
		return readPredicate
	}
}

func runQuoted(l *lexer) bool {
	l.accept("\"")
	for {
		r := l.next()
		switch r {
		case eof:
			return false
		case '\\':
			l.next()
		case '"':
			return true
		}
	}
}

func readQuotedLiteral(l *lexer) stateFn {
	if !runQuoted(l) {
		return l.errorf("Unclosed quote")
	}
	l.emit(tokenQuotedLiteral)
	return readOperator
}

func readQuotedArg(l *lexer) stateFn {
	if !runQuoted(l) {
		return l.errorf("Unclosed quote")
	}
	l.emit(tokenQuotedArg)
	if l.peek() == ':' {
		return readColon
	}
	return readOperator
}

func readExp(l *lexer) stateFn {
	l.acceptRun(whitespace)
	l.ignore()
	switch l.peek() {
	case eof:
		return nil
	case '(':
		return readOpen
	case ')':
		return readClose
	case '-':
		return readNeg
	case '"':
		return readQuotedLiteral
	}
	return readPredicate
}

func readOperator(l *lexer) stateFn {
	l.acceptRun(whitespace)
	l.ignore()
	switch l.peek() {
	case 'a':
		return readAND
	case 'o':
		return readOR
	}
	return readExp
}
