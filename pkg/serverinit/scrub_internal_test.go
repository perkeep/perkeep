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

package serverinit

import (
	"strings"
	"testing"

	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
)

// pricedStorage is a memory.Storage with S3-like prices.
type pricedStorage struct{ *memory.Storage }

func (pricedStorage) Pricing() (blobserver.Pricing, bool) {
	return blobserver.Pricing{
		Desc:             "S3",
		ListPer1000:      0.005,
		ListPage:         1000,
		GetPer1000:       0.0004,
		EgressPerGB:      0.09,
		ListBytesPerBlob: 200,
	}, true
}

func TestScrubCost(t *testing.T) {
	c := &census{results: map[string]censusResult{}}
	s := &scrub{c: c, prefix: "/s3/", sto: pricedStorage{new(memory.Storage)}}
	if got := s.cost(); !strings.HasPrefix(got, "unknown until the storage census") {
		t.Errorf("cost before the census = %q; want unknown", got)
	}

	// 6.9M sha1 blobs of 330 GiB: 27 listing requests for each of 256
	// shards ($0.035) and 1.38 GB of their responses ($0.124), and 6.9M
	// fetches ($2.76) of 354 GB ($31.9).
	c.results["/s3/"] = censusResult{ests: []blobserver.HashEstimate{{Hash: "sha1", Blobs: 6_900_000, Bytes: 330 << 30}}}
	got := s.cost()
	for _, want := range []string{"a full pass: ≈$34.8", "6.91K listing requests", "6.90M fetch or stat requests", "330 GiB transferred out"} {
		if !strings.Contains(got, want) {
			t.Errorf("cost = %q; want it to contain %q", got, want)
		}
	}
}
