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

import "tailscale.com/util/set"

// queueSet is a FIFO queue that coalesces duplicate pending values.
// Its zero value is ready to use. Pop takes constant time; Add takes amortized
// constant time. It is not safe for concurrent use.
// Values must be reflexive (v == v); floating-point NaNs are not supported.
type queueSet[T comparable] struct {
	items   []T
	head    int
	members set.Set[T]
}

func (q *queueSet[T]) Len() int { return q.members.Len() }

func (q *queueSet[T]) Contains(v T) bool { return q.members.Contains(v) }

// Add queues v unless it is already pending.
func (q *queueSet[T]) Add(v T) {
	if q.Contains(v) {
		return
	}
	// Reclaim the consumed prefix before growing, but only once at least
	// half the entries have been popped, to amortize copying.
	if len(q.items) == cap(q.items) && q.head > 0 && q.head >= len(q.items)/2 {
		n := copy(q.items, q.items[q.head:])
		clear(q.items[n:])
		q.items = q.items[:n]
		q.head = 0
	}
	q.items = append(q.items, v)
	q.members.Make()
	q.members.Add(v)
}

// Pop removes the oldest pending value, or returns the zero value and false
// if empty. The returned value can be added again immediately.
func (q *queueSet[T]) Pop() (v T, ok bool) {
	if q.Len() == 0 {
		return v, false
	}
	v = q.items[q.head]
	var zero T
	q.items[q.head] = zero
	q.head++
	if q.head == len(q.items) {
		q.items = q.items[:0]
		q.head = 0
	}
	q.members.Delete(v)
	return v, true
}
