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

	"camlistore.org/pkg/context"
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

var parseImageAtomTests = []struct {
	name        string
	in          string
	want        *Constraint
	errContains string
}{
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
}

func TestParseImageAtom(t *testing.T) {
	cj := func(c *Constraint) []byte {
		v, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			panic(err)
		}
		return v
	}
	for _, tt := range parseImageAtomTests {
		in := tt.in
		got, err := parseAtom(context.TODO(), in)
		if err != nil {
			if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
				continue
			}
			t.Errorf("%v: parseImageAtom(%q) error: %v", tt.name, in, err)
			continue
		}
		if tt.errContains != "" {
			t.Errorf("%v: parseImageAtom(%q) succeeded; want error containing %q", tt.name, in, tt.errContains)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%v: parseImageAtom(%q) got:\n%s\n\nwant:%s\n", tt.name, in, cj(got), cj(tt.want))
		}
	}
}

var parseLocationAtomTests = []struct {
	name        string
	in          string
	want        *Constraint
	errContains string
}{
	{
		in: "has:location",
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{

					File: &FileConstraint{
						IsImage: true,
						Location: &LocationConstraint{
							Any: true,
						},
					},
				},
			},
		},
	},
}

func TestParseLocationAtom(t *testing.T) {
	cj := func(c *Constraint) []byte {
		v, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			panic(err)
		}
		return v
	}
	for _, tt := range parseLocationAtomTests {
		in := tt.in
		got, err := parseAtom(context.TODO(), in)
		if err != nil {
			if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
				continue
			}
			t.Errorf("%v: parseLocationAtom(%q) error: %v", tt.name, in, err)
			continue
		}
		if tt.errContains != "" {
			t.Errorf("%v: parseLocationAtom(%q) succeeded; want error containing %q", tt.name, in, tt.errContains)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%v: parseLocationAtom(%q) got:\n%s\n\nwant:%s\n", tt.name, in, cj(got), cj(tt.want))
		}
	}
}

var parseCoreAtomTests = []struct {
	name        string
	in          string
	want        *Constraint
	errContains string
}{
	{
		name: "tag with spaces",
		in:   `tag:Foo Bar`,
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
		in:   `attr:foo:fun bar`,
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
}

func TestParseCoreAtom(t *testing.T) {
	cj := func(c *Constraint) []byte {
		v, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			panic(err)
		}
		return v
	}
	for _, tt := range parseCoreAtomTests {
		in := tt.in
		got, err := parseAtom(context.TODO(), in)
		if err != nil {
			if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
				continue
			}
			t.Errorf("%v: parseCoreAtom(%q) error: %v", tt.name, in, err)
			continue
		}
		if tt.errContains != "" {
			t.Errorf("%v: parseCoreAtom(%q) succeeded; want error containing %q", tt.name, in, tt.errContains)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%v: parseCoreAtom(%q) got:\n%s\n\nwant:%s\n", tt.name, in, cj(got), cj(tt.want))
		}
	}
}

var parseAtomTests = []struct {
	name        string
	in          string
	want        *Constraint
	errContains string
}{
	{
		in:   "is:pano",
		want: ispanoC,
	},

	{
		in:          "faulty:predicate",
		errContains: "atom",
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
		name: "tag with spaces",
		in:   `tag:Foo Bar`,
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
		in:   `attr:foo:fun bar`,
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
}

func TestParseAtom(t *testing.T) {
	cj := func(c *Constraint) []byte {
		v, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			panic(err)
		}
		return v
	}
	for _, tt := range parseAtomTests {
		in := tt.in
		got, err := parseAtom(context.TODO(), in)
		if err != nil {
			if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
				continue
			}
			t.Errorf("%v: parseAtom(%q) error: %v", tt.name, in, err)
			continue
		}
		if tt.errContains != "" {
			t.Errorf("%v: parseAtom(%q) succeeded; want error containing %q", tt.name, in, tt.errContains)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%v: parseAtom(%q) got:\n%s\n\nwant:%s\n", tt.name, in, cj(got), cj(tt.want))
		}
	}
}

