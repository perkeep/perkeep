/*
Copyright 2026 The Perkeep Authors

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

package searchtypes

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNamedSearchJSON(t *testing.T) {
	for _, tt := range []struct {
		name  string
		alias string
		want  string
	}{
		{
			name:  "ordinary",
			alias: "holiday",
			want:  `{"constraint":{"permanode":{"at":"0001-01-01T00:00:00Z","attr":"camliNamedSearch","value":"holiday"}},"around":null,"describe":{"blobref":null,"at":null}}`,
		},
		{
			name: "empty",
			want: `{"constraint":{"permanode":{"at":"0001-01-01T00:00:00Z","attr":"camliNamedSearch"}},"around":null,"describe":{"blobref":null,"at":null}}`,
		},
		{
			name:  "literal search syntax and whitespace",
			alias: " tag:雪 OR is:image ",
			want:  `{"constraint":{"permanode":{"at":"0001-01-01T00:00:00Z","attr":"camliNamedSearch","value":" tag:雪 OR is:image "}},"around":null,"describe":{"blobref":null,"at":null}}`,
		},
		{
			name:  "JSON escaping",
			alias: "say \"hi\"\\there\n",
			want:  `{"constraint":{"permanode":{"at":"0001-01-01T00:00:00Z","attr":"camliNamedSearch","value":"say \"hi\"\\there\n"}},"around":null,"describe":{"blobref":null,"at":null}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			query := NamedSearch(tt.alias)
			got, err := json.Marshal(query)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("JSON = %s; want %s", got, tt.want)
			}
			var decoded SearchQuery
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(&decoded, query) {
				t.Errorf("roundtrip = %#v; want %#v", &decoded, query)
			}
		})
	}
}

func TestNamedSearchDefaults(t *testing.T) {
	for _, name := range []string{"", "holiday"} {
		t.Run(name, func(t *testing.T) {
			got := NamedSearch(name)
			want := &SearchQuery{
				Constraint: &Constraint{
					Permanode: &PermanodeConstraint{
						Attr:  "camliNamedSearch",
						Value: name,
					},
				},
				Describe: &DescribeRequest{},
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("NamedSearch(%q) = %#v; want %#v", name, got, want)
			}
			if got == nil || got.Describe == nil {
				t.Fatal("NamedSearch must request descriptions")
			}
			if got.Limit != 0 || got.Sort != UnspecifiedSort {
				t.Errorf("limit = %d, sort = %v; want query defaults", got.Limit, got.Sort)
			}
		})
	}
}

func TestNamedSearchFreshAllocations(t *testing.T) {
	first, second := NamedSearch("holiday"), NamedSearch("holiday")
	if first == second || first.Constraint == second.Constraint ||
		first.Constraint.Permanode == second.Constraint.Permanode || first.Describe == second.Describe {
		t.Fatal("NamedSearch calls share mutable state")
	}
	first.Expression = "changed"
	first.Limit = 1
	first.Sort = Unsorted
	first.Constraint.Anything = true
	first.Constraint.Permanode.Attr = "changed"
	first.Constraint.Permanode.Value = "changed"
	first.Describe.Depth = 3
	first.Describe.Rules = []*DescribeRule{{Attrs: []string{"changed"}}}
	if want := NamedSearch("holiday"); !reflect.DeepEqual(second, want) {
		t.Errorf("mutating one query changed another: %#v; want %#v", second, want)
	}
}
