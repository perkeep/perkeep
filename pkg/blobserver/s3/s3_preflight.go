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
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

type bucketInfo struct {
	endpoint string
	region   string
	isAWS    bool
}

// normalizeBucketLocation determines the best endpoint and region to use for
// the given endpoint + bucket. It performs preflight checks to determine the
// appropriate region and endpoint.
// This is required for backwards compatibility. Normally, using
// s3manager.GetBucketRegion would be sufficient for common mistakes, but the
// initial implementation of the s3 blobserver also allowed inputing the s3
// website hostname of a bucket to configure it.
// The AWS SDK provides no way to determine the correct bucket+endpoint from
// this url because, frankly, it's not meant to be used in the way this
// blobserver initially did (where it's punned on to be both an api endpoint
// and the s3-website hostname).
//
// For a concrete example, this bucket might be given an input of
// "test.s3-us-west-1.amazonaws.com", and it would return
// endpoint=s3.us-west-1.amazonaws.com, isAWS=true, region=us-west-1 (assuming,
// of course, the bucket is in us-west-1).
func normalizeBucketLocation(ctx context.Context, cfg client.ConfigProvider, endpoint string, bucket string, configRegion string) (bucketInfo, error) {
	if strings.HasPrefix(endpoint, "https://") || strings.HasPrefix(endpoint, "http://") {
		return bucketInfo{}, fmt.Errorf("invalid s3 endpoint: must not include uri scheme")
	}

	svc := s3.New(cfg)
	endpoint, region, err := determineEndpoint(ctx, svc, endpoint, bucket, configRegion)
	if err != nil {
		return bucketInfo{}, err
	}
	if region != "" {
		svc.Config.WithRegion(region)
	}
	isAWS, endpoint, err := endpointIsOfficial(endpoint)
	if err != nil {
		return bucketInfo{}, err
	}
	// if isAWS is false, the target also supports AWS s3 API
	if !isAWS {
		return bucketInfo{
			endpoint: endpoint,
			isAWS:    isAWS,
			region:   region,
		}, nil
	}
	// the endpoint should be corrected before being used to determine a region
	// or else the region request can fail spuriously
	svc.Config.WithEndpoint(endpoint)
	region, err = s3manager.GetBucketRegion(ctx, cfg, bucket, region)
	// ignore errors if it's not an official endpoint since it's not unusual for
	// non-AWS S3 endpoints to not support the get bucket location API
	if isAWS && err != nil {
		return bucketInfo{}, err
	}
	return bucketInfo{
		endpoint: endpoint,
		isAWS:    isAWS,
		region:   region,
	}, nil
}

// determineEndpoint makes a preflight request to AWS against the provided
// endpoint+bucket+region to see if they ask us to use a different endpoint
// instead.
// This can occur if either the endpoint, region, or pair of them is incorrect.
// It returns the endpoint and region (in that order) that should be used
// according to AWS.
func determineEndpoint(ctx context.Context, svc s3iface.S3API, endpoint, bucket, region string) (string, string, error) {
	req, _ := svc.ListObjectsV2Request(&s3.ListObjectsV2Input{
		Bucket:  &bucket,
		MaxKeys: aws.Int64(1),
	})
	if region != "" {
		req.ClientInfo.SigningRegion = region
	}
	req.Config.S3ForcePathStyle = aws.Bool(true)
	req.DisableFollowRedirects = true
	req.SetContext(ctx)

	var determinedEndpoint string
	req.Handlers.UnmarshalError.PushFront(func(r *request.Request) {
		if r.HTTPResponse.StatusCode != http.StatusMovedPermanently {
			return
		}
		var b bytes.Buffer
		if _, err := io.Copy(&b, r.HTTPResponse.Body); err != nil {
			r.Error = fmt.Errorf("error reading body: %v", err)
			return
		}

		type endpointErr struct {
			Endpoint string `xml:"Endpoint"`
		}

		var epErr endpointErr
		err := xml.NewDecoder(&b).Decode(&epErr)
		if err != nil {
			r.Error = err
			return
		}
		determinedEndpoint = epErr.Endpoint
		r.HTTPResponse.Body = io.NopCloser(&b)
	})
	err := req.Send()
	if determinedEndpoint == "" && err != nil {
		if region == "" {
			// only recurse once; region == "" only happens with a call depth of 1 here
			if newRegion := regionFromMalformedAuthHeaderError(err); newRegion != "" {
				// retry with the correct region
				return determineEndpoint(ctx, svc, endpoint, bucket, newRegion)
			}
		}
		return "", "", fmt.Errorf("s3: could not determine endpoint: %v", err)
	}
	// this indicates the UnmarshalError handler wasn't called, and since the
	// above branch didn't happen there wasn't an error. That means our current
	// endpoint + region combo works fine.
	if determinedEndpoint == "" {
		return endpoint, region, nil
	}
	// the 'Endpoint' object needlessly includes the bucket name at the beginning
	determinedEndpoint = strings.TrimPrefix(determinedEndpoint, bucket+".")
	return determinedEndpoint, region, nil
}

// endpointIsOfficial determines if the endpoint is an aws one.
// If it is an aws one, it will return the aws canonical endpoint as well,
// otherwise it will return the endpoint unmodified.
// For example, "s3.amazonaws.com" is the canonical us-east-1 endpoint,
// 's3.us-west-2.amazonaws.com' is the canonical us-west-2 endpoint, and so on.
// There are additional official endpoints, such as
// 's3-us-west-2.amazonaws.com', but these endpoints are not exposed by the
// SDK's public interfaces, so we can't test against those.
// For more information on these endpoints, see AWS's list:
// https://docs.aws.amazon.com/general/latest/gr/rande.html#s3_region
// In any case where a user has one of these endpoints set, it will not be
// recognized as an AWS endpoint correctly, but should still function just fine
// since this detection is for the purpose of relaxing some constraints on
// non-AWS endpoints, so it's perfectly fine to have false negatives.
func endpointIsOfficial(endpoint string) (bool, string, error) {
	for _, partition := range endpoints.DefaultPartitions() {
		for _, region := range partition.Regions() {
			s3Endpoint, err := region.ResolveEndpoint(endpoints.S3ServiceID) //lint:ignore SA1019 TODO fix this and caller
			if err != nil {
				// S3 isn't available in this region yet; unlikely to ever happen
				continue
			}
			p, err := url.Parse(s3Endpoint.URL)
			if err != nil {
				return false, endpoint, err
			}

			if strings.HasSuffix(endpoint, p.Host) {
				return true, p.Host, nil
			}
		}
	}
	return false, endpoint, nil
}

var malformedAuthHeaderMessageRegexp = regexp.MustCompile("region '[^']+' is wrong; expecting '([^']+)'")

// backwards compatibility: perkeep used to use v2 signing. v2 signing knows
// nothing about regions, so users could configure their perkeep instance with
// region = us-east-1, but endpoint = us-west-2.s3.amazonaws.com, and things
// would work before.
// In an attempt to retain that functionality, we parse the error message
// telling us we hit the wrong region and auto-correct it.
func regionFromMalformedAuthHeaderError(err error) string {
	if aerr, ok := err.(awserr.Error); ok && aerr.Code() == "AuthorizationHeaderMalformed" {
		matches := malformedAuthHeaderMessageRegexp.FindStringSubmatch(aerr.Message())
		if len(matches) == 2 {
			return matches[1]
		}
		log.Printf("s3: got AuthorizationHeaderMalformed, but couldn't parse message: %v", aerr.Message())
	}
	return ""
}
