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
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/net/context"
)

var skiphiddenC = &Constraint{
	Permanode: &PermanodeConstraint{
		SkipHidden: true,
	},
}

var ispanoC = &Constraint{
	Permanode: &PermanodeConstraint{
		Attr: "camliContent",
		ValueInSet: &Constraint{
			File: &FileConstraint{
				IsImage: true,
				WHRatio: &FloatConstraint{
					Min: 2.0,
				},
			},
		},
	},
}

var attrfoobarC = &Constraint{
	Permanode: &PermanodeConstraint{
		Attr:       "foo",
		Value:      "bar",
		SkipHidden: true,
	},
}

var attrgorunC = &Constraint{
	Permanode: &PermanodeConstraint{
		Attr:       "go",
		Value:      "run",
		SkipHidden: true,
	},
}

var hasLocationC = orConst(&Constraint{
	Permanode: &PermanodeConstraint{
		Attr: "camliContent",
		ValueInSet: &Constraint{
			File: &FileConstraint{
				IsImage:  true,
				Location: &LocationConstraint{Any: true},
			},
		},
	},
}, &Constraint{
	Permanode: &PermanodeConstraint{
		Location: &LocationConstraint{Any: true},
	},
})

var parseExpressionTests = []struct {
	name        string
	in          string
	inList      []string
	want        *SearchQuery
	errContains string
	ctx         context.Context
}{
	{
		name:   "empty search",
		inList: []string{"", "  ", "\n"},
		want: &SearchQuery{
			Constraint: skiphiddenC,
		},
	},

	{
		in: "is:pano",
		want: &SearchQuery{
			Constraint: andConst(skiphiddenC, ispanoC),
		},
	},

	{
		in:          "is:pano)",
		errContains: "No matching opening",
	},

	{
		in: "width:0-640",
		want: &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "and",
					A:  skiphiddenC,
					B: &Constraint{
						Permanode: &PermanodeConstraint{
							Attr: "camliContent",
							ValueInSet: &Constraint{
								File: &FileConstraint{
									IsImage: true,
									Width: &IntConstraint{
										ZeroMin: true,
										Max:     640,
									},
								},
							},
						},
					},
				},
			},
		},
	},

	{
		name: "tag with spaces",
		in:   `tag:"Foo Bar"`,
		want: &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "and",
					A:  skiphiddenC,
					B: &Constraint{
						Permanode: &PermanodeConstraint{
							Attr:       "tag",
							Value:      "Foo Bar",
							SkipHidden: true,
						},
					},
				},
			},
		},
	},

	{
		name: "attribute search",
		in:   "attr:foo:bar",
		want: &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "and",
					A:  skiphiddenC,
					B: &Constraint{
						Permanode: &PermanodeConstraint{
							Attr:       "foo",
							Value:      "bar",
							SkipHidden: true,
						},
					},
				},
			},
		},
	},

	{
		name: "attribute search with space in value",
		in:   `attr:foo:"fun bar"`,
		want: &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "and",
					A:  skiphiddenC,
					B: &Constraint{
						Permanode: &PermanodeConstraint{
							Attr:       "foo",
							Value:      "fun bar",
							SkipHidden: true,
						},
					},
				},
			},
		},
	},

	{
		in: "tag:funny",
		want: &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "and",
					A:  skiphiddenC,
					B: &Constraint{
						Permanode: &PermanodeConstraint{
							Attr:       "tag",
							Value:      "funny",
							SkipHidden: true,
						},
					},
				},
			},
		},
	},

	{
		in: "title:Doggies",
		want: &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "and",
					A:  skiphiddenC,
					B: &Constraint{
						Permanode: &PermanodeConstraint{
							Attr: "title",
							ValueMatches: &StringConstraint{
								Contains:        "Doggies",
								CaseInsensitive: true,
							},
							SkipHidden: true,
						},
					},
				},
			},
		},
	},

	{
		in: "childrenof:sha1-f00ba4",
		want: &SearchQuery{
			Constraint: &Constraint{
				Logical: &LogicalConstraint{
					Op: "and",
					A:  skiphiddenC,
					B: &Constraint{
						Permanode: &PermanodeConstraint{
							Relation: &RelationConstraint{
								Relation: "parent",
								Any: &Constraint{
									BlobRefPrefix: "sha1-f00ba4",
								},
							},
						},
					},
				},
			},
		},
	},
	// Location predicates
	{
		in: "loc:Uitdam", // Small dutch town
		want: &SearchQuery{
			Constraint: andConst(skiphiddenC, orConst(&Constraint{
				Permanode: &PermanodeConstraint{
					Attr: "camliContent",
					ValueInSet: &Constraint{
						File: &FileConstraint{
							IsImage:  true,
							Location: uitdamLC,
						},
					},
				},
			}, &Constraint{
				Permanode: &PermanodeConstraint{
					Location: uitdamLC,
				},
			})),
		},
		ctx: newGeocodeContext(),
	},

	{
		in: "has:location",
		want: &SearchQuery{
			Constraint: andConst(skiphiddenC, hasLocationC),
		},
	},

	// TODO: at least 'x' will go away eventually.
	/*
		{
			inList:      []string{"x", "bogus:operator"},
			errContains: "unknown expression",
		},
	*/
}

