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
	"testing"

	"perkeep.org/pkg/blob"
)

func TestArchiveImportResultAccountRefJSON(t *testing.T) {
	ref := blob.RefFromString("existing archive account")
	for _, tt := range []struct {
		name    string
		ref     blob.Ref
		created bool
	}{
		{name: "unmatched inspection"},
		{name: "matched inspection", ref: ref},
		{name: "new account", ref: ref, created: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := ArchiveImportResult{
				Identity:   ArchiveIdentity{UserName: "runner"},
				AccountRef: tt.ref,
				Created:    tt.created,
			}
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			value, present := wire["accountRef"]
			if !tt.ref.Valid() {
				if present {
					t.Fatalf("unmatched accountRef must be omitted, got %s in %s", value, data)
				}
			} else {
				want, err := json.Marshal(tt.ref)
				if err != nil {
					t.Fatal(err)
				}
				if !present || string(value) != string(want) {
					t.Fatalf("accountRef = %s, want %s", value, want)
				}
			}
			var roundTrip ArchiveImportResult
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if roundTrip.AccountRef != tt.ref || roundTrip.Created != tt.created {
				t.Fatalf("round trip = %+v, want %+v", roundTrip, result)
			}
		})
	}
}
