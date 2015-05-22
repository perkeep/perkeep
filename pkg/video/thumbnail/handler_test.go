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

package thumbnail

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/test"
)

func TestHandlerWrongRef(t *testing.T) {
	storage := new(test.Fetcher)
	ref := blob.MustParse("sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15")
	wrongRefString := "sha1-e242ed3bffccdf271b7fbaf34ed72d089537b42f"
	ts := httptest.NewServer(createVideothumbnailHandler(ref, storage))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/" + wrongRefString)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("excepted forbidden status when the wrong ref is requested")
	}
}

func TestHandlerRightRef(t *testing.T) {
	b := test.Blob{Contents: "Foo"}
	storage := new(test.Fetcher)
	ref, err := schema.WriteFileFromReader(storage, "", b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(createVideothumbnailHandler(ref, storage))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/" + ref.String())

	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 status: %v", resp)
	}
	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != b.Contents {
		t.Errorf("excepted handler to serve data")
	}
}
