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

package search

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"perkeep.org/internal/searchhelp"
)

func checkBuiltinSearchHelp(t *testing.T) {
	t.Helper()
	var help []json.RawMessage
	if err := json.Unmarshal([]byte(SearchHelp()), &help); err != nil {
		t.Fatal(err)
	}
	if len(help) != len(keywords) {
		t.Fatalf("live help has %d entries; registry has %d", len(help), len(keywords))
	}
	builtins := make([]json.RawMessage, 0, len(help))
	for i, k := range keywords {
		want, err := json.Marshal(struct{ Name, Description string }{k.Name(), k.Description()})
		if err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		if err := json.Compact(&got, help[i]); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Bytes(), want) {
			t.Errorf("live help entry %d = %s; want %s", i, got.Bytes(), want)
		}
		// InitHandler may append this optional predicate more than once.
		// Filter by concrete type and registry position, not by its name.
		if _, optional := k.(namedSearch); !optional {
			builtins = append(builtins, help[i])
		}
	}
	got, err := json.MarshalIndent(builtins, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != searchhelp.SearchHelp() {
		t.Error("offline search help is stale; run go generate ./internal/searchhelp from the repository root")
	}
}

func TestBuiltinSearchHelp(t *testing.T) {
	checkBuiltinSearchHelp(t)
}

func TestBuiltinSearchHelpWithNamedPredicates(t *testing.T) {
	// Keep this test nonparallel: the live registry is package-global.
	original := keywords
	keywords = append(slices.Clone(keywords), newNamedSearch(nil), newNamedSearch(nil))
	t.Cleanup(func() { keywords = original })
	checkBuiltinSearchHelp(t)
}
