package search

import (
	"reflect"
	"testing"
)

const scaryQuote = `"\"Hi there\""`

var lexerTests = []struct {
	in   string
	want []token
}{
	{
		in: "width:++1",
		want: []token{
			{tokenPredicate, "width", 0},
			{tokenColon, ":", 5},
			{tokenArg, "++1", 6},
		},
	},

	{
		in: "and and and",
		want: []token{
			{tokenLiteral, "and", 0},
			{tokenAnd, "and", 4},
			{tokenLiteral, "and", 8},
		},
	},

	{
		in: "and nd and",
		want: []token{
			{tokenLiteral, "and", 0},
			{tokenLiteral, "nd", 4},
			{tokenLiteral, "and", 7},
		},
	},

	{
		in: "or or or",
		want: []token{
			{tokenLiteral, "or", 0},
			{tokenOr, "or", 3},
			{tokenLiteral, "or", 6},
		},
	},

	{
		in: "or r or",
		want: []token{
			{tokenLiteral, "or", 0},
			{tokenLiteral, "r", 3},
			{tokenLiteral, "or", 5},
		},
	},

	{
		in: "(or or or) and or",
		want: []token{
			{tokenOpen, "(", 0},
			{tokenLiteral, "or", 1},
			{tokenOr, "or", 4},
			{tokenLiteral, "or", 7},
			{tokenClose, ")", 9},
			{tokenAnd, "and", 11},
			{tokenLiteral, "or", 15},
		},
	},

	{
		in: `(or or "or) and or`,
		want: []token{
			{tokenOpen, "(", 0},
			{tokenLiteral, "or", 1},
			{tokenOr, "or", 4},
			{tokenError, "Unclosed quote", 7},
		},
	},

	{
		in:   "bar and baz",
		want: []token{{tokenLiteral, "bar", 0}, {tokenAnd, "and", 4}, {tokenLiteral, "baz", 8}},
	},

	{
		in:   "foo or bar",
		want: []token{{tokenLiteral, "foo", 0}, {tokenOr, "or", 4}, {tokenLiteral, "bar", 7}},
	},

	{
		in:   "foo or (bar )",
		want: []token{{tokenLiteral, "foo", 0}, {tokenOr, "or", 4}, {tokenOpen, "(", 7}, {tokenLiteral, "bar", 8}, {tokenClose, ")", 12}},
	},

	{
		in: "foo or bar:foo:baz",
		want: []token{
			{tokenLiteral, "foo", 0},
			{tokenOr, "or", 4},
			{tokenPredicate, "bar", 7},
			{tokenColon, ":", 10},
			{tokenArg, "foo", 11},
			{tokenColon, ":", 14},
			{tokenArg, "baz", 15},
		},
	},

	{
		in: "--foo or - bar",
		want: []token{
			{tokenNot, "-", 0},
			{tokenNot, "-", 1},
			{tokenLiteral, "foo", 2},
			{tokenOr, "or", 6},
			{tokenNot, "-", 9},
			{tokenLiteral, "bar", 11},
		},
	},

	{
		in: "foo:bar:baz or bar",
		want: []token{
			{tokenPredicate, "foo", 0},
			{tokenColon, ":", 3},
			{tokenArg, "bar", 4},
			{tokenColon, ":", 7},
			{tokenArg, "baz", 8},
			{tokenOr, "or", 12},
			{tokenLiteral, "bar", 15},
		},
	},

	{
		in: "is:pano or",
		want: []token{
			{tokenPredicate, "is", 0},
			{tokenColon, ":", 2},
			{tokenArg, "pano", 3},
			{tokenLiteral, "or", 8},
		},
	},

	{
		in: "foo:" + scaryQuote + " or bar",
		want: []token{
			{tokenPredicate, "foo", 0},
			{tokenColon, ":", 3},
			{tokenQuotedArg, scaryQuote, 4},
			{tokenOr, "or", 19},
			{tokenLiteral, "bar", 22},
		},
	},

	{
		in: scaryQuote,
		want: []token{
			{tokenQuotedLiteral, scaryQuote, 0}},
	},

	{
		in: "foo:",
		want: []token{
			{tokenPredicate, "foo", 0},
			{tokenColon, ":", 3},
			{tokenArg, "", 4},
		},
	},
}

func array(in string) (parsed []token) {
	_, tokens := lex(in)
	for token := range tokens {
		if token.typ == tokenEOF {
			break
		}
		parsed = append(parsed, token)
	}
	return
}

func TestLex(t *testing.T) {
	for _, tt := range lexerTests {

		tokens := array(tt.in)
		if !reflect.DeepEqual(tokens, tt.want) {
			t.Errorf("Got lex(%q)=%v expected %v", tt.in, tokens, tt.want)
		}
	}
}
