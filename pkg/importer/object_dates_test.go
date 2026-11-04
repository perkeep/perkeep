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
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
)

// TestObjectClaimDates checks that an Object's claims are dated in the
// order it writes them, even when the clock doesn't move between them,
// as it doesn't in a synctest bubble, so that they apply in that order.
func TestObjectClaimDates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newObjectClaimFixture(t, nil)
		for _, write := range []func() error{
			func() error { return f.o.SetAttr("state", "a") },
			func() error { return f.o.DelAttr("state", "") },
			func() error { return f.o.SetAttrs("state", "b", "other", "c") },
			func() error { return f.o.SetAttr("state", "d") },
		} {
			if err := write(); err != nil {
				t.Fatal(err)
			}
		}
		var last time.Time
		for i, body := range f.target.uploads() {
			b, err := schema.BlobFromReader(blob.RefFromString(body), strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			claim, ok := b.AsClaim()
			if !ok {
				t.Fatalf("upload %d isn't a claim", i)
			}
			date, err := time.Parse(time.RFC3339Nano, claim.ClaimDateString())
			if err != nil {
				t.Fatal(err)
			}
			if !date.After(last) {
				t.Errorf("claim %d is dated %v, not after the claim before's %v", i, date, last)
			}
			last = date
		}
	})
}
