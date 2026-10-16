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

import (
	"fmt"
	"strings"
)

// Pricing is what a storage service charges for the operations that
// checks of a storage do, in US dollars, for estimating their costs.
type Pricing struct {
	Desc        string  // such as "AWS S3 Standard, us-west-1"
	ListPer1000 float64 // per 1000 listing requests
	ListPage    int     // the most blobs a listing request returns
	GetPer1000  float64 // per 1000 requests to fetch or stat a blob
	EgressPerGB float64 // per GB (1e9 bytes) transferred out
	// ListBytesPerBlob is about how many bytes a listing response has
	// per blob, which count as transferred out.
	ListBytesPerBlob int
}

// A Pricer is a storage that knows what its service charges. Wrappers of
// storage (such as caches) may implement it by asking what they wrap.
type Pricer interface {
	// Pricing returns the storage's pricing, or false if it's unknown
	// (or free, such as a local disk).
	Pricing() (Pricing, bool)
}

// PricingOf returns the pricing of sto, if it's a Pricer that knows it.
func PricingOf(sto any) (Pricing, bool) {
	if p, ok := sto.(Pricer); ok {
		return p.Pricing()
	}
	return Pricing{}, false
}

// CostEstimate is an estimate of what an operation costs, with what it
// costs for.
type CostEstimate struct {
	Items []CostItem
}

// CostItem is part of a CostEstimate.
type CostItem struct {
	What string // such as "7.7K listing requests to AWS S3 Standard, us-west-1"
	USD  float64
}

// Add adds an item to e.
func (e *CostEstimate) Add(usd float64, format string, args ...any) {
	e.Items = append(e.Items, CostItem{What: fmt.Sprintf(format, args...), USD: usd})
}

// AddListing adds the cost of listing blobs blobs, in requests, with p.
func (e *CostEstimate) AddListing(p Pricing, blobs, requests int64) {
	e.Add(float64(requests)*p.ListPer1000/1000, "%s listing requests to %s", countString(requests), p.Desc)
	if out := blobs * int64(p.ListBytesPerBlob); out > 0 {
		e.Add(float64(out)/1e9*p.EgressPerGB, "%s of their responses", HashEstimate{Bytes: out}.BytesString())
	}
}

// AddGets adds the cost of n requests to fetch or stat blobs, of bytes
// bytes in all, with p.
func (e *CostEstimate) AddGets(p Pricing, n, bytes int64) {
	e.Add(float64(n)*p.GetPer1000/1000, "%s fetch or stat requests to %s", countString(n), p.Desc)
	if bytes > 0 {
		e.Add(float64(bytes)/1e9*p.EgressPerGB, "%s transferred out", HashEstimate{Bytes: bytes}.BytesString())
	}
}

// USD returns the estimate's total.
func (e CostEstimate) USD() float64 {
	var t float64
	for _, it := range e.Items {
		t += it.USD
	}
	return t
}

// String describes e, such as "≈$0.04: 7.7K listing requests to AWS S3
// Standard, us-west-1 ($0.042)".
func (e CostEstimate) String() string {
	if len(e.Items) == 0 {
		return "free"
	}
	var parts []string
	for _, it := range e.Items {
		parts = append(parts, fmt.Sprintf("%s (%s)", it.What, usdString(it.USD)))
	}
	return fmt.Sprintf("≈%s: %s", usdString(e.USD()), strings.Join(parts, "; "))
}

func usdString(usd float64) string {
	switch {
	case usd >= 1:
		return fmt.Sprintf("$%.2f", usd)
	case usd >= 0.01:
		return fmt.Sprintf("$%.3f", usd)
	case usd > 0:
		return fmt.Sprintf("$%.4f", usd)
	}
	return "$0"
}

func countString(n int64) string {
	return HashEstimate{Blobs: n}.BlobsString()
}
