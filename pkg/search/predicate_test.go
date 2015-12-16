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

package search

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/types"

	"go4.org/ctxutil"
	"golang.org/x/net/context"
)

func TestSearchHelp(t *testing.T) {
	s := SearchHelp()
	type help struct{ Name, Description string }
	h := []help{}
	err := json.Unmarshal([]byte(s), &h)
	if err != nil {
		t.Fatal(err)
	}
	count := len(keywords)
	if len(h) != count {
		t.Errorf("Expected %d help items, got %d", count, len(h))
	}
}

type keywordTestcase struct {
	name        string
	object      keyword
	args        []string
	want        *Constraint
	errContains string
	ctx         context.Context
}

var uitdamLC = &LocationConstraint{
	North: 52.4486802,
	West:  5.0353014,
	East:  5.094973299999999,
	South: 52.4152441,
}

func newGeocodeContext() context.Context {
	url := "https://maps.googleapis.com/maps/api/geocode/json?address=Uitdam&sensor=false"
	transport := httputil.NewFakeTransport(map[string]func() *http.Response{url: httputil.StaticResponder(uitdamGoogle)})
	return context.WithValue(context.TODO(), ctxutil.HTTPClient, &http.Client{Transport: transport})
}

var uitdamGoogle = `HTTP/1.1 200 OK
Content-Type: application/json; charset=UTF-8
  Date: Tue, 13 May 2014 21:15:01 GMT
  Expires: Wed, 14 May 2014 21:15:01 GMT
  Cache-Control: public, max-age=86400
  Vary: Accept-Language
  Access-Control-Allow-Origin: *
  Server: mafe
  X-XSS-Protection: 1; mode=block
  X-Frame-Options: SAMEORIGIN
  Transfer-Encoding: chunked


{
   "results" : [
      {
         "address_components" : [
            {
               "long_name" : "Uitdam",
               "short_name" : "Uitdam",
               "types" : [ "locality", "political" ]
            },
            {
               "long_name" : "Waterland",
               "short_name" : "Waterland",
               "types" : [ "administrative_area_level_2", "political" ]
            },
            {
               "long_name" : "North Holland",
               "short_name" : "NH",
               "types" : [ "administrative_area_level_1", "political" ]
            },
            {
               "long_name" : "The Netherlands",
               "short_name" : "NL",
               "types" : [ "country", "political" ]
            },
            {
               "long_name" : "1154",
               "short_name" : "1154",
               "types" : [ "postal_code_prefix", "postal_code" ]
            }
         ],
         "formatted_address" : "1154 Uitdam, The Netherlands",
         "geometry" : {
            "bounds" : {
               "northeast" : {
                  "lat" : 52.4486802,
                  "lng" : 5.094973299999999
               },
               "southwest" : {
                  "lat" : 52.4152441,
                  "lng" : 5.0353014
               }
            },
            "location" : {
               "lat" : 52.4210268,
               "lng" : 5.0724962
            },
            "location_type" : "APPROXIMATE",
            "viewport" : {
               "northeast" : {
                  "lat" : 52.4486802,
                  "lng" : 5.094973299999999
               },
               "southwest" : {
                  "lat" : 52.4152441,
                  "lng" : 5.0353014
               }
            }
         },
         "types" : [ "locality", "political" ]
      }
   ],
   "status" : "OK"
}
`
var testtime = time.Date(2013, time.February, 3, 0, 0, 0, 0, time.UTC)

