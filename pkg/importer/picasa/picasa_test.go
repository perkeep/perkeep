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

package picasa

import (
	"net/http"
	"testing"

	"camlistore.org/pkg/httputil"

	"camlistore.org/third_party/github.com/tgulacsi/picago"
)

func TestGetUserId(t *testing.T) {
	userID := "11047045264"
	responder := httputil.FileResponder("testdata/users-me-res.xml")
	cl := &http.Client{
		Transport: httputil.NewFakeTransport(map[string]func() *http.Response{
			"https://picasaweb.google.com/data/feed/api/user/default/contacts?kind=user":        responder,
			"https://picasaweb.google.com/data/feed/api/user/" + userID + "/contacts?kind=user": responder,
		})}
	inf, err := picago.GetUser(cl, "default")
	if err != nil {
		t.Fatal(err)
	}
	want := picago.User{
		ID:        userID,
		URI:       "https://picasaweb.google.com/" + userID,
		Name:      "Tamás Gulácsi",
		Thumbnail: "https://lh4.googleusercontent.com/-qqove344/AAAAAAAAAAI/AAAAAAABcbg/TXl3f2K9dzI/s64-c/11047045264.jpg",
	}
	if inf != want {
		t.Errorf("user info = %+v; want %+v", inf, want)
	}
}

func TestMediaURLsEqual(t *testing.T) {
	if !mediaURLsEqual("https://lh1.googleusercontent.com/foo.jpg", "https://lh100.googleusercontent.com/foo.jpg") {
		t.Fatal("want equal")
	}
	if mediaURLsEqual("https://foo.com/foo.jpg", "https://bar.com/foo.jpg") {
		t.Fatal("want not equal")
	}
}
