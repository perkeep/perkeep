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

package attrclaims

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
)

func newSigner(t *testing.T) *schema.Signer {
	t.Helper()
	ent, err := jsonsign.NewEntity()
	if err != nil {
		t.Fatal(err)
	}
	armorPub, err := jsonsign.ArmoredPublicKey(ent)
	if err != nil {
		t.Fatal(err)
	}
	s, err := schema.NewSigner(blob.RefFromString(armorPub), strings.NewReader(armorPub), ent)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSign(t *testing.T) {
	ctx := context.Background()
	signer := newSigner(t)
	pn := blob.RefFromString("permanode")
	start := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)

	one, err := Sign(ctx, signer, pn, []Change{{"set", "title", "x"}}, start)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || !strings.Contains(one[0], `"claimType": "set-attribute"`) {
		t.Fatalf("one change: got %d claims: %v", len(one), one)
	}

	var changes []Change
	for i := range 10000 {
		changes = append(changes, Change{"add", "camliMember", blob.RefFromString(fmt.Sprint(i)).String()})
	}
	claims, err := Sign(ctx, signer, pn, changes, start)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) < 2 {
		t.Fatalf("got %d claims; want the changes split across several", len(claims))
	}
	members := 0
	var last time.Time
	for i, c := range claims {
		if len(c) > schema.MaxSchemaBlobSize {
			t.Errorf("claim %d is %d bytes, over the limit", i, len(c))
		}
		members += strings.Count(c, `"attribute": "camliMember"`)
		b, err := schema.BlobFromReader(blob.RefFromString(c), strings.NewReader(c))
		if err != nil {
			t.Fatal(err)
		}
		date, err := b.ClaimDate()
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && !date.After(last) {
			t.Errorf("claim %d dated %v, not after the one before (%v)", i, date, last)
		}
		last = date
	}
	if members != len(changes) {
		t.Errorf("claims have %d changes; want %d", members, len(changes))
	}

	if _, err := Sign(ctx, signer, pn, []Change{{"set", "content", strings.Repeat("x", schema.MaxSchemaBlobSize)}}, start); err == nil {
		t.Error("Sign of a value too large for a claim succeeded")
	}
	if _, err := Sign(ctx, signer, pn, []Change{{"frob", "title", "x"}}, start); err == nil {
		t.Error("Sign of an unknown op succeeded")
	}
}

func TestPlannedPermanode(t *testing.T) {
	ctx := context.Background()
	signer := newSigner(t)
	s1, r1, err := PlannedPermanode(ctx, signer, "key")
	if err != nil {
		t.Fatal(err)
	}
	s2, r2, err := PlannedPermanode(ctx, signer, "key")
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 || r1 != r2 || r1 != blob.RefFromString(s1) {
		t.Errorf("planned permanodes differ: %v, %v", r1, r2)
	}
	if _, r3, _ := PlannedPermanode(ctx, signer, "other"); r3 == r1 {
		t.Error("different keys made the same permanode")
	}
}
