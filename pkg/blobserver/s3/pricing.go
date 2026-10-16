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

package s3

import "perkeep.org/pkg/blobserver"

// awsRequestPrices are AWS's S3 Standard prices, in US dollars per 1000
// requests, for listing (as for PUT, COPY, and POST) and for GET and
// HEAD requests, by region, as of October 2026. Other regions use
// us-east-1's, which may be lower than theirs.
var awsRequestPrices = map[string]struct{ list, get float64 }{
	"us-east-1": {0.005, 0.0004},
	"us-east-2": {0.005, 0.0004},
	"us-west-1": {0.0055, 0.00044},
	"us-west-2": {0.005, 0.0004},
}

// awsEgressPerGB is AWS's price for data transferred out to the
// internet, per GB, for the first 10 TB a month, not counting the first
// 100 GB a month, which are free.
const awsEgressPerGB = 0.09

var _ blobserver.Pricer = (*s3Storage)(nil)

// Pricing returns AWS's S3 Standard prices for the bucket's region, or
// false for an S3-compatible service other than AWS.
func (sto *s3Storage) Pricing() (blobserver.Pricing, bool) {
	if sto.hostname != "" && !isAWSHostname(sto.hostname) {
		return blobserver.Pricing{}, false
	}
	region := sto.client.Options().Region
	desc := "AWS S3 Standard, " + region
	prices, ok := awsRequestPrices[region]
	if !ok {
		prices = awsRequestPrices["us-east-1"]
		desc += " (at us-east-1's prices)"
	}
	return blobserver.Pricing{
		Desc:             desc,
		ListPer1000:      prices.list,
		ListPage:         s3MaxKeys,
		GetPer1000:       prices.get,
		EgressPerGB:      awsEgressPerGB,
		ListBytesPerBlob: 200,
	}, true
}
