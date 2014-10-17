/*
Copyright 2014 The Camlistore Authors

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
	"flag"
	"log"
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/jsonconfig"
)

var (
	key    = flag.String("s3_key", "", "AWS access Key ID")
	secret = flag.String("s3_secret", "", "AWS access secret")
	bucket = flag.String("s3_bucket", "", "Bucket name to use for testing. If empty, testing is skipped. If non-empty, it must begin with 'camlistore-' and end in '-test' and have zero items in it.")
)

func TestS3(t *testing.T) {
	if *bucket == "" || *key == "" || *secret == "" {
		t.Skip("Skipping test because at least one of -s3_key, -s3_secret, or -s3_bucket flags has not been provided.")
	}
	if !strings.HasPrefix(*bucket, "camlistore-") || !strings.HasSuffix(*bucket, "-test") {
		t.Fatalf("bogus bucket name %q; must begin with 'camlistore-' and end in '-test'", *bucket)
	}
	storagetest.Test(t, func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
		sto, err := newFromConfig(nil, jsonconfig.Obj{
			"aws_access_key":        *key,
			"aws_secret_access_key": *secret,
			"bucket":                *bucket,
		})
		if err != nil {
			t.Fatalf("newFromConfig error: %v", err)
		}
		if !testing.Short() {
			log.Printf("Warning: this test does many serial operations. Without the go test -short flag, this test will be very slow.")
		}
		clearBucket := func() {
			var all []blob.Ref
			blobserver.EnumerateAll(context.New(), sto, func(sb blob.SizedRef) error {
				t.Logf("Deleting: %v", sb.Ref)
				all = append(all, sb.Ref)
				return nil
			})
			if err := sto.RemoveBlobs(all); err != nil {
				t.Fatalf("Error removing blobs during cleanup: %v", err)
			}
		}
		clearBucket()
		return sto, clearBucket
	})
}

func TestNextStr(t *testing.T) {
	tests := []struct {
		s, want string
	}{
		{"", ""},
		{"abc", "abd"},
		{"ab\xff", "ac\x00"},
		{"a\xff\xff", "b\x00\x00"},
		{"sha1-da39a3ee5e6b4b0d3255bfef95601890afd80709", "sha1-da39a3ee5e6b4b0d3255bfef95601890afd8070:"},
	}
	for _, tt := range tests {
		if got := nextStr(tt.s); got != tt.want {
			t.Errorf("nextStr(%q) = %q; want %q", tt.s, got, tt.want)
		}
	}
}
