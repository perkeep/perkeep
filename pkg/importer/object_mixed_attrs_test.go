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

package importer

import (
	"errors"
	"reflect"
	"testing"

	"perkeep.org/pkg/schema"
)

func TestObjectSetAttrsWithDeletes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		old     map[string][]string
		deletes []string
		sets    []string
		want    []objectClaimMutation
		cached  map[string][]string
	}{
		{
			name:    "mixed",
			old:     map[string][]string{"remove": {"old", "other"}, "keep": {"same"}},
			deletes: []string{"remove", "missing", "remove"},
			sets:    []string{"keep", "same", "new", "value", "completed", "2"},
			want: []objectClaimMutation{
				{schema.DelAttributeClaim, "remove", ""},
				{schema.SetAttributeClaim, "new", "value"},
				{schema.SetAttributeClaim, "completed", "2"},
			},
			cached: map[string][]string{"keep": {"same"}, "new": {"value"}, "completed": {"2"}},
		},
		{
			name:    "delete before ordered repeated sets",
			old:     map[string][]string{"key": {"old", "other"}},
			deletes: []string{"key"},
			sets:    []string{"key", "first", "key", "last"},
			want: []objectClaimMutation{
				{schema.DelAttributeClaim, "key", ""},
				{schema.SetAttributeClaim, "key", "first"},
				{schema.SetAttributeClaim, "key", "last"},
			},
			cached: map[string][]string{"key": {"last"}},
		},
		{
			name:    "delete explicit empty value",
			old:     map[string][]string{"key": {""}},
			deletes: []string{"key"},
			want:    []objectClaimMutation{{schema.DelAttributeClaim, "key", ""}},
			cached:  map[string][]string{},
		},
		{
			name:    "no ops",
			old:     map[string][]string{"key": {"same"}},
			deletes: []string{"missing", "missing"},
			sets:    []string{"key", "same", "unset", ""},
			cached:  map[string][]string{"key": {"same"}},
		},
		{
			name:   "single set",
			old:    map[string][]string{},
			sets:   []string{"key", "value"},
			want:   []objectClaimMutation{{schema.SetAttributeClaim, "key", "value"}},
			cached: map[string][]string{"key": {"value"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newObjectClaimFixture(t, tc.old)
			if err := f.o.SetAttrsWithDeletes(tc.deletes, tc.sets...); err != nil {
				t.Fatal(err)
			}
			if len(tc.want) == 0 {
				if len(f.target.uploads()) != 0 {
					t.Fatal("no-op update uploaded a claim")
				}
			} else {
				f.wantClaims(t, tc.want...)
			}
			if !reflect.DeepEqual(f.o.attr, tc.cached) {
				t.Fatalf("cached attributes = %#v; want %#v", f.o.attr, tc.cached)
			}
		})
	}
}

func TestObjectSetAttrsWithDeletesFailure(t *testing.T) {
	old := map[string][]string{"remove": {"value"}, "keep": {"old"}, "completed": {"1"}}
	f := newObjectClaimFixture(t, old)
	failure := errors.New("injected mixed-claim upload failure")
	f.target.err = failure
	err := f.o.SetAttrsWithDeletes([]string{"remove"}, "keep", "new", "completed", "2")
	if !errors.Is(err, failure) {
		t.Fatalf("mixed update failure = %v", err)
	}
	if !reflect.DeepEqual(f.o.attr, old) {
		t.Fatal("failed upload changed cached attributes")
	}
	if len(f.target.uploads()) != 1 {
		t.Fatal("failed mixed update did not attempt exactly one envelope")
	}
}

func TestObjectSetAttrsWithDeletesOddArguments(t *testing.T) {
	f := newObjectClaimFixture(t, map[string][]string{"key": {"old"}})
	defer func() {
		if recover() == nil {
			t.Fatal("odd set arguments did not panic")
		}
		if len(f.target.uploads()) != 0 || f.o.Attr("key") != "old" {
			t.Fatal("invalid arguments changed the object")
		}
	}()
	_ = f.o.SetAttrsWithDeletes([]string{"key"}, "unpaired")
}
