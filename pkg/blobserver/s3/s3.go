/*
Copyright 2011 The Perkeep Authors

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

/*
Package s3 registers the "s3" blobserver storage type, storing
blobs in an Amazon Web Services' S3 storage bucket.

Example low-level config:

	"/r1/": {
	    "handler": "storage-s3",
	    "handlerArgs": {
	       "bucket": "foo",
	       "aws_region": "us-east-1",
	       "aws_access_key": "...",
	       "aws_secret_access_key": "...",
	       "skipStartupCheck": false
	     }
	},
*/
package s3 // import "perkeep.org/pkg/blobserver/s3"

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/blobserver/proxycache"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"go4.org/fault"
	"go4.org/jsonconfig"
)

var (
	_ blob.SubFetcher               = (*s3Storage)(nil)
	_ blobserver.MaxEnumerateConfig = (*s3Storage)(nil)
)

var (
	faultReceive   = fault.NewInjector("s3_receive")
	faultEnumerate = fault.NewInjector("s3_enumerate")
	faultStat      = fault.NewInjector("s3_stat")
	faultGet       = fault.NewInjector("s3_get")
)

type s3Storage struct {
	client *s3.Client
	bucket string
	// optional "directory" where the blobs are stored, instead of at the root of the bucket.
	// S3 is actually flat, which in effect just means that all the objects should have this
	// dirPrefix as a prefix of their key.
	// If non empty, it should be a slash separated path with a trailing slash and no starting
	// slash.
	dirPrefix string
	// hostname indicates the hostname of the server providing an S3 compatible endpoint.
	// It should not be set for AWS's S3 since the correct endpoint will be
	// automatically identified based on the bucket name (and, if provided, the
	// 'aws_region' low-level config option).
	hostname string
}

func (sto *s3Storage) String() string {
	if sto.dirPrefix != "" {
		return fmt.Sprintf("\"S3\" blob storage at host %q, bucket %q, directory %q", sto.hostname, sto.bucket, sto.dirPrefix)
	}
	return fmt.Sprintf("\"S3\" blob storage at host %q, bucket %q", sto.hostname, sto.bucket)
}

func newFromConfig(l blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	return newFromConfigWithTransport(l, config, nil)
}

// newFromConfigWithTransport constructs a s3 blobserver using the given
// transport for all s3 requests.  The transport may be set to 'nil' to use a
// default transport.
// This is used for unit tests.
func newFromConfigWithTransport(_ blobserver.Loader, config jsonconfig.Obj, transport http.RoundTripper) (blobserver.Storage, error) {
	hostname := config.OptionalString("hostname", "")
	region := config.OptionalString("aws_region", "us-east-1")

	cacheSize := config.OptionalInt64("cacheSize", 32<<20)
	accessKey := config.RequiredString("aws_access_key")
	secretKey := config.RequiredString("aws_secret_access_key")

	bucket := config.RequiredString("bucket")
	var dirPrefix string
	if parts := strings.SplitN(bucket, "/", 2); len(parts) > 1 {
		dirPrefix = parts[1]
		bucket = parts[0]
	}
	if dirPrefix != "" && !strings.HasSuffix(dirPrefix, "/") {
		dirPrefix += "/"
	}

	skipStartupCheck := config.OptionalBool("skipStartupCheck", false)
	if err := config.Validate(); err != nil {
		return nil, err
	}

	if strings.Contains(hostname, "://") {
		return nil, fmt.Errorf("invalid s3 hostname %q: must not include URI scheme", hostname)
	}

	opts := s3.Options{
		Credentials: credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		Region:      region,
		// Only send and verify checksums when the API requires them.
		// Many S3-compatible services don't support the newer
		// checksum headers that the SDK otherwise sends by default.
		RequestChecksumCalculation:                aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation:                aws.ResponseChecksumValidationWhenRequired,
		DisableLogOutputChecksumValidationSkipped: true,
	}
	if transport != nil {
		opts.HTTPClient = &http.Client{Transport: transport}
	}
	// For AWS, any configured hostname (such as "s3-us-west-2.amazonaws.com")
	// is ignored and the SDK picks the endpoint based on the bucket's region.
	isAWS := hostname == "" || isAWSHostname(hostname)
	if !isAWS {
		opts.BaseEndpoint = aws.String("https://" + hostname)
		opts.UsePathStyle = true
	}
	client := s3.New(opts)

	if isAWS && !skipStartupCheck {
		ctx := context.TODO() // TODO: 5 min timeout or something?
		bucketRegion, err := getBucketRegion(ctx, client, bucket)
		if err != nil {
			return nil, err
		}
		if bucketRegion != region {
			opts.Region = bucketRegion
			client = s3.New(opts)
		}
	}

	sto := &s3Storage{
		client:    client,
		bucket:    bucket,
		dirPrefix: dirPrefix,
		hostname:  hostname,
	}

	if cacheSize != 0 {
		// This has two layers of LRU caching (proxycache and memory).
		// We make the outer one 4x the size so that it doesn't evict from the
		// underlying one when it's about to perform its own eviction.
		return proxycache.New(cacheSize<<2, memory.NewCache(cacheSize), sto), nil
	}
	return sto, nil
}

func init() {
	blobserver.RegisterStorageConstructor("s3", blobserver.StorageConstructor(newFromConfig))
	blobserver.RegisterStorageConstructor("b2", blobserver.StorageConstructor(newFromConfig))
}

// isAWSHostname reports whether hostname is one of AWS's own S3 endpoints,
// as opposed to that of an S3-compatible service.
func isAWSHostname(hostname string) bool {
	host, _, err := net.SplitHostPort(hostname)
	if err != nil {
		host = hostname
	}
	return host == "amazonaws.com" || strings.HasSuffix(host, ".amazonaws.com")
}

// getBucketRegion returns the AWS region that bucket is in.
func getBucketRegion(ctx context.Context, client *s3.Client, bucket string) (string, error) {
	res, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &bucket})
	if err == nil {
		if r := aws.ToString(res.BucketRegion); r != "" {
			return r, nil
		}
		return client.Options().Region, nil
	}
	// AWS includes the bucket's region in its response even when it
	// rejects a request for being sent to the wrong region.
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) {
		if r := re.Response.Header.Get("X-Amz-Bucket-Region"); r != "" {
			return r, nil
		}
	}
	return "", fmt.Errorf("s3: could not determine region of bucket %q: %w", bucket, err)
}

// isNotFound checks for s3 errors which indicate the object doesn't exist.
func isNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		// GetObject returns "NoSuchKey", but HEAD responses have no body,
		// so HeadObject's error code is just "NotFound".
		return apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound"
	}
	return false
}
