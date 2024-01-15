/*
Copyright 2014 The Perkeep Authors

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
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"perkeep.org/internal/httputil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/schema"

	"go4.org/jsonconfig"
)

var (
	key          = flag.String("s3_key", "", "AWS access Key ID")
	secret       = flag.String("s3_secret", "", "AWS access secret")
	bucket       = flag.String("s3_bucket", "", "Bucket name to use for testing. If empty, testing is skipped. If non-empty, it must begin with 'camlistore-' and end in '-test' and have zero items in it.")
	flagTestData = flag.String("testdata", "", "Optional directory containing some files to write to the bucket, for additional tests.")
)

var ctxbg = context.Background()

func TestS3(t *testing.T) {
	testStorage(t, "")
}

// TestS3Endpoints is an integ test for the various forms of s3 bucket urls.
// It verifies that a blobserver is instantiated without error using those endpoints.
func TestS3Endpoints(t *testing.T) {
	if *bucket == "" || *key == "" || *secret == "" {
		t.Skip("Skipping test because at least one of -s3_key, -s3_secret, or -s3_bucket flags has not been provided.")
	}

	hostnames := []string{
		"s3.amazonaws.com",            // us-east-1
		"s3-external-1.amazonaws.com", // also us-east-1
		"s3.us-west-2.amazonaws.com",  // us-west-2
		"s3-us-west-2.amazonaws.com",  // also us-west-2
	}

	for i := range hostnames {
		hostname := hostnames[i]
		t.Run(hostname, func(t *testing.T) {
			_, err := newFromConfig(nil, jsonconfig.Obj{
				"aws_access_key":        *key,
				"aws_secret_access_key": *secret,
				"bucket":                *bucket,
				"hostname":              hostname,
			})
			if err != nil {
				t.Errorf("error constructing blobserver: %v", err)
			}
		})
	}

}

func TestS3WithBucketDir(t *testing.T) {
	testStorage(t, "/bl/obs/")
}

func TestS3WriteFiles(t *testing.T) {
	if *flagTestData == "" {
		t.Skipf("testdata dir not specified, skipping test.")
	}
	sto, err := newFromConfig(nil, jsonconfig.Obj{
		"aws_access_key":        *key,
		"aws_secret_access_key": *secret,
		"bucket":                *bucket,
	})
	if err != nil {
		t.Fatalf("newFromConfig error: %v", err)
	}
	dir, err := os.Open(*flagTestData)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		f, err := os.Open(filepath.Join(*flagTestData, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close() // assuming there aren't that many files.
		if _, err := schema.WriteFileFromReaderWithModTime(ctxbg, sto, name, time.Now(), f); err != nil {
			t.Fatalf("Error while writing %v to S3: %v", name, err)
		}
		t.Logf("Wrote %v successfully to S3", name)
	}
}

func testStorage(t *testing.T, bucketDir string) {
	if *bucket == "" || *key == "" || *secret == "" {
		t.Skip("Skipping test because at least one of -s3_key, -s3_secret, or -s3_bucket flags has not been provided.")
	}
	if !strings.HasPrefix(*bucket, "camlistore-") || !strings.HasSuffix(*bucket, "-test") {
		t.Fatalf("bogus bucket name %q; must begin with 'camlistore-' and end in '-test'", *bucket)
	}

	bucketWithDir := path.Join(*bucket, bucketDir)
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		sto, err := newFromConfig(nil, jsonconfig.Obj{
			"aws_access_key":        *key,
			"aws_secret_access_key": *secret,
			"bucket":                bucketWithDir,
			"cacheSize":             float64(0),
		})
		if err != nil {
			t.Fatalf("newFromConfig error: %v", err)
		}
		if !testing.Short() {
			log.Printf("Warning: this test does many serial operations. Without the go test -short flag, this test will be very slow.")
		}

		if bucketWithDir != *bucket {
			// Adding "a", and "c" objects in the bucket to make sure objects out of the
			// "directory" are not touched and have no influence.
			for _, key := range []string{"a", "c"} {
				if err != nil {
					t.Fatalf("could not insert object %s in bucket %v: %v", key, sto.(*s3Storage).bucket, err)
				}
				if _, err := sto.(*s3Storage).client.PutObject(&s3.PutObjectInput{
					Bucket: &sto.(*s3Storage).bucket,
					Key:    aws.String(key),
					Body:   strings.NewReader(key),
				}); err != nil {
					t.Fatalf("could not insert object %s in bucket %v: %v", key, sto.(*s3Storage).bucket, err)
				}
			}
		}
		clearBucket := func(beforeTests bool) func() {
			return func() {
				var all []blob.Ref
				blobserver.EnumerateAll(ctxbg, sto, func(sb blob.SizedRef) error {
					t.Logf("Deleting: %v", sb.Ref)
					all = append(all, sb.Ref)
					return nil
				})
				if err := sto.RemoveBlobs(ctxbg, all); err != nil {
					t.Fatalf("Error removing blobs during cleanup: %v", err)
				}
				if beforeTests {
					return
				}
				if bucketWithDir != *bucket {
					// checking that "a" and "c" at the root were left untouched.
					for _, key := range []string{"a", "c"} {
						if _, err := sto.(*s3Storage).client.GetObject(&s3.GetObjectInput{
							Bucket: &sto.(*s3Storage).bucket,
							Key:    aws.String(key),
						}); err != nil {
							t.Fatalf("could not find object %s after tests: %v", key, err)
						}
						if _, err := sto.(*s3Storage).client.DeleteObject(&s3.DeleteObjectInput{
							Bucket: &sto.(*s3Storage).bucket,
							Key:    aws.String(key),
						}); err != nil {
							t.Fatalf("could not remove object %s after tests: %v", key, err)
						}
					}
				}
			}
		}
		clearBucket(true)()
		t.Cleanup(clearBucket(false))
		return sto
	})
}

func TestS3EndpointRedirect(t *testing.T) {
	transport, err := httputil.NewRegexpFakeTransport([]*httputil.Matcher{
		{
			URLRegex: regexp.QuoteMeta("https://s3.amazonaws.com/mock_bucket/?location") + ".*",
			Fn: func() *http.Response {
				return &http.Response{
					Status:     "301 Moved Permanently",
					StatusCode: http.StatusMovedPermanently,
					Header: http.Header(map[string][]string{
						"X-Amz-Bucket-Region": []string{"us-east-1"},
					}),
					Body: io.NopCloser(strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>
<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-west-1</LocationConstraint>`)),
				}
			},
		},
		{
			URLRegex: regexp.QuoteMeta("https://s3.amazonaws.com/mock_bucket") + ".*",
			Fn: func() *http.Response {
				return &http.Response{
					Status:     "301 Moved Permanently",
					StatusCode: http.StatusMovedPermanently,
					Header: http.Header(map[string][]string{
						"X-Amz-Bucket-Region": []string{"us-east-1"},
					}),
					Body: io.NopCloser(strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>PermanentRedirect</Code><Message>The bucket you are attempting to access must be addressed using the specified endpoint. Please send all future requests to this endpoint.</Message><Bucket>mock_bucket</Bucket><Endpoint>mock_bucket.s3.amazonaws.com</Endpoint><RequestId>123</RequestId><HostId>abc</HostId></Error>`)),
				}
			},
		},
		{
			URLRegex: regexp.QuoteMeta("https://s3-us-west-1.amazonaws.com/mock_bucket") + ".*",
			Fn: func() *http.Response {
				return &http.Response{
					Status:     "200 OK",
					StatusCode: 200,
					Body: io.NopCloser(strings.NewReader(`
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>mock_bucket</Name><Prefix></Prefix><MaxKeys>1</MaxKeys><Marker></Marker><IsTruncated>false</IsTruncated><Contents></Contents></ListBucketResult>
					`)),
				}
			},
		},
	})
	if err != nil {
		panic(err)
	}

	_, err = newFromConfigWithTransport(nil, jsonconfig.Obj{
		"aws_access_key":        "key",
		"aws_secret_access_key": "secret",
		"bucket":                "mock_bucket",
	}, transport)
	if err != nil {
		t.Fatalf("newFromConfig error: %v", err)
	}
}

func TestNonS3Endpoints(t *testing.T) {
	testValidHostnames := []string{
		"localhost",
		"s3-but-not-amazon.notaws.com",
		"example.com:443",
	}
	testInvalidHostnames := []string{
		"http://localhost",
	}

	transport := func(hostname string) http.RoundTripper {
		transport, err := httputil.NewRegexpFakeTransport([]*httputil.Matcher{
			{
				URLRegex: regexp.QuoteMeta("https://"+hostname+"/mock_bucket") + ".*",
				Fn: func() *http.Response {
					return &http.Response{
						Status:     "200 OK",
						StatusCode: 200,
						Body: io.NopCloser(strings.NewReader(`
		<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>mock_bucket</Name><Prefix></Prefix><MaxKeys>1</MaxKeys><Marker></Marker><IsTruncated>false</IsTruncated><Contents></Contents></ListBucketResult>
							`)),
					}
				},
			},
		})

		if err != nil {
			panic(err)
		}

		return transport
	}

	for _, hostname := range testValidHostnames {
		t.Run(hostname, func(t *testing.T) {
			_, err := newFromConfigWithTransport(nil, jsonconfig.Obj{
				"aws_access_key":        "key",
				"aws_secret_access_key": "secret",
				"bucket":                "mock_bucket",
				"hostname":              hostname,
			}, transport(hostname))
			if err != nil {
				t.Errorf("newFromConfig error: %v", err)
			}
		})
	}

	for _, hostname := range testInvalidHostnames {
		t.Run(hostname, func(t *testing.T) {
			_, err := newFromConfigWithTransport(nil, jsonconfig.Obj{
				"aws_access_key":        "key",
				"aws_secret_access_key": "secret",
				"bucket":                "mock_bucket",
				"hostname":              hostname,
			}, transport(hostname))
			if err == nil {
				t.Error("expected error, didn't get one")
			}
		})
	}

}
