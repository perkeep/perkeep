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
	"net/http"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/misc/amazon/s3"
)

type s3Storage struct {
	s3Client *s3.Client
	bucket   string
	hostname string
}

func (s *s3Storage) String() string {
	return fmt.Sprintf("\"s3\" blob storage at host %q, bucket %q", s.hostname, s.bucket)
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	hostname := config.OptionalString("hostname", "s3.amazonaws.com")
	client := &s3.Client{
		Auth: &s3.Auth{
			AccessKey:       config.RequiredString("aws_access_key"),
			SecretAccessKey: config.RequiredString("aws_secret_access_key"),
			Hostname:        hostname,
		},
		HTTPClient: http.DefaultClient,
	}
	sto := &s3Storage{
		s3Client: client,
		bucket:   config.RequiredString("bucket"),
		hostname: hostname,
	}
	skipStartupCheck := config.OptionalBool("skipStartupCheck", false)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !skipStartupCheck {
		// TODO: skip this check if a file
		// ~/.camli/.configcheck/sha1-("IS GOOD: s3: sha1(access key +
		// secret key)") exists and has recent time?
		buckets, err := client.Buckets()
		if err != nil {
			return nil, fmt.Errorf("Failed to get bucket list from S3: %v", err)
		}
		haveBucket := make(map[string]bool)
		for _, b := range buckets {
			haveBucket[b.Name] = true
		}
		if !haveBucket[sto.bucket] {
			return nil, fmt.Errorf("S3 bucket %q doesn't exist. Create it first at https://console.aws.amazon.com/s3/home", sto.bucket)
		}
	}
	return sto, nil
}

func init() {
	blobserver.RegisterStorageConstructor("s3", blobserver.StorageConstructor(newFromConfig))
}
