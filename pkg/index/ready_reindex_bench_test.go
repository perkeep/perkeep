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
	"fmt"
	"testing"

	"perkeep.org/pkg/blob"
	"tailscale.com/util/set"
)

// BenchmarkReadyReindexDrain isolates the dequeue algorithm in indexReadyBlobs.
// Each operation drains a whole batch; ns/blob reports the per-item cost.
// Both implementations take ix.mu per pop and retain their membership map.
// MapPop uses range/break/delete; Queue uses queueSet.Pop.
//
// Setup, enqueueing, fetch/index work, and readyFailed bookkeeping are excluded.
// Fixed iteration counts (e.g. -benchtime=100x) avoid excessive untimed setup
// for the small batches.
func BenchmarkReadyReindexDrain(b *testing.B) {
	for _, size := range []int{1, 1024, 16384, 65536} {
		b.Run(fmt.Sprintf("Burst/%d", size), func(b *testing.B) {
			benchmarkReadyReindexDrain(b, size, size)
		})
	}
	// Model the Index-lifetime map after a previous large backlog has drained.
	for _, size := range []int{1, 16, 1024} {
		b.Run(fmt.Sprintf("After65536/%d", size), func(b *testing.B) {
			benchmarkReadyReindexDrain(b, 65536, size)
		})
	}
}

func benchmarkReadyReindexDrain(b *testing.B, highWater, size int) {
	refs := make([]blob.Ref, highWater)
	for i := range refs {
		refs[i] = blob.RefFromString(fmt.Sprintf("ready reindex benchmark %d", i))
	}
	for _, queued := range []bool{false, true} {
		name := "MapPop"
		if queued {
			name = "Queue"
		}
		b.Run(name, func(b *testing.B) {
			ix := new(Index)
			legacy := make(set.Set[blob.Ref])
			enqueue := func(br blob.Ref) {
				if queued {
					ix.readyReindex.Add(br)
				} else {
					legacy.Add(br)
				}
			}
			pop := func() (blob.Ref, bool) {
				if queued {
					return ix.readyReindex.Pop()
				}
				var br blob.Ref
				for br = range legacy {
					break
				}
				ok := br.Valid()
				if ok {
					legacy.Delete(br)
				}
				return br, ok
			}
			// Grow by insertion, not just a capacity hint. Delete individually
			// as the production worker does, retaining the map's high water.
			ix.Lock()
			for _, br := range refs {
				enqueue(br)
			}
			for {
				if _, ok := pop(); !ok {
					break
				}
			}
			ix.Unlock()
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				ix.Lock()
				for _, br := range refs[:size] {
					enqueue(br)
				}
				ix.Unlock()
				b.StartTimer()
				popped := 0
				for {
					ix.Lock()
					_, ok := pop()
					if !ok {
						ix.Unlock()
						break
					}
					ix.Unlock()
					popped++
				}
				b.StopTimer()
				remaining := ix.readyReindex.Len() + legacy.Len()
				if popped != size || remaining != 0 {
					b.Fatalf("drained %d, remaining %d; want %d, 0",
						popped, remaining, size)
				}
				b.StartTimer()
			}
			b.StopTimer()
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(size), "ns/blob")
		})
	}
}