var keywordTests = []keywordTestcase{
	// Core predicates
	{
		object:      newAfter(),
		args:        []string{"faulty"},
		errContains: "faulty",
	},

	{
		object: newAfter(),
		args:   []string{"2013-02-03"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Time: &TimeConstraint{
					After: types.Time3339(testtime),
				},
			},
		},
	},

	{
		object:      newBefore(),
		args:        []string{"faulty"},
		errContains: "faulty",
	},

	{
		object: newBefore(),
		args:   []string{"2013-02-03"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Time: &TimeConstraint{
					Before: types.Time3339(testtime),
				},
			},
		},
	},

	{
		object: newAttribute(),
		args:   []string{"foo", "bar"},
		want:   attrfoobarC,
	},

	{
		object: newAttribute(),
		args:   []string{"foo", ""},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:         "foo",
				ValueMatches: &StringConstraint{Empty: true},
				SkipHidden:   true,
			},
		},
	},

	{
		object: newAttribute(),
		args:   []string{"foo", "~bar"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "foo",
				ValueMatches: &StringConstraint{
					Contains:        "bar",
					CaseInsensitive: true,
				},
				SkipHidden: true,
			},
		},
	},

	{
		object: newChildrenOf(),
		args:   []string{"foo"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Relation: &RelationConstraint{
					Relation: "parent",
					Any: &Constraint{
						BlobRefPrefix: "foo",
					},
				},
			},
		},
	},

	{
		object:      newFormat(),
		args:        []string{"faulty"},
		errContains: "Unknown format: faulty",
	},

	{
		object: newFormat(),
		args:   []string{"pdf"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						MIMEType: &StringConstraint{
							Equals: "application/pdf",
						},
					},
				},
			},
		},
	},

	{
		object: newTag(),
		args:   []string{"foo"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       "tag",
				Value:      "foo",
				SkipHidden: true,
			},
		},
	},

	{
		object: newTag(),
		args:   []string{""},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:         "tag",
				ValueMatches: &StringConstraint{Empty: true},
				SkipHidden:   true,
			},
		},
	},

	{
		object: newTitle(),
		args:   []string{""},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       "title",
				SkipHidden: true,
				ValueMatches: &StringConstraint{
					CaseInsensitive: true,
				},
			}},
	},

	{
		object: newTitle(),
		args:   []string{"foo"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr:       "title",
				SkipHidden: true,
				ValueMatches: &StringConstraint{
					Contains:        "foo",
					CaseInsensitive: true,
				},
			},
		},
	},

	// Image predicates
	{
		object: newIsImage(),
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
					},
				},
			},
		},
	},

	{
		object: newIsPano(),
		want:   ispanoC,
	},

	{
		object: newIsLandscape(),
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						WHRatio: &FloatConstraint{
							Min: 1.0,
						},
					},
				},
			},
		},
	},

	{
		object: newIsPortait(),
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						WHRatio: &FloatConstraint{
							Max: 1.0,
						},
					},
				},
			},
		},
	},

	{
		object:      newWidth(),
		args:        []string{""},
		errContains: "Unable to parse \"\" as range, wanted something like 480-1024, 480-, -1024 or 1024",
	},

	{
		object: newWidth(),
		args:   []string{"100-"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						Width: &IntConstraint{
							Min: 100,
						},
					},
				},
			},
		},
	},

	{
		object: newWidth(),
		args:   []string{"0-200"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						Width: &IntConstraint{
							ZeroMin: true,
							Max:     200,
						},
					},
				},
			},
		},
	},

	{
		object: newWidth(),
		args:   []string{"-200"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						Width: &IntConstraint{
							Max: 200,
						},
					},
				},
			},
		},
	},

	{
		object: newWidth(),
		args:   []string{"100-200"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						Width: &IntConstraint{
							Min: 100,
							Max: 200,
						},
					},
				},
			},
		},
	},

	{
		object:      newHeight(),
		args:        []string{""},
		errContains: "Unable to parse \"\" as range, wanted something like 480-1024, 480-, -1024 or 1024",
	},

	{
		object: newHeight(),
		args:   []string{"100-200"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						Height: &IntConstraint{
							Min: 100,
							Max: 200,
						},
					},
				},
			},
		},
	},

	{
		object: newHeight(),
		args:   []string{"-200"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						Height: &IntConstraint{
							Max: 200,
						},
					},
				},
			},
		},
	},

	{
		object: newHeight(),
		args:   []string{"100-"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						Height: &IntConstraint{
							Min: 100,
						},
					},
				},
			},
		},
	},

	{
		object: newHeight(),
		args:   []string{"0-200"},
		want: &Constraint{
			Permanode: &PermanodeConstraint{
				Attr: "camliContent",
				ValueInSet: &Constraint{
					File: &FileConstraint{
						IsImage: true,
						Height: &IntConstraint{
							ZeroMin: true,
							Max:     200,
						},
					},
				},
			},
		},
	},

	// Location predicates
	{
		object: newLocation(),
		args:   []string{"Uitdam"}, // Small dutch town
		want: orConst(&Constraint{
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
		}),
		ctx: newGeocodeContext(),
	},

	{
		object: newHasLocation(),
		want:   hasLocationC,
	},
}

