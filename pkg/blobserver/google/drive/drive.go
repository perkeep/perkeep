/*
Copyright 2013 Google Inc.

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
Package drive registers the "googledrive" blobserver storage
type, storing blobs in a Google Drive folder.

Example low-level config:

    "/storage-googledrive/": {
      "handler": "storage-googledrive",
      "handlerArgs": map[string]interface{}{
        "parent_id": parentId,
        "auth": map[string]interface{}{
          "client_id":     clientId,
          "client_secret": clientSecret,
          "refresh_token": refreshToken,
        },
      },
    },
*/
package drive

import (
	"net/http"
	"time"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/google/drive/service"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/third_party/code.google.com/p/goauth2/oauth"
)

const (
	GoogleOAuth2AuthURL  = "https://accounts.google.com/o/oauth2/auth"
	GoogleOAuth2TokenURL = "https://accounts.google.com/o/oauth2/token"
)

type driveStorage struct {
	*blobserver.SimpleBlobHubPartitionMap
	service *service.DriveService
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	auth := config.RequiredObject("auth")
	oauthConf := &oauth.Config{
		ClientId:     auth.RequiredString("client_id"),
		ClientSecret: auth.RequiredString("client_secret"),
		AuthURL:      GoogleOAuth2AuthURL,
		TokenURL:     GoogleOAuth2TokenURL,
	}

	// force refreshes the access token on start, make sure
	// refresh request in parallel are being started
	transport := &oauth.Transport{
		Token: &oauth.Token{
			AccessToken:  "",
			RefreshToken: auth.RequiredString("refresh_token"),
			Expiry:       time.Now(),
		},
		Config:    oauthConf,
		Transport: http.DefaultTransport,
	}

	service, err := service.New(transport, config.RequiredString("parent_id"))
	sto := &driveStorage{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
		service:                   service,
	}
	return sto, err
}

func init() {
	blobserver.RegisterStorageConstructor("googledrive", blobserver.StorageConstructor(newFromConfig))
}