var parseExprTests = []struct {
	name        string
	in          string
	inList      []string
	want        *SearchQuery
	errContains string
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
			panic(err)
		}
		return v
	}
	for _, tt := range parseExprTests {
		ins := tt.inList
		if len(ins) == 0 {
			ins = []string{tt.in}
		}
		for _, in := range ins {
			got, err := parseExpression(context.TODO(), in)
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

var parseDisjunctionTests = []struct {
	name        string
	left        int
	tokens      []string
	lhs         *Constraint
	want        *Constraint
	remCount    int
	errContains string
}{
	{
		name:     "stop on )",
		tokens:   []string{"is:pano", ")"},
		want:     orConst(nil, ispanoC),
		remCount: 1,
	},

	{
		tokens:   []string{"is:pano", "and", "attr:foo:bar"},
		want:     orConst(nil, andConst(ispanoC, attrfoobarC)),
		remCount: 0,
	},

	{
		name:     "add atom",
		tokens:   []string{"is:pano"},
		want:     orConst(nil, ispanoC),
		remCount: 0,
	},
}

func TestParseDisjunction(t *testing.T) {
	cj := func(c *Constraint) []byte {
		v, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			panic(err)
		}
		return v
	}
	for _, tt := range parseDisjunctionTests {
		in := tt.tokens
		got, rem, err := parseDisjunction(context.TODO(), tt.lhs, in)
		if err != nil {
			if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
				continue
			}
			t.Errorf("parseDisjunction(%q) error: %v", in, err)
			continue
		}
		if tt.errContains != "" {
			t.Errorf("%s: parseDisjunction(%q) succeeded; want error containing %q got: %s", tt.name, in, tt.errContains, cj(got))
			continue
		}
		if len(rem) != tt.remCount {
			t.Errorf("%s: parseGroup(%q): expected remainder of length %d  got %d (remainder: %s)\n", tt.name, in, tt.remCount, len(rem), rem)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: parseDisjunction(%q) got:\n%s\n\nwant:%s\n", tt.name, in, cj(got), cj(tt.want))
		}
	}
}

var parseConjunctionTests = []struct {
	name        string
	left        int
	tokens      []string
	lhs         *Constraint
	want        *Constraint
	remCount    int
	errContains string
}{
	{
		name:     "stop on )",
		tokens:   []string{"is:pano", ")"},
		want:     andConst(nil, ispanoC),
		remCount: 1,
	},

	{
		name:     "stop on or",
		tokens:   []string{"is:pano", "or"},
		want:     andConst(nil, ispanoC),
		remCount: 1,
	},

	{
		name:     "add atom",
		tokens:   []string{"is:pano"},
		want:     andConst(nil, ispanoC),
		remCount: 0,
	},
}

func TestParseConjuction(t *testing.T) {
	cj := func(c *Constraint) []byte {
		v, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			panic(err)
		}
		return v
	}
	for _, tt := range parseConjunctionTests {
		in := tt.tokens
		got, rem, err := parseConjunction(context.TODO(), tt.lhs, in)
		if err != nil {
			if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
				continue
			}
			t.Errorf("parseConjunction(%q) error: %v", in, err)
			continue
		}
		if tt.errContains != "" {
			t.Errorf("%s: parseConjunction(%q) succeeded; want error containing %q got: %s", tt.name, in, tt.errContains, cj(got))
			continue
		}
		if len(rem) != tt.remCount {
			t.Errorf("%s: parseGroup(%q): expected remainder of length %d  got %d (remainder: %s)\n", tt.name, in, tt.remCount, len(rem), rem)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: parseConjunction(%q) got:\n%s\n\nwant:%s\n", tt.name, in, cj(got), cj(tt.want))
		}
	}
}