func TestParseExpression(t *testing.T) {
	qj := func(sq *SearchQuery) []byte {
		v, err := json.MarshalIndent(sq, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tt := range parseExpressionTests {
		ins := tt.inList
		if len(ins) == 0 {
			ins = []string{tt.in}
		}
		for _, in := range ins {
			ctx := tt.ctx
			if ctx == nil {
				ctx = context.TODO()
			}
			got, err := parseExpression(ctx, in)
			if err != nil {
				if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
					continue
				}
				t.Errorf("%s: parseExpression(%q) error: %v", tt.name, in, err)
				continue
			}
			if tt.errContains != "" {
				t.Errorf("%s: parseExpression(%q) succeeded; want error containing %q", tt.name, in, tt.errContains)
				continue
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s: parseExpression(%q) got:\n%s\n\nwant:%s\n", tt.name, in, qj(got), qj(tt.want))
			}
		}
	}
}

func doSticherChecking(name string, t *testing.T, tt sticherTestCase, got *Constraint, err error, p parser) {
	ntt := parserTestCase{
		name:        tt.name,
		in:          tt.in,
		want:        tt.want,
		remCount:    tt.remCount,
		errContains: tt.errContains,
	}
	doChecking(name, t, ntt, got, err, p)
}

func doChecking(name string, t *testing.T, tt parserTestCase, got *Constraint, err error, p parser) {
	cj := func(c *Constraint) []byte {
		v, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			panic(err)
		}
		return v
	}
	remain := func() []token {
		var remainder []token
		var i int
		for i = 0; true; i++ {
			token := p.next()
			if token.typ == tokenEOF {
				break
			} else {
				remainder = append(remainder, *token)
			}
		}
		return remainder
	}

	if err != nil {
		if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
			return
		}
		if tt.errContains != "" {
			t.Errorf("%s: %s(%q) error: %v, but wanted an error with: %v", tt.name, name, tt.in, err, tt.errContains)
		} else {
			t.Errorf("%s: %s(%q) unexpected error: %v", tt.name, name, tt.in, err)
		}
		return
	}
	if tt.errContains != "" {
		t.Errorf("%s: %s(%q) succeeded; want error containing %q got: %s", tt.name, name, tt.in, tt.errContains, cj(got))
		return
	}
	if !reflect.DeepEqual(got, tt.want) {
		t.Errorf("%s: %s(%q) got:\n%s\n\nwant:%s\n", tt.name, name, tt.in, cj(got), cj(tt.want))
	}
	remainder := remain()
	if len(remainder) != tt.remCount {
		t.Errorf("%s: %s(%s): Expected remainder of %d got %d\nRemaining tokens: %#v", tt.name, name, tt.in, tt.remCount, len(remainder), remainder)
	}
}

type parserTestCase struct {
	name        string
	in          string
	want        *Constraint
	remCount    int
	errContains string
}

type sticherTestCase struct {
	name        string
	in          string
	want        *Constraint
	remCount    int
	errContains string
	lhs         *Constraint
}

var parseOrRHSTests = []sticherTestCase{
	{
		name:     "stop on )",
		in:       "is:pano )",
		want:     orConst(nil, ispanoC),
		remCount: 1,
	},

	{
		in:       "is:pano and attr:foo:bar",
		want:     orConst(nil, andConst(ispanoC, attrfoobarC)),
		remCount: 0,
	},

	{
		name:     "add atom",
		in:       "is:pano",
		want:     orConst(nil, ispanoC),
		remCount: 0,
	},
}

