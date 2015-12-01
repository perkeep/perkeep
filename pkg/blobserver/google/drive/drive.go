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
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/google/drive/service"
	"camlistore.org/pkg/constants/google"
	"go4.org/jsonconfig"

	"go4.org/oauthutil"
	"golang.org/x/oauth2"
)

// Scope is the OAuth2 scope used for Google Drive.
const Scope = "https://www.googleapis.com/auth/drive"

type driveStorage struct {
	service *service.DriveService
}

func newFromConfig(_ blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	auth := config.RequiredObject("auth")
	oAuthClient := oauth2.NewClient(oauth2.NoContext, oauthutil.NewRefreshTokenSource(&oauth2.Config{
		Scopes:       []string{Scope},
		Endpoint:     google.Endpoint,
		ClientID:     auth.RequiredString("client_id"),
		ClientSecret: auth.RequiredString("client_secret"),
		RedirectURL:  oauthutil.TitleBarRedirectURL,
	}, auth.RequiredString("refresh_token")))
	parent := config.RequiredString("parent_id")
	if err := config.Validate(); err != nil {
		return nil, err
	}
	service, err := service.New(oAuthClient, parent)
	sto := &driveStorage{
		service: service,
	}
	return sto, err
}

func init() {
	blobserver.RegisterStorageConstructor("googledrive", blobserver.StorageConstructor(newFromConfig))
}