func TestKeywords(t *testing.T) {
	cj := func(c *Constraint) []byte {
		v, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tt := range keywordTests {
		got, err := tt.object.Predicate(tt.ctx, tt.args)
		if err != nil {
			if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
				continue
			}
			t.Errorf("%v: %#v(%q) error: %v, but wanted an error containing: %v", tt.name, tt.object, tt.args, err, tt.errContains)
			continue
		}
		if tt.errContains != "" {
			t.Errorf("%v: %#v(%q) succeeded; want error containing %q", tt.name, tt.object, tt.args, tt.errContains)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%v: %#v(%q) got:\n%s\n\nwant:%s\n", tt.name, tt.object, tt.args, cj(got), cj(tt.want))
		}
	}
}

func TestParseWHExpression(t *testing.T) {
	tests := []struct {
		in          string
		wantMin     string
		wantMax     string
		errContains string
	}{
		{in: "450-470", wantMin: "450", wantMax: "470"},
		{in: "450-470+", errContains: "Unable to parse \"450-470+\" as range, wanted something like 480-1024, 480-, -1024 or 1024"},
		{in: "", errContains: "Unable to parse \"\" as range, wanted something like 480-1024, 480-, -1024 or 1024"},
		{in: "450", wantMin: "450", wantMax: "450"},
	}

	for _, tt := range tests {
		gotMin, gotMax, err := parseWHExpression(tt.in)
		if err != nil {
			if tt.errContains != "" && strings.Contains(err.Error(), tt.errContains) {
				continue
			}
			t.Errorf("parseWHExpression(%v) error: %v, but wanted an error containing: %v", tt.in, err, tt.errContains)
			continue
		}
		if tt.errContains != "" {
			t.Errorf("parseWHExpression(%v) succeeded; want error containing %v got: %s,%s ", tt.in, tt.errContains, gotMin, gotMax)
			continue
		}
		if !reflect.DeepEqual(gotMin, tt.wantMin) {
			t.Errorf("parseWHExpression(%s) min  = %v; want %v", tt.in, gotMin, tt.wantMin)
		}
		if !reflect.DeepEqual(gotMax, tt.wantMax) {
			t.Errorf("parseWHExpression(%s) max  = %v; want %v", tt.in, gotMax, tt.wantMax)
		}
	}
}

func TestMatchEqual(t *testing.T) {
	me := matchEqual("foo:bar:baz")
	a := atom{"foo", []string{"bar", "baz"}}

	if m, _ := me.Match(a); !m {
		t.Error("Expected a match")
	}

	a = atom{"foo", []string{"foo", "baz"}}
	if m, _ := me.Match(a); m {
		t.Error("Did not expect a match")
	}
}

func TestMatchPrefix(t *testing.T) {
	mp := matchPrefix{"foo", 1}
	a := atom{"foo", []string{"bar"}}
	if m, err := mp.Match(a); err != nil || !m {
		t.Error("Expected a match")
	}

	a = atom{"foo", []string{}}
	if _, err := mp.Match(a); err == nil {
		t.Error("Expected an error got nil")
	}
	a = atom{"bar", []string{}}
	if m, err := mp.Match(a); err != nil || m {
		t.Error("Expected simple mismatch")
	}
}

func TestLocationConstraint(t *testing.T) {
	var c LocationConstraint
	if c.matchesLatLong(1, 2) {
		t.Error("zero value shouldn't match")
	}
	c.Any = true
	if !c.matchesLatLong(1, 2) {
		t.Error("Any should match")
	}

	c = LocationConstraint{North: 2, South: 1, West: 0, East: 2}
	tests := []struct {
		lat, long float64
		want      bool
	}{
		{1, 1, true},
		{3, 1, false},  // too north
		{1, 3, false},  // too east
		{1, -1, false}, // too west
		{0, 1, false},  // too south
	}
	for _, tt := range tests {
		if got := c.matchesLatLong(tt.lat, tt.long); got != tt.want {
			t.Errorf("matches(%v, %v) = %v; want %v", tt.lat, tt.long, got, tt.want)
		}
	}
}