var parseGroupTests = []struct {
	name        string
	left        int
	tokens      []string
	want        *Constraint
	remCount    int
	errContains string
}{
	{
		name:     "simple grouped atom",
		tokens:   []string{"(", "is:pano", ")"},
		want:     ispanoC,
		remCount: 0,
	},

	{
		name:     "simple grouped or with remainder",
		tokens:   []string{"(", "attr:foo:bar", "or", "is:pano", ")", "attr:foo:bar"},
		want:     orConst(attrfoobarC, ispanoC),
		remCount: 1,
	},

	{
		name:     "simple grouped and with remainder",
		tokens:   []string{"(", "attr:foo:bar", "is:pano", ")", "attr:foo:bar"},
		want:     andConst(attrfoobarC, ispanoC),
		remCount: 1,
	},

	{
		name:     "simple grouped atom with remainder",
		tokens:   []string{"(", "is:pano", ")", "attr:foo:bar"},
		want:     ispanoC,
		remCount: 1,
	},
}

func TestParseGroup(t *testing.T) {
	cj := func(c *Constraint) []byte {
		v, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			panic(err)
		}
		return v
	}
	for _, tt := range parseGroupTests {
		in := tt.tokens
		got, rem, err := parseGroup(context.TODO(), in)
		if err != nil {
			if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
				continue
			}
			t.Errorf("parseGroup(%q) error: %v", in, err)
			continue
		}
		if tt.errContains != "" {
			t.Errorf("%s: parseGroup(%q) succeeded; want error containing %q got: %s", tt.name, in, tt.errContains, cj(got))
			continue
		}
		if len(rem) != tt.remCount {
			t.Errorf("%s: parseGroup(%q): expected remainder of length %d  got %d (remainder: %s)\n", tt.name, in, tt.remCount, len(rem), rem)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: parseGroup(%q) got:\n%s\n\nwant:%s\n", tt.name, in, cj(got), cj(tt.want))
		}
	}
}

var parseOperandTests = []struct {
	name        string
	left        int
	tokens      []string
	want        *Constraint
	remCount    int
	errContains string
}{
	{
		name:     "group of one atom",
		tokens:   []string{"(", "is:pano", ")"},
		want:     ispanoC,
		remCount: 0,
	},

	{
		name:     "one atom",
		tokens:   []string{"is:pano"},
		want:     ispanoC,
		remCount: 0,
	},

	{
		name:     "two atoms",
		tokens:   []string{"is:pano", "attr:foo:bar"},
		want:     ispanoC,
		remCount: 1,
	},

	{
		name:     "grouped atom and atom",
		tokens:   []string{"(", "is:pano", ")", "attr:foo:bar"},
		want:     ispanoC,
		remCount: 1,
	},

	{
		name:     "atom and )",
		tokens:   []string{"is:pano", ")"},
		want:     ispanoC,
		remCount: 1,
	},
}

func TestParseOperand(t *testing.T) {
	cj := func(c *Constraint) []byte {
		v, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			panic(err)
		}
		return v
	}
	for _, tt := range parseOperandTests {
		in := tt.tokens
		got, rem, err := parseOperand(context.TODO(), in)
		if err != nil {
			if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
				continue
			}
			t.Errorf("parseOperand(%q) error: %v", in, err)
			continue
		}
		if tt.errContains != "" {
			t.Errorf("%s: parseOperand(%q) succeeded; want error containing %q got: %s", tt.name, in, tt.errContains, cj(got))
			continue
		}
		if len(rem) != tt.remCount {
			t.Errorf("%s: parseGroup(%q): expected remainder of length %d  got %d (remainder: %s)\n", tt.name, in, tt.remCount, len(rem), rem)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: parseOperand(%q) got:\n%s\n\nwant:%s\n", tt.name, in, cj(got), cj(tt.want))
		}
	}
}

