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

import (
	"testing"

	"perkeep.org/pkg/blobserver"

	"go4.org/jsonconfig"
)

func TestPricing(t *testing.T) {
	for _, tt := range []struct {
		name     string
		conf     jsonconfig.Obj
		wantOK   bool
		wantList float64
		wantDesc string
	}{
		{"us-west-1", jsonconfig.Obj{"aws_region": "us-west-1"}, true, 0.0055, "AWS S3 Standard, us-west-1"},
		{"elsewhere", jsonconfig.Obj{"aws_region": "ap-south-1"}, true, 0.005, "AWS S3 Standard, ap-south-1 (at us-east-1's prices)"},
		{"not_aws", jsonconfig.Obj{"hostname": "s3.example"}, false, 0, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			conf := jsonconfig.Obj{
				"aws_access_key":        "key",
				"aws_secret_access_key": "secret",
				"bucket":                "b",
				"skipStartupCheck":      true,
			}
			for k, v := range tt.conf {
				conf[k] = v
			}
			// The cache in front of it passes its pricing through.
			sto, err := newFromConfigWithTransport(nil, conf, &listingS3{})
			if err != nil {
				t.Fatal(err)
			}
			p, ok := blobserver.PricingOf(sto)
			if ok != tt.wantOK || p.ListPer1000 != tt.wantList || p.Desc != tt.wantDesc {
				t.Errorf("pricing = %+v, %v; want list %v, %q, %v", p, ok, tt.wantList, tt.wantDesc, tt.wantOK)
			}
		})
	}
}