func TestParseOrRhs(t *testing.T) {
	for _, tt := range parseOrRHSTests {
		p := newParser(tt.in, context.TODO())

		got, err := p.parseOrRHS(tt.lhs)

		doSticherChecking("parseOrRHS", t, tt, got, err, p)
	}
}

var parseAndRHSTests = []sticherTestCase{
	{
		name:     "stop on )",
		in:       "is:pano )",
		want:     andConst(nil, ispanoC),
		remCount: 1,
	},

	{
		name:     "stop on or",
		in:       "is:pano or",
		want:     andConst(nil, ispanoC),
		remCount: 1,
	},

	{
		name:     "add atom",
		in:       "is:pano",
		want:     andConst(nil, ispanoC),
		remCount: 0,
	},
}

func TestParseConjuction(t *testing.T) {
	for _, tt := range parseAndRHSTests {
		p := newParser(tt.in, context.TODO())

		got, err := p.parseAndRHS(tt.lhs)

		doSticherChecking("parseAndRHS", t, tt, got, err, p)
	}
}

var parseGroupTests = []struct {
	name        string
	in          string
	want        *Constraint
	remCount    int
	errContains string
}{
	{
		name:     "simple grouped atom",
		in:       "( is:pano )",
		want:     ispanoC,
		remCount: 0,
	},

	{
		name:     "simple grouped or with remainder",
		in:       "( attr:foo:bar or is:pano ) attr:foo:bar",
		want:     orConst(attrfoobarC, ispanoC),
		remCount: 5,
	},

	{
		name:     "simple grouped and with remainder",
		in:       "( attr:foo:bar is:pano ) attr:foo:bar",
		want:     andConst(attrfoobarC, ispanoC),
		remCount: 5,
	},

	{
		name:     "simple grouped atom with remainder",
		in:       "( is:pano ) attr:foo:bar",
		want:     ispanoC,
		remCount: 5,
	},
}

func TestParseGroup(t *testing.T) {
	for _, tt := range parseGroupTests {
		p := newParser(tt.in, context.TODO())

		got, err := p.parseGroup()

		doChecking("parseGroup", t, tt, got, err, p)
	}
}

var parseOperandTests = []struct {
	name        string
	in          string
	want        *Constraint
	remCount    int
	errContains string
}{
	{
		name:     "group of one atom",
		in:       "( is:pano )",
		want:     ispanoC,
		remCount: 0,
	},

	{
		name:     "one atom",
		in:       "is:pano",
		want:     ispanoC,
		remCount: 0,
	},

	{
		name:     "two atoms",
		in:       "is:pano attr:foo:bar",
		want:     ispanoC,
		remCount: 5,
	},

	{
		name:     "grouped atom and atom",
		in:       "( is:pano ) attr:foo:bar",
		want:     ispanoC,
		remCount: 5,
	},

	{
		name:     "atom and )",
		in:       "is:pano )",
		want:     ispanoC,
		remCount: 1,
	},
}

func TestParseOperand(t *testing.T) {
	for _, tt := range parseOperandTests {
		p := newParser(tt.in, context.TODO())

		got, err := p.parseOperand()

		doChecking("parseOperand", t, tt, got, err, p)
	}
}