var parseTests = []struct {
	name        string
	left        int
	tokens      []string
	want        *Constraint
	remCount    int
	errContains string
}{
	{
		name:        "Unmatched (",
		tokens:      []string{"("},
		errContains: "No matching closing parenthesis",
	},

	{
		name:        "Unmatched )",
		tokens:      []string{")"},
		errContains: "No matching opening parenthesis",
	},

	{
		name:     "Unmatched ) at the end ",
		tokens:   []string{"is:pano", "or", "attr:foo:bar", ")"},
		want:     orConst(ispanoC, attrfoobarC),
		remCount: 1,
	},

	{
		name:   "empty search",
		tokens: []string{},
		want:   nil,
	},

	{
		name:        "faulty negation in 'or'",
		tokens:      []string{"is:pano", "-", "or", "-", "is:pano"},
		errContains: "Expression cannot start with a binary operator",
	},

	{
		name:        "faulty negation in 'or'",
		tokens:      []string{"is:pano", "or", "-"},
		errContains: "an atom",
	},

	{
		name:        "faulty disjunction, empty right",
		tokens:      []string{"is:pano", "or"},
		errContains: "an atom",
	},

	{
		name:        "faulty disjunction",
		tokens:      []string{"or", "is:pano"},
		errContains: "Expression cannot start with a binary operator",
	},

	{
		name:        "faulty conjunction",
		tokens:      []string{"and", "is:pano"},
		errContains: "Expression cannot start with a binary operator",
	},

	{
		name:   "one atom",
		tokens: []string{"is:pano"},
		want:   ispanoC,
	},

	{
		name:   "negated atom",
		tokens: []string{"-", "is:pano"},
		want:   notConst(ispanoC),
	},

	{
		name:   "double negated atom",
		tokens: []string{"-", "-", "is:pano"},
		want:   ispanoC,
	},

	{
		name:   "parenthesized atom with implicit 'and' and other atom",
		tokens: []string{"(", "is:pano", ")", "attr:foo:bar"},
		want:   andConst(ispanoC, attrfoobarC),
	},

	{
		name:   "negated  implicit 'and'",
		tokens: []string{"-", "(", "is:pano", "attr:foo:bar", ")"},
		want:   notConst(andConst(ispanoC, attrfoobarC)),
	},

	{
		name:   "negated  implicit 'and' with trailing attr:go:run",
		tokens: []string{"-", "(", "is:pano", "attr:foo:bar", ")", "attr:go:run"},
		want:   andConst(notConst(andConst(ispanoC, attrfoobarC)), attrgorunC),
	},

	{
		name:   "parenthesized implicit 'and'",
		tokens: []string{"(", "is:pano", "attr:foo:bar", ")"},
		want:   andConst(ispanoC, attrfoobarC),
	},

	{
		name:   "simple 'or' of two atoms",
		tokens: []string{"is:pano", "or", "attr:foo:bar"},
		want:   orConst(ispanoC, attrfoobarC),
	},

	{
		name:   "left associativity of implicit 'and'",
		tokens: []string{"is:pano", "attr:go:run", "attr:foo:bar"},
		want:   andConst(andConst(ispanoC, attrgorunC), attrfoobarC),
	},

	{
		name:   "left associativity of explicit 'and'",
		tokens: []string{"is:pano", "and", "attr:go:run", "and", "attr:foo:bar"},
		want:   andConst(andConst(ispanoC, attrgorunC), attrfoobarC),
	},

	{
		name:   "left associativity of 'or'",
		tokens: []string{"is:pano", "or", "attr:go:run", "or", "attr:foo:bar"},
		want:   orConst(orConst(ispanoC, attrgorunC), attrfoobarC)},

	{
		name:   "left associativity of 'or' with negated atom",
		tokens: []string{"is:pano", "or", "-", "attr:go:run", "or", "attr:foo:bar"},
		want:   orConst(orConst(ispanoC, notConst(attrgorunC)), attrfoobarC),
	},

	{
		name:   "left associativity of 'or' with double negated atom",
		tokens: []string{"is:pano", "or", "-", "-", "attr:go:run", "or", "attr:foo:bar"},
		want:   orConst(orConst(ispanoC, attrgorunC), attrfoobarC),
	},

	{
		name:   "left associativity of 'or' with parenthesized subexpression",
		tokens: []string{"is:pano", "or", "(", "-", "attr:go:run", ")", "or", "attr:foo:bar"},
		want:   orConst(orConst(ispanoC, notConst(attrgorunC)), attrfoobarC),
	},

	{
		name:   "explicit 'and' of two atoms",
		tokens: []string{"is:pano", "and", "attr:foo:bar"},
		want:   andConst(ispanoC, attrfoobarC),
	},

	{
		name:   "implicit 'and' of two atom",
		tokens: []string{"is:pano", "attr:foo:bar"},
		want:   andConst(ispanoC, attrfoobarC),
	},

	{
		name:   "grouping an 'and' in an 'or'",
		tokens: []string{"is:pano", "or", "(", "attr:foo:bar", "attr:go:run", ")"},
		want:   orConst(ispanoC, andConst(attrfoobarC, attrgorunC)),
	},

	{
		name:   "precedence of 'and' over 'or'",
		tokens: []string{"is:pano", "or", "attr:foo:bar", "and", "attr:go:run"},
		want:   orConst(ispanoC, andConst(attrfoobarC, attrgorunC)),
	},

	{
		name:   "precedence of 'and' over 'or' with 'and' on the left",
		tokens: []string{"is:pano", "and", "attr:foo:bar", "or", "attr:go:run"},
		want:   orConst(andConst(ispanoC, attrfoobarC), attrgorunC),
	},

	{
		name:   "precedence of 'and' over 'or' with 'and' on the left and right",
		tokens: []string{"is:pano", "and", "attr:foo:bar", "or", "attr:go:run", "is:pano"},
		want:   orConst(andConst(ispanoC, attrfoobarC), andConst(attrgorunC, ispanoC)),
	},

	{
		name:   "precedence of 'and' over 'or' with 'and' on the left and right with a negation",
		tokens: []string{"is:pano", "and", "attr:foo:bar", "or", "-", "attr:go:run", "is:pano"},
		want:   orConst(andConst(ispanoC, attrfoobarC), andConst(notConst(attrgorunC), ispanoC)),
	},

	{
		name:   "precedence of 'and' over 'or' with 'and' on the left and right with a negation of group and trailing 'and'",
		tokens: []string{"is:pano", "and", "attr:foo:bar", "or", "-", "(", "attr:go:run", "is:pano", ")", "is:pano"},
		want:   orConst(andConst(ispanoC, attrfoobarC), andConst(notConst(andConst(attrgorunC, ispanoC)), ispanoC)),
	},

	{
		name:   "complicated",
		tokens: []string{"-", "(", "is:pano", "and", "attr:foo:bar", ")", "or", "-", "(", "attr:go:run", "is:pano", ")", "is:pano"},
		want:   orConst(notConst(andConst(ispanoC, attrfoobarC)), andConst(notConst(andConst(attrgorunC, ispanoC)), ispanoC)),
	},

	{
		name:   "complicated",
		tokens: []string{"is:pano", "or", "attr:foo:bar", "attr:go:run", "or", "-", "attr:go:run", "or", "is:pano", "is:pano"},
		want:   orConst(orConst(orConst(ispanoC, andConst(attrfoobarC, attrgorunC)), notConst(attrgorunC)), andConst(ispanoC, ispanoC)),
	},

	{
		name:   "complicated",
		tokens: []string{"is:pano", "or", "attr:foo:bar", "attr:go:run", "or", "-", "attr:go:run", "or", "is:pano", "is:pano", "or", "attr:foo:bar"},
		want:   orConst(orConst(orConst(orConst(ispanoC, andConst(attrfoobarC, attrgorunC)), notConst(attrgorunC)), andConst(ispanoC, ispanoC)), attrfoobarC),
	},
}

