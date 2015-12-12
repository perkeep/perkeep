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

package cloudstorage

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"path"
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/constants/google"
	"camlistore.org/pkg/googlestorage"
	"go4.org/jsonconfig"
	"golang.org/x/net/context"

	"go4.org/oauthutil"
	"golang.org/x/oauth2"
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
		data, err := ioutil.ReadFile(*configFile)
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
	if *clientID == "" || *clientSecret == "" {
		t.Fatal("client ID and client secret required. Obtain from https://console.developers.google.com/ > Project > APIs & Auth > Credentials. Should be a 'native' or 'Installed application'")
	}
	if *configFile == "" {
		config := &oauth2.Config{
			Scopes:       []string{googlestorage.Scope},
			Endpoint:     google.Endpoint,
			ClientID:     *clientID,
			ClientSecret: *clientSecret,
			RedirectURL:  oauthutil.TitleBarRedirectURL,
		}
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

	bucketWithDir := path.Join(*bucket, bucketDir)

	storagetest.TestOpt(t, storagetest.Opts{
		New: func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
			sto, err := newFromConfig(nil, jsonconfig.Obj{
				"bucket": bucketWithDir,
				"auth": map[string]interface{}{
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
			objs, err := sto.(*Storage).client.EnumerateObjects(*bucket, "", 1)
			if err != nil {
				t.Fatalf("Error checking if bucket is empty: %v", err)
			}
			if len(objs) != 0 {
				t.Fatalf("Refusing to run test: bucket %v is not empty", *bucket)
			}
			if bucketWithDir != *bucket {
				// Adding "a", and "c" objects in the bucket to make sure objects out of the
				// "directory" are not touched and have no influence.
				for _, key := range []string{"a", "c"} {
					err := sto.(*Storage).client.PutObject(
						&googlestorage.Object{Bucket: sto.(*Storage).bucket, Key: key},
						strings.NewReader(key))
					if err != nil {
						t.Fatalf("could not insert object %s in bucket %v: %v", key, sto.(*Storage).bucket, err)
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
					if bucketWithDir != *bucket {
						// checking that "a" and "c" at the root were left untouched.
						for _, key := range []string{"a", "c"} {
							if _, _, err := sto.(*Storage).client.GetObject(&googlestorage.Object{Bucket: sto.(*Storage).bucket,
								Key: key}); err != nil {
								t.Fatalf("could not find object %s after tests: %v", key, err)
							}
							if err := sto.(*Storage).client.DeleteObject(&googlestorage.Object{Bucket: sto.(*Storage).bucket, Key: key}); err != nil {
								t.Fatalf("could not remove object %s after tests: %v", key, err)
							}

						}
					}
				}
			}
			clearBucket(true)()
			return sto, clearBucket(false)
		},
	})
}