var parseExpTests = []parserTestCase{
	{
		in: "attr:foo:",
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:         "foo",
				ValueMatches: &StringConstraint{Empty: true},
				SkipHidden:   true,
			},
		},
	},

	{
		in:          "after:foo",
		errContains: "as \"2006\" at position 0",
	},

	{
		in:          "after:foo:bar",
		errContains: `Wrong number of arguments for "after", given 2, expected 1 at position 0, token: "after:foo:bar"`,
	},

	{
		in:          "     attr:foo",
		errContains: `Wrong number of arguments for "attr", given 1, expected 2 at position 5, token: "attr:foo"`,
	},

	{
		in:   "has:location",
		want: hasLocationC,
	},

	{
		in:   "is:pano",
		want: ispanoC,
	},

	{
		in: "height:0-640",
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						Height: &IntConstraint{
							ZeroMin: true,
							Max:     640,
						},
					},
				},
			},
		},
	},

	{
		in: "width:0-640",
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						Width: &IntConstraint{
							ZeroMin: true,
							Max:     640,
						},
					},
				},
			},
		},
	},

	{
		in:          "height:++0",
		errContains: "Unable to parse \"++0\" as range, wanted something like 480-1024, 480-, -1024 or 1024 at position 0",
	},

	{
		in: "height:480",
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						Height: &IntConstraint{
							Min: 480,
							Max: 480,
						},
					},
				},
			},
		},
	},

	{
		in:          "width:++0",
		errContains: "Unable to parse \"++0\" as range, wanted something like 480-1024, 480-, -1024 or 1024 at position 0",
	},

	{
		in: "width:640",
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						Width: &IntConstraint{
							Min: 640,
							Max: 640,
						},
					},
				},
			},
		},
	},
	{
		name: "tag with spaces",
		in:   `tag:"Foo Bar"`,
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       "tag",
				Value:      "Foo Bar",
				SkipHidden: true,
			},
		},
	},

	{
		name: "attribute search",
		in:   "attr:foo:bar",
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       "foo",
				Value:      "bar",
				SkipHidden: true,
			},
		},
	},

	{
		name: "attribute search with space in value",
		in:   `attr:foo:"fun bar"`,
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       "foo",
				Value:      "fun bar",
				SkipHidden: true,
			},
		},
	},

	{
		in: "tag:funny",
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       "tag",
				Value:      "funny",
				SkipHidden: true,
			},
		},
	},

	{
		in: "title:Doggies",
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "title",
				ValueMatches: &StringConstraint{
					Contains:        "Doggies",
					CaseInsensitive: true,
				},
				SkipHidden: true,
			},
		},
	},

	{
		in: "childrenof:sha1-f00ba4",
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Relation: &RelationConstraint{
					Relation: "parent",
					Any: &Constraint{
						BlobRefPrefix: "sha1-f00ba4",
					},
				},
			},
		},
	},

	{
		name:        "Unmatched quote",
		in:          `is:pano and "foo`,
		errContains: "Unclosed quote at position 12",
	},

	{
		name:        "Unmatched quote",
		in:          `"foo`,
		errContains: "Unclosed quote at position 0",
	},

	{
		name:        "Unmatched (",
		in:          "(",
		errContains: "No matching closing parenthesis at position 0",
	},

	{
		name:        "Unmatched )",
		in:          ")",
		errContains: "No matching opening parenthesis",
	},

	{
		name:     "Unmatched ) at the end ",
		in:       "is:pano or attr:foo:bar )",
		want:     orConst(ispanoC, attrfoobarC),
		remCount: 1,
	},

	{
		name: "empty search",
		in:   "",
		want: nil,
	},

	{
		name:        "faulty negation in 'or'",
		in:          "is:pano - or - is:pano",
		errContains: "at position 10",
	},

	{
		name:        "faulty negation in 'or'",
		in:          "is:pano or -",
		errContains: "an atom",
	},

	{
		name:        "faulty disjunction, empty right",
		in:          "is:pano or",
		errContains: "at position 8",
	},

	{
		name:        "faulty disjunction",
		in:          "or is:pano",
		errContains: "at position 0",
	},

	{
		name:        "faulty conjunction",
		in:          "and is:pano",
		errContains: "at position 0",
	},

	{
		name: "one atom",
		in:   "is:pano",
		want: ispanoC,
	},

	{
		name: "negated atom",
		in:   "- is:pano",
		want: notConst(ispanoC),
	},

	{
		name: "double negated atom",
		in:   "- - is:pano",
		want: ispanoC,
	},

	{
		name: "parenthesized atom with implicit 'and' and other atom",
		in:   "( is:pano ) attr:foo:bar",
		want: andConst(ispanoC, attrfoobarC),
	},

	{
		name: "negated  implicit 'and'",
		in:   "- ( is:pano attr:foo:bar )",
		want: notConst(andConst(ispanoC, attrfoobarC)),
	},

	{
		name: "negated  implicit 'and' with trailing attr:go:run",
		in:   "- ( is:pano attr:foo:bar ) attr:go:run",
		want: andConst(notConst(andConst(ispanoC, attrfoobarC)), attrgorunC),
	},

	{
		name: "parenthesized implicit 'and'",
		in:   "( is:pano attr:foo:bar )",
		want: andConst(ispanoC, attrfoobarC),
	},

	{
		name: "simple 'or' of two atoms",
		in:   "is:pano or attr:foo:bar",
		want: orConst(ispanoC, attrfoobarC),
	},

	{
		name: "left associativity of implicit 'and'",
		in:   "is:pano attr:go:run attr:foo:bar",
		want: andConst(andConst(ispanoC, attrgorunC), attrfoobarC),
	},

	{
		name: "left associativity of explicit 'and'",
		in:   "is:pano and attr:go:run and attr:foo:bar",
		want: andConst(andConst(ispanoC, attrgorunC), attrfoobarC),
	},

	{
		name: "left associativity of 'or'",
		in:   "is:pano or attr:go:run or attr:foo:bar",
		want: orConst(orConst(ispanoC, attrgorunC), attrfoobarC)},

	{
		name: "left associativity of 'or' with negated atom",
		in:   "is:pano or - attr:go:run or attr:foo:bar",
		want: orConst(orConst(ispanoC, notConst(attrgorunC)), attrfoobarC),
	},

	{
		name: "left associativity of 'or' with double negated atom",
		in:   "is:pano or - - attr:go:run or attr:foo:bar",
		want: orConst(orConst(ispanoC, attrgorunC), attrfoobarC),
	},

	{
		name: "left associativity of 'or' with parenthesized subexpression",
		in:   "is:pano or ( - attr:go:run ) or attr:foo:bar",
		want: orConst(orConst(ispanoC, notConst(attrgorunC)), attrfoobarC),
	},

	{
		name: "explicit 'and' of two atoms",
		in:   "is:pano and attr:foo:bar",
		want: andConst(ispanoC, attrfoobarC),
	},

	{
		name: "implicit 'and' of two atom",
		in:   "is:pano attr:foo:bar",
		want: andConst(ispanoC, attrfoobarC),
	},

	{
		name: "grouping an 'and' in an 'or'",
		in:   "is:pano or ( attr:foo:bar attr:go:run )",
		want: orConst(ispanoC, andConst(attrfoobarC, attrgorunC)),
	},

	{
		name: "precedence of 'and' over 'or'",
		in:   "is:pano or attr:foo:bar and attr:go:run",
		want: orConst(ispanoC, andConst(attrfoobarC, attrgorunC)),
	},

	{
		name: "precedence of 'and' over 'or' with 'and' on the left",
		in:   "is:pano and attr:foo:bar or attr:go:run",
		want: orConst(andConst(ispanoC, attrfoobarC), attrgorunC),
	},

	{
		name: "precedence of 'and' over 'or' with 'and' on the left and right",
		in:   "is:pano and attr:foo:bar or attr:go:run is:pano",
		want: orConst(andConst(ispanoC, attrfoobarC), andConst(attrgorunC, ispanoC)),
	},

	{
		name: "precedence of 'and' over 'or' with 'and' on the left and right with a negation",
		in:   "is:pano and attr:foo:bar or - attr:go:run is:pano",
		want: orConst(andConst(ispanoC, attrfoobarC), andConst(notConst(attrgorunC), ispanoC)),
	},

	{
		name: "precedence of 'and' over 'or' with 'and' on the left and right with a negation of group and trailing 'and'",
		in:   "is:pano and attr:foo:bar or - ( attr:go:run is:pano ) is:pano",
		want: orConst(andConst(ispanoC, attrfoobarC), andConst(notConst(andConst(attrgorunC, ispanoC)), ispanoC)),
	},

	{
		name: "complicated",
		in:   "- ( is:pano and attr:foo:bar ) or - ( attr:go:run is:pano ) is:pano",
		want: orConst(notConst(andConst(ispanoC, attrfoobarC)), andConst(notConst(andConst(attrgorunC, ispanoC)), ispanoC)),
	},

	{
		name: "complicated",
		in:   "is:pano or attr:foo:bar attr:go:run or - attr:go:run or is:pano is:pano",
		want: orConst(orConst(orConst(ispanoC, andConst(attrfoobarC, attrgorunC)), notConst(attrgorunC)), andConst(ispanoC, ispanoC)),
	},

	{
		name: "complicated",
		in:   "is:pano or attr:foo:bar attr:go:run or - attr:go:run or is:pano is:pano or attr:foo:bar",
		want: orConst(orConst(orConst(orConst(ispanoC, andConst(attrfoobarC, attrgorunC)), notConst(attrgorunC)), andConst(ispanoC, ispanoC)), attrfoobarC),
	},
}

func TestParseExp(t *testing.T) {
	for _, tt := range parseExpTests {
		p := newParser(tt.in, context.TODO())

		got, err := p.parseExp()

		doChecking("parseExp", t, tt, got, err, p)
	}
}
