/*
Copyright 2011 Google Inc.

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
            "aws_access_key": "...",
            "aws_secret_access_key": "...",
            "skipStartupCheck": false
          }
     },

*/
package s3

import (
	"fmt"
	"log"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/memory"
	"camlistore.org/pkg/misc/amazon/s3"

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
	s3Client *s3.Client
	bucket   string
	// optional "directory" where the blobs are stored, instead of at the root of the bucket.
	// S3 is actually flat, which in effect just means that all the objects should have this
	// dirPrefix as a prefix of their key.
	// If non empty, it should be a slash separated path with a trailing slash and no starting
	// slash.
	dirPrefix string
	hostname  string
	cache     *memory.Storage // or nil for no cache
}

func (s *s3Storage) String() string {
	if s.dirPrefix != "" {
		return fmt.Sprintf("\"s3\" blob storage at host %q, bucket %q, directory %q", s.hostname, s.bucket, s.dirPrefix)
	}
	return fmt.Sprintf("\"s3\" blob storage at host %q, bucket %q", s.hostname, s.bucket)
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	hostname := config.OptionalString("hostname", "s3.amazonaws.com")
	cacheSize := config.OptionalInt64("cacheSize", 32<<20)
	client := &s3.Client{
		Auth: &s3.Auth{
			AccessKey:       config.RequiredString("aws_access_key"),
			SecretAccessKey: config.RequiredString("aws_secret_access_key"),
			Hostname:        hostname,
		},
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
	sto := &s3Storage{
		s3Client:  client,
		bucket:    bucket,
		dirPrefix: dirPrefix,
		hostname:  hostname,
	}
	skipStartupCheck := config.OptionalBool("skipStartupCheck", false)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if cacheSize != 0 {
		sto.cache = memory.NewCache(cacheSize)
	}
	if !skipStartupCheck {
		_, err := client.ListBucket(sto.bucket, "", 1)
		if serr, ok := err.(*s3.Error); ok {
			if serr.AmazonCode == "NoSuchBucket" {
				return nil, fmt.Errorf("Bucket %q doesn't exist.", sto.bucket)
			}

			// This code appears when the hostname has dots in it:
			if serr.AmazonCode == "PermanentRedirect" {
				loc, lerr := client.BucketLocation(sto.bucket)
				if lerr != nil {
					return nil, fmt.Errorf("Wrong server for bucket %q; and error determining bucket's location: %v", sto.bucket, lerr)
				}
				client.Auth.Hostname = loc
				_, err = client.ListBucket(sto.bucket, "", 1)
				if err == nil {
					log.Printf("Warning: s3 server should be %q, not %q. Change config file to avoid start-up latency.", client.Auth.Hostname, hostname)
				}
			}

			// This path occurs when the user set the
			// wrong server, or didn't set one at all, but
			// the bucket doesn't have dots in it:
			if serr.UseEndpoint != "" {
				// UseEndpoint will be e.g. "brads3test-ca.s3-us-west-1.amazonaws.com"
				// But we only want the "s3-us-west-1.amazonaws.com" part.
				client.Auth.Hostname = strings.TrimPrefix(serr.UseEndpoint, sto.bucket+".")
				_, err = client.ListBucket(sto.bucket, "", 1)
				if err == nil {
					log.Printf("Warning: s3 server should be %q, not %q. Change config file to avoid start-up latency.", client.Auth.Hostname, hostname)
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("Error listing bucket %s: %v", sto.bucket, err)
		}
	}
	return sto, nil
}

func init() {
	blobserver.RegisterStorageConstructor("s3", blobserver.StorageConstructor(newFromConfig))
}
