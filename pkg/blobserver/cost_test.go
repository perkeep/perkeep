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

package blobserver

import "testing"

func TestCostEstimate(t *testing.T) {
	p := Pricing{
		Desc:             "S3",
		ListPer1000:      0.005,
		ListPage:         1000,
		GetPer1000:       0.0004,
		EgressPerGB:      0.09,
		ListBytesPerBlob: 200,
	}
	var e CostEstimate
	if got := e.String(); got != "free" {
		t.Errorf("empty estimate = %q; want free", got)
	}
	e.AddListing(p, 1_000_000, 1000)  // $0.005, and 200 MB out: $0.018
	e.AddGets(p, 10_000, 100_000_000) // $0.004, and 100 MB out: $0.009
	if got, want := e.String(), "≈$0.036: 1.00K listing requests to S3 ($0.0050); 191 MiB of their responses ($0.018); 10.0K fetch or stat requests to S3 ($0.0040); 95.4 MiB transferred out ($0.0090)"; got != want {
		t.Errorf("estimate = %q;\nwant      %q", got, want)
	}
}
