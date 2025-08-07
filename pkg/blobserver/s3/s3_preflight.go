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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyendpoints "github.com/aws/smithy-go/endpoints"
)

type s3EPResolver struct {
	Resolver   s3.EndpointResolverV2
	Parameters *s3.EndpointParameters
}

// ResolveEndpoint attempts to resolve the endpoint with the provided options,
// returning the endpoint if found. Otherwise an error is returned.
func (res s3EPResolver) ResolveEndpoint(ctx context.Context, params s3.EndpointParameters) (
	smithyendpoints.Endpoint, error,
) {
	if res.Parameters != nil {
		params = *res.Parameters
	}
	return res.Resolver.ResolveEndpoint(ctx, params)
}

func s3New(cfg aws.Config, region, endpoint string) *s3.Client {
	var ep s3.EndpointParameters
	ep.ForcePathStyle = aws.Bool(true)
	if endpoint != "" {
		ep.Endpoint = aws.String(endpoint)
	}
	if region != "" {
		ep.Region = aws.String(region)
	}
	ep = ep.WithDefaults()

	return s3.NewFromConfig(cfg, s3.WithEndpointResolverV2(s3EPResolver{Resolver: s3.NewDefaultEndpointResolverV2(), Parameters: &ep}))
}