func TestParse(t *testing.T) {
	cj := func(c *Constraint) []byte {
		v, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			panic(err)
		}
		return v
	}
	for _, tt := range parseTests {
		in := tt.tokens
		got, rem, err := parseExp(context.TODO(), in)
		if err != nil {
			if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
				continue
			}
			t.Errorf("parse(%q) error: %v", in, err)
			continue
		}
		if tt.errContains != "" {
			t.Errorf("%s: parse(%q) succeeded; want error containing %q got: %s", tt.name, in, tt.errContains, cj(got))
			continue
		}
		if len(rem) != tt.remCount {
			t.Errorf("%s: parseGroup(%q): expected remainder of length %d  got %d (remainder: %s)\n", tt.name, in, tt.remCount, len(rem), rem)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: parse(%q) got:\n%s\n\nwant:%s\n", tt.name, in, cj(got), cj(tt.want))
		}
	}
}

func TestSplitExpr(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"foo", []string{"foo"}},
		{"foo bar", []string{"foo", "bar"}},
		{" foo  bar ", []string{"foo", "bar"}},
		{`foo:"quoted string" bar`, []string{`foo:quoted string`, "bar"}},
		{`foo:"quoted \"-containing"`, []string{`foo:quoted "-containing`}},
		{"foo:bar:foo or bar or (foo or bar)", []string{"foo:bar:foo", "or", "bar", "or", "(", "foo", "or", "bar", ")"}},
		{"-foo:bar:foo", []string{"-", "foo:bar:foo"}},
	}
	for _, tt := range tests {
		got := splitExpr(tt.in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("split(%s) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestTokenizeExpr(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"foo", []string{"foo"}},
		{"andouille and android", []string{"andouille", " ", "and", " ", "android"}},
		{"and(", []string{"and", "("}},
		{"oregon", []string{"oregon"}},
		{"or-", []string{"or", "-"}},
		{")or-", []string{")", "or", "-"}},
		{"foo bar", []string{"foo", " ", "bar"}},
		{" foo  bar ", []string{" ", "foo", " ", "bar", " "}},
		{" -foo  bar", []string{" ", "-", "foo", " ", "bar"}},
		{`-"quote"foo`, []string{"-", `"quote"`, "foo"}},
		{`foo:"quoted string" bar`, []string{"foo:", `"quoted string"`, " ", "bar"}},
		{`"quoted \"-containing"`, []string{`"quoted \"-containing"`}},
		{"foo and bar or foobar", []string{"foo", " ", "and", " ", "bar", " ", "or", " ", "foobar"}},
		{"(foo:bar and bar) or foobar", []string{"(", "foo:", "bar", " ", "and", " ", "bar", ")", " ", "or", " ", "foobar"}},
		{"(foo:bar:foo and bar) or foobar", []string{"(", "foo:", "bar:", "foo", " ", "and", " ", "bar", ")", " ", "or", " ", "foobar"}},
	}
	for _, tt := range tests {
		got := tokenizeExpr(tt.in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("tokens(%s) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestStripNot(t *testing.T) {
	tests := []struct {
		in       []string
		wantNeg  bool
		wantRest []string
	}{
		{[]string{"-", "-", "foo"}, false, []string{"foo"}},
		{[]string{"-", "-", "("}, false, []string{"("}},
		{[]string{"-", "("}, true, []string{"("}},
		{[]string{"foo"}, false, []string{"foo"}},
		{[]string{"-", "-", "-", "foo"}, true, []string{"foo"}},
	}
	for _, tt := range tests {
		gotNeg, gotRest := stripNot(tt.in)
		if !reflect.DeepEqual(gotNeg, tt.wantNeg) {
			t.Errorf("stripNot(%s) = %v; want %v", tt.in, gotNeg, tt.wantNeg)
		}
		if !reflect.DeepEqual(gotRest, tt.wantRest) {
			t.Errorf("stripNot(%s) = %v; want %v", tt.in, gotRest, tt.wantRest)
		}
	}
}
