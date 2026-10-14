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

package camtypes

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
)

func TestClaimReplayOrder(t *testing.T) {
	refs := []blob.Ref{blob.RefFromString("envelope-a"), blob.RefFromString("envelope-b")}
	if refs[1].Less(refs[0]) {
		refs[0], refs[1] = refs[1], refs[0]
	}
	date := time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)
	want := []Claim{
		{Date: date.Add(-time.Nanosecond), BlobRef: refs[1], MultiIndex: 10},
		{Date: date, BlobRef: refs[0], MultiIndex: 0},
		{Date: date, BlobRef: refs[0], MultiIndex: 1},
		{Date: date, BlobRef: refs[0], MultiIndex: 10},
		{Date: date, BlobRef: refs[1], MultiIndex: 0},
	}
	got := slices.Clone(want)
	slices.Reverse(got)
	sort.Sort(ClaimsByDate(got))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClaimsByDate = %#v; want %#v", got, want)
	}
	ptrs := make(ClaimPtrsByDate, len(got))
	for i := range got {
		ptrs[i] = &got[len(got)-1-i]
	}
	sort.Sort(ptrs)
	for i, cl := range ptrs {
		if *cl != want[i] {
			t.Errorf("ClaimPtrsByDate[%d] = %#v; want %#v", i, cl, want[i])
		}
	}
}

func TestClaimMultiIndexPrivate(t *testing.T) {
	cl := Claim{BlobRef: blob.RefFromString("envelope"), MultiIndex: 123}
	data, err := json.Marshal(cl)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["MultiIndex"]; ok {
		t.Fatalf("internal child ordinal leaked: %s", data)
	}
	cl.MultiIndex = 0
	legacy, err := json.Marshal(cl)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(legacy) {
		t.Fatalf("child index changed JSON: %s != %s", data, legacy)
	}
}
