/*
Copyright 2016 The Camlistore Authors

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

package b2

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"path"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"

	"go4.org/jsonconfig"
	"golang.org/x/net/context"
)

var (
	accountID = flag.String("account-id", "", "B2 Account ID for testing")
	appKey    = flag.String("application-key", "", "B2 Application Key for testing")
)

func TestStorage(t *testing.T) {
	testStorage(t, "")
}

func TestStorageWithBucketDir(t *testing.T) {
	testStorage(t, "/bl/obs/")
}

func testStorage(t *testing.T, bucketDir string) {
	if *accountID == "" && *appKey == "" {
		t.Skip("Skipping test without --account-id or --application-key flag")
	}

	rn := rand.New(rand.NewSource(time.Now().UnixNano())).Intn(1000000)
	bucket := fmt.Sprintf("camli-test-%d", rn)
	bucketWithDir := path.Join(bucket, bucketDir)

	storagetest.TestOpt(t, storagetest.Opts{
		New: func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
			sto, err := newFromConfig(nil, jsonconfig.Obj{
				"bucket": bucketWithDir,
				"auth": map[string]interface{}{
					"account_id":      *accountID,
					"application_key": *appKey,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !testing.Short() {
				log.Printf("Warning: this test does many serial operations. Without the go test -short flag, this test will be very slow.")
			}
			if bucketWithDir != bucket {
				// Adding "a", and "c" objects in the bucket to make sure objects out of the
				// "directory" are not touched and have no influence.
				for _, key := range []string{"a", "c"} {
					if _, err := sto.(*Storage).b.Upload(strings.NewReader(key), key, ""); err != nil {
						t.Fatalf("could not insert object %s in bucket %v: %v", key, sto.(*Storage).b.Name, err)
					}
				}
			}

			clearBucket := func(beforeTests bool) func() {
				return func() {
					var all []blob.Ref
					blobserver.EnumerateAll(context.TODO(), sto, func(sb blob.SizedRef) error {
						t.Logf("Deleting: %v", sb.Ref)
						all = append(all, sb.Ref)
						return nil
					})
					if err := sto.RemoveBlobs(all); err != nil {
						t.Fatalf("Error removing blobs during cleanup: %v", err)
					}
					if beforeTests {
						return
					}
					if bucketWithDir != bucket {
						// checking that "a" and "c" at the root were left untouched.
						for _, key := range []string{"a", "c"} {
							fi, err := sto.(*Storage).b.GetFileInfoByName(key)
							if err != nil {
								t.Fatalf("could not remove object %s after tests: %v", key, err)
							}
							if err := sto.(*Storage).cl.DeleteFile(fi.ID, fi.Name); err != nil {
								t.Fatalf("could not remove object %s after tests: %v", key, err)
							}

						}
					}
					if err := sto.(*Storage).b.Delete(); err != nil {
						t.Fatalf("could not remove bucket %s after tests: %v", sto.(*Storage).b.Name, err)
					}
				}
			}
			clearBucket(true)()
			return sto, clearBucket(false)
		},
	})
}
