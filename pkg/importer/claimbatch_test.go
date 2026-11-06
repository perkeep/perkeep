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
	"context"
	"fmt"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
)

// claimsOf returns the attribute operations of the claim bodies, as
// "permanode attribute=value", and fails if any isn't a claim.
func claimsOf(t *testing.T, bodies []string, names map[blob.Ref]string) [][]string {
	t.Helper()
	var all [][]string
	for _, body := range bodies {
		b, err := schema.BlobFromReader(blob.RefFromString(body), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		c, ok := b.AsClaim()
		if !ok {
			t.Fatalf("uploaded a %s, not a claim", b.Type())
		}
		acs, err := c.AttributeClaims()
		if err != nil {
			t.Fatal(err)
		}
		var ops []string
		for _, ac := range acs {
			ops = append(ops, fmt.Sprintf("%s %s=%s", names[ac.Permanode], ac.Attribute, ac.Value))
		}
		all = append(all, ops)
	}
	return all
}

func TestClaimBatch(t *testing.T) {
	ctx := context.Background()
	f := newObjectClaimFixture(t, nil)
	h := f.o.h
	objs, err := h.NewObjects(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.target.uploads()); n != 3 {
		t.Fatalf("NewObjects(3) uploaded %d blobs; want the 3 permanodes", n)
	}
	a, b, c := objs[0], objs[1], objs[2]
	names := map[blob.Ref]string{a.pn: "a", b.pn: "b", c.pn: "c"}
	if err := c.SetAttrs("title", "C"); err != nil {
		t.Fatal(err)
	}
	before := len(f.target.uploads())

	batch := h.NewClaimBatch()
	batch.SetAttrs(a, "title", "A", "x", "1")
	batch.SetAttrs(b, "title", "B")
	batch.SetAttrs(c, "title", "C") // unchanged
	batch.SetAttrs(a, "y", "2")
	if err := batch.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := claimsOf(t, f.target.uploads()[before:], names)
	if len(got) != 1 {
		t.Fatalf("committed %d claims; want one: %q", len(got), got)
	}
	ops := strings.Join(got[0], ", ")
	for _, want := range []string{"a title=A", "a x=1", "a y=2", "b title=B"} {
		if !strings.Contains(ops, want) {
			t.Errorf("claim of %q lacks %q", ops, want)
		}
	}
	if strings.Contains(ops, "c ") {
		t.Errorf("claim of %q changes c, unchanged", ops)
	}
	if !batch.Changed(a) || !batch.Changed(b) || batch.Changed(c) {
		t.Errorf("changed a, b, c = %v, %v, %v; want true, true, false", batch.Changed(a), batch.Changed(b), batch.Changed(c))
	}
	if a.Attr("title") != "A" || a.Attr("y") != "2" || b.Attr("title") != "B" {
		t.Errorf("cached attributes not updated: %q %q %q", a.Attr("title"), a.Attr("y"), b.Attr("title"))
	}

	// Again, the same: nothing to claim.
	before = len(f.target.uploads())
	again := h.NewClaimBatch()
	again.SetAttrs(a, "title", "A")
	if err := again.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(f.target.uploads()) - before; n != 0 || again.Changed(a) {
		t.Errorf("unchanged batch uploaded %d blobs, changed %v", n, again.Changed(a))
	}
}

// TestClaimBatchSplits tests that a batch too big for one claim is split
// between objects, never within one.
func TestClaimBatchSplits(t *testing.T) {
	ctx := context.Background()
	f := newObjectClaimFixture(t, nil)
	h := f.o.h
	objs, err := h.NewObjects(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	names := map[blob.Ref]string{}
	batch := h.NewClaimBatch()
	big := strings.Repeat("x", 50<<10)
	for i, o := range objs {
		names[o.pn] = fmt.Sprint(i)
		batch.SetAttrs(o, "a", big, "b", "small")
	}
	before := len(f.target.uploads())
	if err := batch.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := claimsOf(t, f.target.uploads()[before:], names)
	if len(got) < 2 || len(got) > 3 {
		t.Fatalf("committed %d claims; want 2 or 3", len(got))
	}
	seen := map[string]int{}
	for i, ops := range got {
		for _, op := range ops {
			obj, _, _ := strings.Cut(op, " ")
			if j, ok := seen[obj]; ok && j != i {
				t.Errorf("object %s's changes in claims %d and %d", obj, j, i)
			}
			seen[obj] = i
		}
	}
	if len(seen) != len(objs) {
		t.Errorf("claimed %d objects' changes; want %d", len(seen), len(objs))
	}
}

func TestChildPathObjects(t *testing.T) {
	ctx := context.Background()
	f := newObjectClaimFixture(t, nil)
	h := f.o.h
	objs, err := h.NewObjects(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	parent := objs[0]
	before := len(f.target.uploads())
	children, err := parent.ChildPathObjects(ctx, []string{"x", "y", "x"})
	if err != nil {
		t.Fatal(err)
	}
	// Two permanodes and one claim on the parent.
	if n := len(f.target.uploads()) - before; n != 3 {
		t.Errorf("uploaded %d blobs; want 3", n)
	}
	if children[0] != children[2] || children[0] == children[1] {
		t.Errorf("children of x, y, x = %v; want x's twice", children)
	}
	if parent.Attr("camliPath:x") != children[0].pn.String() || parent.Attr("camliPath:y") != children[1].pn.String() {
		t.Errorf("parent's camliPaths = %q, %q", parent.Attr("camliPath:x"), parent.Attr("camliPath:y"))
	}
}
