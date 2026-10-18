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
	"context"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/types/camtypes"
)

// matcherFileIndex supplies just the metadata needed by an unconstrained file
// matcher. Any unexpected index operation still fails through the nil interface.
type matcherFileIndex struct{ index.Interface }

func (matcherFileIndex) GetFileInfo(context.Context, blob.Ref) (camtypes.FileInfo, error) {
	return camtypes.FileInfo{}, nil
}

func TestConstraintMatcherCache(t *testing.T) {
	c := &Constraint{Anything: true}
	first := constraintMatcher(c)
	c.Anything = false
	// Matcher compilation is once per constraint, including across repeated queries.
	second := constraintMatcher(c)
	for _, matcher := range []matchFn{first, second} {
		if got, err := matcher(context.Background(), nil, blob.Ref{}, camtypes.BlobMeta{}); !got || err != nil {
			t.Fatalf("cached matcher = %v, %v; want true, nil", got, err)
		}
	}
}

func TestConstraintMatcherBindsReceivers(t *testing.T) {
	br := blob.RefFromString("match candidate")
	tests := []struct {
		name       string
		constraint *Constraint
		clear      func(*Constraint)
		state      *search
		camliType  schema.CamliType
		want       bool
	}{
		{
			name:       "permanode",
			constraint: &Constraint{Permanode: &PermanodeConstraint{}},
			clear:      func(c *Constraint) { c.Permanode = nil },
			state:      &search{h: &Handler{corpus: new(index.Corpus)}, q: &SearchQuery{}},
			camliType:  schema.TypePermanode,
			want:       true,
		},
		{
			name:       "file",
			constraint: &Constraint{File: &FileConstraint{}},
			clear:      func(c *Constraint) { c.File = nil },
			state:      &search{h: &Handler{index: matcherFileIndex{}}, q: &SearchQuery{}},
			camliType:  schema.TypeFile,
			want:       true,
		},
		{
			name:       "directory",
			constraint: &Constraint{Dir: &DirConstraint{BlobRefPrefix: blob.RefFromString("other").String()}},
			clear:      func(c *Constraint) { c.Dir = nil },
			camliType:  schema.TypeDirectory,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := constraintMatcher(tt.constraint)
			// A method value used to bind each nested receiver when compiling. Closures
			// replacing those method values must not reread the subsequently changed field.
			tt.clear(tt.constraint)
			got, err := matcher(context.Background(), tt.state, br, camtypes.BlobMeta{CamliType: tt.camliType})
			if got != tt.want || err != nil {
				t.Fatalf("bound matcher = %v, %v; want %v, nil", got, err, tt.want)
			}
		})
	}
}

func TestSearchQueryValidationOrder(t *testing.T) {
	invalid := &Constraint{Claim: &ClaimConstraint{}}
	for _, tt := range []struct {
		name  string
		query SearchQuery
		want  string
	}{
		{"sort", SearchQuery{Sort: maxSortType, Continue: "token", Around: blob.RefFromString("around"), Constraint: invalid, Expression: "anything"}, "invalid sort type"},
		{"continue and around", SearchQuery{Sort: MapSort, Continue: "token", Around: blob.RefFromString("around"), Constraint: invalid, Expression: "anything"}, "Continue and Around parameters are mutually exclusive"},
		{"map", SearchQuery{Sort: MapSort, Continue: "token", Constraint: invalid, Expression: "anything"}, "Continue or Around parameters are not available with MapSort"},
		{"constraint and expression", SearchQuery{Constraint: invalid, Expression: "anything"}, "Constraint and Expression are mutually exclusive in a search query"},
		{"constraint", SearchQuery{Constraint: invalid}, "TODO: implement ClaimConstraint"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := searchQueryCheckValid(&tt.query, context.Background())
			if err == nil || err.Error() != tt.want {
				t.Fatalf("validation error = %v; want %q", err, tt.want)
			}
		})
	}
}
