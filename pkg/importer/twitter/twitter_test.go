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

package twitter

import (
	"net/http"
	"path/filepath"
	"testing"

	"camlistore.org/pkg/httputil"
	"camlistore.org/pkg/importer"

	"camlistore.org/third_party/github.com/garyburd/go-oauth/oauth"

	"go4.org/ctxutil"
	"golang.org/x/net/context"
)

func TestGetUserID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.WithValue(context.TODO(), ctxutil.HTTPClient, &http.Client{
		Transport: httputil.NewFakeTransport(map[string]func() *http.Response{
			apiURL + userInfoAPIPath: httputil.FileResponder(filepath.FromSlash("testdata/verify_credentials-res.json")),
		}),
	}))
	defer cancel()
	inf, err := getUserInfo(importer.OAuthContext{ctx, &oauth.Client{}, &oauth.Credentials{}})
	if err != nil {
		t.Fatal(err)
	}
	want := userInfo{
		ID:         "2325935334",
		ScreenName: "lejatorn",
		Name:       "Mathieu Lonjaret",
	}
	if inf != want {
		t.Errorf("user info = %+v; want %+v", inf, want)
	}
}
