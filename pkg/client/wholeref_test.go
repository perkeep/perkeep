/*
Copyright 2026 The Perkeep Authors

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

package client

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
)

func TestWholeRefHashes(t *testing.T) {
	for _, tt := range []struct {
		disco string
		want  []string // hash names
	}{
		{`{"blobRoot": "/bs/"}`, []string{"sha224"}},
		{`{"blobRoot": "/bs/", "hasLegacySHA1Index": true}`, []string{"sha224", "sha1"}},
	} {
		ts := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/javascript")
			w.Write([]byte(tt.disco))
		}))
		c, err := New(OptionServer("http://example.com"), OptionNoExternalConfig(), OptionAuthMode(auth.None{}))
		if err != nil {
			t.Fatal(err)
		}
		c.SetHTTPClient(ts.Client())
		hashes, err := c.WholeRefHashes()
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, newHash := range hashes {
			got = append(got, blob.RefFromHash(newHash()).HashName())
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("with discovery %s, WholeRefHashes = %q; want %q", tt.disco, got, tt.want)
		}
	}
}
