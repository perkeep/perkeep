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

package index

import (
	"math/rand"
	"slices"
	"testing"
)

func TestQueueSet(t *testing.T) {
	var q queueSet[int]
	if q.Len() != 0 || q.Contains(0) {
		t.Fatal("zero-value queue is not empty")
	}
	if got, ok := q.Pop(); got != 0 || ok {
		t.Fatalf("empty Pop = (%v, %v); want (0, false)", got, ok)
	}
	for _, v := range []int{0, 1, 0, 2, 1} {
		q.Add(v)
	}
	if q.Len() != 3 {
		t.Fatalf("Len = %d; want 3", q.Len())
	}
	for _, v := range []int{0, 1, 2} {
		if !q.Contains(v) {
			t.Errorf("Contains(%d) = false; want true", v)
		}
	}
	if got, ok := q.Pop(); got != 0 || !ok {
		t.Fatalf("zero entry Pop = (%v, %v); want (0, true)", got, ok)
	}
	if q.Contains(0) || q.Len() != 2 {
		t.Fatal("Pop did not remove membership before returning")
	}
	// Readding a popped value places it behind the remaining entries.
	q.Add(0)
	q.Add(0)
	for _, want := range []int{1, 2, 0} {
		if got, ok := q.Pop(); got != want || !ok {
			t.Fatalf("Pop = (%v, %v); want (%v, true)", got, ok, want)
		}
		if q.Contains(want) {
			t.Errorf("popped value %d remains a member", want)
		}
	}
	if q.Len() != 0 {
		t.Fatalf("drained Len = %d; want 0", q.Len())
	}
	q.Add(3)
	if got, ok := q.Pop(); got != 3 || !ok {
		t.Fatalf("reused Pop = (%v, %v); want (3, true)", got, ok)
	}
	if got, ok := q.Pop(); got != 0 || ok {
		t.Fatalf("drained Pop = (%v, %v); want (0, false)", got, ok)
	}
}

func TestQueueSetClearsAndCompacts(t *testing.T) {
	q := queueSet[*int]{items: make([]*int, 0, 4)}
	backing := q.items[:cap(q.items)]
	a, b, c, d := new(int), new(int), new(int), new(int)
	for _, v := range []*int{a, b, c, d} {
		q.Add(v)
	}
	for i, want := range []*int{a, b} {
		if got, ok := q.Pop(); got != want || !ok {
			t.Fatalf("Pop = (%v, %v); want (%v, true)", got, ok, want)
		}
		if backing[i] != nil || q.Contains(want) {
			t.Fatal("Pop retained a pointer in its slot or membership")
		}
	}
	// A full slice with half consumed must compact without allocating.
	q.Add(a)
	if q.head != 0 || len(q.items) != 3 ||
		&q.items[0] != &backing[0] {
		t.Fatal("Add did not compact and reuse the consumed prefix")
	}
	if want := []*int{c, d, a, nil}; !slices.Equal(backing, want) {
		t.Fatalf("compacted backing = %v; want %v", backing, want)
	}
	for _, want := range []*int{c, d, a} {
		if got, ok := q.Pop(); got != want || !ok {
			t.Fatalf("Pop = (%v, %v); want (%v, true)", got, ok, want)
		}
	}
	if q.Len() != 0 || len(q.items) != 0 || q.head != 0 {
		t.Fatal("drained queue did not reset")
	}
	for i, v := range backing {
		if v != nil {
			t.Errorf("drained backing slot %d retains %v", i, v)
		}
	}
	// Nil is a legitimate entry, not an empty-queue sentinel.
	q.Add(nil)
	q.Add(nil)
	if q.Len() != 1 || !q.Contains(nil) {
		t.Fatal("nil entry was dropped or duplicated")
	}
	if &q.items[0] != &backing[0] {
		t.Error("drained queue did not reuse its backing array")
	}
	if got, ok := q.Pop(); got != nil || !ok {
		t.Fatalf("nil entry Pop = (%v, %v); want (nil, true)", got, ok)
	}
	if q.Contains(nil) {
		t.Error("popped nil remains a member")
	}
	if got, ok := q.Pop(); got != nil || ok {
		t.Fatalf("empty Pop = (%v, %v); want (nil, false)", got, ok)
	}
}

func TestQueueSetMixedOperations(t *testing.T) {
	var q queueSet[int]
	var model []int
	rng := rand.New(rand.NewSource(1787))
	pop := func() {
		t.Helper()
		want, wantOK := 0, len(model) != 0
		if wantOK {
			want, model = model[0], model[1:]
		}
		if got, ok := q.Pop(); got != want || ok != wantOK {
			t.Fatalf("Pop = (%v, %v); want (%v, %v)",
				got, ok, want, wantOK)
		}
	}
	for step := range 500 {
		if rng.Intn(3) == 0 {
			pop()
		} else {
			v := rng.Intn(9) - 4
			q.Add(v)
			if !slices.Contains(model, v) {
				model = append(model, v)
			}
		}
		if q.Len() != len(model) {
			t.Fatalf("step %d: Len = %d; want %d",
				step, q.Len(), len(model))
		}
		for v := -4; v <= 4; v++ {
			if q.Contains(v) != slices.Contains(model, v) {
				t.Fatalf("step %d: Contains(%d) differs from model",
					step, v)
			}
		}
	}
	for len(model) != 0 {
		pop()
	}
	pop()
}
