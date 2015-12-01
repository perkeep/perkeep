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

package drive

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"testing"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/constants/google"
	"go4.org/jsonconfig"

	"go4.org/oauthutil"
	"golang.org/x/oauth2"
)

var (
	configFile   = flag.String("config", "", "Path to a configuration JSON file. If given, all other configuration flags are ignored. Use \"camtool googinit --type=drive\" to generate the auth parameters.")
	parentId     = flag.String("parentDir", "", "id of the directory on google drive to use for testing. If empty or \"root\", and --config blank too, testing is skipped.")
	clientID     = flag.String("client_id", "", "OAuth2 client_id for testing")
	clientSecret = flag.String("client_secret", "", "OAuth2 client secret for testing")
	tokenCache   = flag.String("token_cache", ".tokencache", "Token cache file.")
	authCode     = flag.String("auth_code", "", "Use when instructed to do so, when the --token_cache is empty.")
)

type Config struct {
	Auth      AuthConfig `json:"auth"`
	ParentDir string     `json:"parentDir"`
}

type AuthConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
}

func TestStorage(t *testing.T) {
	if (*parentId == "" || *parentId == "root") && *configFile == "" {
		t.Skip("Skipping test, refusing to use goodle drive's root directory. (you need to specify --parentDir or --config).")
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
		*parentId = conf.ParentDir
	}
	if *parentId == "" || *parentId == "root" {
		t.Fatal("ParentDir must be specified, and not \"root\"")
	}
	if *clientID == "" || *clientSecret == "" {
		t.Fatal("--client_id and --client_secret required. Obtain from https://console.developers.google.com/ > Project > APIs & Auth > Credentials. Should be a 'native' or 'Installed application'")
	}
	if *configFile == "" {
		config := &oauth2.Config{
			Scopes:       []string{Scope},
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

	storagetest.TestOpt(t, storagetest.Opts{
		New: func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
			sto, err := newFromConfig(nil, jsonconfig.Obj{
				"parent_id": *parentId,
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
			clearDirectory := func() {
				log.Printf("WARNING: no cleanup in %v directory was done.", *parentId)
			}
			return sto, clearDirectory
		},
		SkipEnum: true,
	})
}
