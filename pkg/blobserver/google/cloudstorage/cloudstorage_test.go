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

package cloudstorage

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"os"
	"path"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/storage"
	"go4.org/jsonconfig"
	"go4.org/oauthutil"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
)

var (
	configFile   = flag.String("config", "", "Path to a configuration JSON file. If given, all other configuration flags are ignored. Use \"camtool googinit --type=cloud\" to generate the auth parameters.")
	bucket       = flag.String("bucket", "", "Bucket name to use for testing. If empty, and --config blank too, testing is skipped. The bucket must be empty.")
	clientID     = flag.String("client_id", "", "OAuth2 client_id for testing")
	clientSecret = flag.String("client_secret", "", "OAuth2 client secret for testing")
	tokenCache   = flag.String("token_cache", ".tokencache", "Token cache file.")
	authCode     = flag.String("auth_code", "", "Use when instructed to do so, when the --token_cache is empty.")
)

func TestStorage(t *testing.T) {
	testStorage(t, "")
}

func TestStorageWithBucketDir(t *testing.T) {
	testStorage(t, "/bl/obs/")
}

type Config struct {
	Auth   AuthConfig `json:"auth"`
	Bucket string     `json:"bucket"`
}

type AuthConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
}

func testStorage(t *testing.T, bucketDir string) {
	if *bucket == "" && *configFile == "" {
		t.Skip("Skipping test without --bucket or --config flag")
	}
	var refreshToken string
	if *configFile != "" {
		data, err := os.ReadFile(*configFile)
		if err != nil {
			t.Fatalf("Error reading config file %v: %v", *configFile, err)
		}
		var conf Config
		if err := json.Unmarshal(data, &conf); err != nil {
			t.Fatalf("Error decoding config file %v: %v", *configFile, err)
		}
		*clientID = conf.Auth.ClientID
		*clientSecret = conf.Auth.ClientSecret
		refreshToken = conf.Auth.RefreshToken
		*bucket = conf.Bucket
	}
	if *bucket == "" {
		t.Fatal("bucket not provided in config file or as a flag.")
	}
	if *clientID == "" {
		if *clientSecret == "" {
			// Assume auto if neither the the clientID and clientSecret are specified
			// A service account key should be specified using the GOOGLE_APPLICATION_CREDENTIALS environment variable.
			*clientID = "auto"
		} else {
			t.Fatal("client ID and client secret must be specified together. Obtain from https://console.developers.google.com/ > Project > APIs & Auth > Credentials. Should be a 'native' or 'Installed application'")
		}
	}
	if *configFile == "" {
		config := &oauth2.Config{
			Scopes:       []string{storage.ScopeReadWrite},
			Endpoint:     google.Endpoint,
			ClientID:     *clientID,
			ClientSecret: *clientSecret,
			RedirectURL:  oauthutil.TitleBarRedirectURL,
		}
		if !metadata.OnGCE() && *clientID != "auto" {
			token, err := oauth2.ReuseTokenSource(nil,
				&oauthutil.TokenSource{
					Config:    config,
					CacheFile: *tokenCache,
					AuthCode: func() string {
						if *authCode == "" {
							t.Skipf("Re-run using --auth_code= with the value obtained from %s",
								config.AuthCodeURL("", oauth2.AccessTypeOffline, oauth2.ApprovalForce))
							return ""
						}
						return *authCode
					},
				}).Token()
			if err != nil {
				t.Fatalf("could not acquire token: %v", err)
			}
			refreshToken = token.RefreshToken
		}
	}

	bucketWithDir := path.Join(*bucket, bucketDir)

	storagetest.TestOpt(t, storagetest.Opts{
		New: func(t *testing.T) blobserver.Storage {
			sto, err := newFromConfig(nil, jsonconfig.Obj{
				"bucket": bucketWithDir,
				"auth": map[string]any{
					"client_id":     *clientID,
					"client_secret": *clientSecret,
					"refresh_token": refreshToken,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !testing.Short() {
				log.Printf("Warning: this test does many serial operations. Without the go test -short flag, this test will be very slow.")
			}
			// Bail if bucket is not empty
			ctx := context.Background()
			stor := sto.(*Storage)
			it := stor.client.Bucket(stor.bucket).Objects(ctx, nil)
			if _, err := it.Next(); err != iterator.Done {
				if err == nil {
					t.Fatalf("Refusing to run test: bucket %v is not empty", *bucket)
				}
				t.Fatalf("Error checking if bucket is empty: %v", err)
			}
			if bucketWithDir != *bucket {
				// Adding "a", and "c" objects in the bucket to make sure objects out of the
				// "directory" are not touched and have no influence.
				for _, key := range []string{"a", "c"} {
					w := stor.client.Bucket(stor.bucket).Object(key).NewWriter(ctx)
					if _, err := io.Copy(w, strings.NewReader(key)); err != nil {
						t.Fatalf("could not insert object %s in bucket %v: %v", key, sto.(*Storage).bucket, err)
					}
					if err := w.Close(); err != nil {
						t.Fatalf("could not insert object %s in bucket %v: %v", key, sto.(*Storage).bucket, err)
					}
				}
			}

			clearBucket := func(beforeTests bool) func() {
				return func() {
					var all []blob.Ref
					blobserver.EnumerateAll(ctx, sto, func(sb blob.SizedRef) error {
						t.Logf("Deleting: %v", sb.Ref)
						all = append(all, sb.Ref)
						return nil
					})
					if err := sto.RemoveBlobs(ctx, all); err != nil {
						t.Fatalf("Error removing blobs during cleanup: %v", err)
					}
					if beforeTests {
						return
					}
					if bucketWithDir != *bucket {
						// checking that "a" and "c" at the root were left untouched.
						for _, key := range []string{"a", "c"} {
							rc, err := stor.client.Bucket(stor.bucket).Object(key).NewReader(ctx)
							if err != nil {
								t.Fatalf("could not find object %s after tests: %v", key, err)
							}
							if _, err := io.Copy(io.Discard, rc); err != nil {
								t.Fatalf("could not find object %s after tests: %v", key, err)
							}
							if err := stor.client.Bucket(stor.bucket).Object(key).Delete(ctx); err != nil {
								t.Fatalf("could not remove object %s after tests: %v", key, err)
							}

						}
					}
				}
			}
			clearBucket(true)()
			t.Cleanup(clearBucket(false))
			return sto
		},
	})
}
