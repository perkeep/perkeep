/*
Copyright 2018 The Perkeep Authors

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
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyendpoints "github.com/aws/smithy-go/endpoints"
)

type s3EPResolver struct {
	Endpoint string
	endpoint smithyendpoints.Endpoint
}

// ResolveEndpoint attempts to resolve the endpoint with the provided options,
// returning the endpoint if found. Otherwise an error is returned.
func (res *s3EPResolver) ResolveEndpoint(ctx context.Context, params s3.EndpointParameters) (
	smithyendpoints.Endpoint, error,
) {
	if res.Endpoint != "" {
		if res.endpoint.URI.Host == "" {
			if u, err := url.Parse(res.Endpoint); err != nil {
				return smithyendpoints.Endpoint{}, fmt.Errorf("parse %s: %w", res.Endpoint, err)

			} else {
				res.endpoint.URI = *u
			}
		}
		if b := aws.ToString(params.Bucket); b != "" {
			e := res.endpoint
			e.URI = *e.URI.JoinPath(b)
			return e, nil
		}
		return res.endpoint, nil
	}
	return s3.NewDefaultEndpointResolverV2().ResolveEndpoint(ctx, params)
}

// s3New create a new *s3.Client form config.
//
// Help: https://docs.aws.amazon.com/pdfs/sdk-for-go/v2/developer-guide/aws-sdk-go-v2-dg.pdf
func s3New(cfg aws.Config, region, endpoint string) *s3.Client {
	var ep s3.EndpointParameters
	if endpoint != "" {
		if !strings.Contains(endpoint, "://") {
			endpoint = "https://" + endpoint
		}
	}
	if region != "" {
		ep.Region = aws.String(region)
	}
	ep = ep.WithDefaults()

	return s3.NewFromConfig(cfg,
		s3.WithEndpointResolverV2(&s3EPResolver{Endpoint: endpoint}),
		// https://github.com/aws/aws-sdk-go-v2/issues/3020#issuecomment-2689117647
		func(o *s3.Options) {
			o.DisableLogOutputChecksumValidationSkipped = true
		},
	)
}
