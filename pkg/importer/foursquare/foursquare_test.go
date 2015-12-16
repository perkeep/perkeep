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

package foursquare

import (
	"net/http"
	"testing"

	"camlistore.org/pkg/httputil"

	"go4.org/ctxutil"
	"golang.org/x/net/context"
)

func TestGetUserId(t *testing.T) {
	im := &imp{}
	ctx, cancel := context.WithCancel(context.WithValue(context.TODO(), ctxutil.HTTPClient, &http.Client{
		Transport: httputil.NewFakeTransport(map[string]func() *http.Response{
			"https://api.foursquare.com/v2/users/self?oauth_token=footoken&v=20140225": httputil.FileResponder("testdata/users-me-res.json"),
		}),
	}))
	defer cancel()
	inf, err := im.getUserInfo(ctx, "footoken")
	if err != nil {
		t.Fatal(err)
	}
	want := user{
		Id:        "13674",
		FirstName: "Brad",
		LastName:  "Fitzpatrick",
	}
	if inf != want {
		t.Errorf("user info = %+v; want %+v", inf, want)
	}
}
