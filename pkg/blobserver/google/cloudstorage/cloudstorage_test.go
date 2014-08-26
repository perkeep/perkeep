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
	"flag"
	"log"
	"strings"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
)

var (
	bucket       = flag.String("bucket", "", "Bucket name to use for testing. If empty, testing is skipped. If non-empty, it must begin with 'camlistore-' and end in '-test' and have zero items in it.")
	clientID     = flag.String("client_id", "", "OAuth2 client_id for testing")
	clientSecret = flag.String("client_secret", "", "OAuth2 client secret for testing")
	tokenCache   = flag.String("token_cache", ".tokencache", "Token cache file.")
	authCode     = flag.String("auth_code", "", "Use when instructed to do so, when the --token_cache is empty.")
)

func TestStorage(t *testing.T) {
	if *bucket == "" {
		t.Skip("Skipping test without --bucket flag")
	}
	if !strings.HasPrefix(*bucket, "camlistore-") || !strings.HasSuffix(*bucket, "-test") {
		t.Fatalf("bogus bucket name %q; must begin with 'camlistore-' and end in '-test'", *bucket)
	}
	if *clientID == "" || *clientSecret == "" {
		t.Fatal("--client_id and --client_secret required. Obtain from https://console.developers.google.com/ > Project > APIs & Auth > Credentials. Should be a 'native' or 'Installed application'")
	}

	tokenCache := oauth.CacheFile(*tokenCache)
	token, err := tokenCache.Token()
	if err != nil {
		config := &oauth.Config{
			// The client-id and secret should be for an "Installed Application" when using
			// the CLI. Later we'll use a web application with a callback.
			ClientId:     *clientID,
			ClientSecret: *clientSecret,
			Scope:        "https://www.googleapis.com/auth/devstorage.full_control",
			AuthURL:      "https://accounts.google.com/o/oauth2/auth",
			TokenURL:     "https://accounts.google.com/o/oauth2/token",
			RedirectURL:  "urn:ietf:wg:oauth:2.0:oob",
		}
		if *authCode != "" {
			tr := &oauth.Transport{
				Config: config,
			}
			token, err = tr.Exchange(*authCode)
			if err != nil {
				t.Fatalf("Error getting a token using auth code: %v", err)
			}
			tokenCache.PutToken(token)
		} else {
			t.Skipf("Re-run using --auth_code= with the value obtained from %s", config.AuthCodeURL(""))
		}
	}

	storagetest.TestOpt(t, storagetest.Opts{
		New: func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
			sto, err := newFromConfig(nil, jsonconfig.Obj{
				"bucket": *bucket,
				"auth": map[string]interface{}{
					"client_id":     *clientID,
					"client_secret": *clientSecret,
					"refresh_token": token.RefreshToken,
				},
			})
			if err != nil {
				t.Fatal(err)
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
		},
	})
}
