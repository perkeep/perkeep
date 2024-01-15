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
	"fmt"
	"net/http"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/blobserver/proxycache"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
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
	client s3iface.S3API
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
	s3Cfg := aws.NewConfig().WithCredentials(credentials.NewStaticCredentials(
		config.RequiredString("aws_access_key"),
		config.RequiredString("aws_secret_access_key"),
		"",
	))
	if hostname != "" {
		s3Cfg.WithEndpoint(hostname)
	}
	s3Cfg.WithRegion(region)
	if transport != nil {
		httpClient := *http.DefaultClient
		httpClient.Transport = transport
		s3Cfg.WithHTTPClient(&httpClient)
	}
	awsSession, err := session.NewSession(s3Cfg)
	if err != nil {
		return nil, err
	}

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

	ctx := context.TODO() // TODO: 5 min timeout or something?
	if !skipStartupCheck {
		info, err := normalizeBucketLocation(ctx, awsSession, hostname, bucket, region)
		if err != nil {
			return nil, err
		}
		awsSession.Config.WithRegion(info.region)
		awsSession.Config.WithEndpoint(info.endpoint)
		if !info.isAWS {
			awsSession.Config.WithS3ForcePathStyle(true)
		}
	} else {
		// safer default if we can't determine more info
		awsSession.Config.WithS3ForcePathStyle(true)
	}

	sto := &s3Storage{
		client:    s3.New(awsSession),
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

// isNotFound checks for s3 errors which indicate the object doesn't exist.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if aerr, ok := err.(awserr.Error); ok {
		return aerr.Code() == s3.ErrCodeNoSuchKey ||
			// Check 'NotFound' as well because it's returned for some requests, even
			// though the API model doesn't include it (hence why there isn't an
			// 's3.ErrCodeNotFound' for comparison)
			aerr.Code() == "NotFound"
	}
	return false
}
