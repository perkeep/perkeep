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

package search_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"perkeep.org/internal/searchtypes"
	"perkeep.org/pkg/search"
)

// Both constructors retain the public function signature through the type alias.
var (
	_ func(string) *search.SearchQuery = search.NamedSearch
	_ func(string) *search.SearchQuery = searchtypes.NamedSearch
)

func TestNamedSearchWrapper(t *testing.T) {
	for _, name := range []string{"", "holiday", " tag:雪 OR is:image ", "say \"hi\"\\there\n"} {
		t.Run(name, func(t *testing.T) {
			got, want := search.NamedSearch(name), searchtypes.NamedSearch(name)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("public NamedSearch = %#v; want %#v", got, want)
			}
			gotJSON, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			wantJSON, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("public JSON = %s; want %s", gotJSON, wantJSON)
			}
		})
	}
}
