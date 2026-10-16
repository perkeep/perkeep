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

package serverinit_test

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	_ "perkeep.org/pkg/blobserver/blobpacked"
	_ "perkeep.org/pkg/blobserver/localdisk"
	"perkeep.org/pkg/serverinit"
	"perkeep.org/pkg/test"
)

// TestOpenStorage checks that OpenStorage sets up the storage at a prefix
// and the storage it uses, and nothing else.
func TestOpenStorage(t *testing.T) {
	loose, packed := t.TempDir(), t.TempDir()
	confData, err := json.Marshal(map[string]any{
		"handlerConfig": true,
		"prefixes": map[string]any{
			"/bs/": map[string]any{
				"handler": "storage-blobpacked",
				"handlerArgs": map[string]any{
					"smallBlobs": "/bs-loose/",
					"largeBlobs": "/bs-packed/",
					"metaIndex":  map[string]any{"type": "memory"},
				},
			},
			"/bs-loose/": map[string]any{
				"handler":     "storage-filesystem",
				"handlerArgs": map[string]any{"path": loose},
			},
			"/bs-packed/": map[string]any{
				"handler":     "storage-filesystem",
				"handlerArgs": map[string]any{"path": packed},
			},
			// Setting this up would fail.
			"/broken/": map[string]any{
				"handler": "storage-doesnotexist",
			},
			"/ui/": map[string]any{
				"handler": "ui",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAMLI_CONFIG_DIR", t.TempDir())
	conf, err := serverinit.Load(confData)
	if err != nil {
		t.Fatal(err)
	}

	sto, shutdown, err := conf.OpenStorage("/bs/")
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	b := &test.Blob{Contents: "foo"}
	b.MustUpload(t, sto)
	rc, _, err := sto.Fetch(t.Context(), b.BlobRef())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(got) != "foo" {
		t.Errorf("fetched %q, %v; want %q", got, err, "foo")
	}
	if err := shutdown.Close(); err != nil {
		t.Errorf("shutdown: %v", err)
	}

	for _, pfx := range []string{"/ui/", "/nope/"} {
		if _, _, err := conf.OpenStorage(pfx); err == nil || !strings.Contains(err.Error(), "not a storage handler") {
			t.Errorf("OpenStorage(%q) error = %v; want not a storage handler", pfx, err)
		}
	}
	if _, _, err := conf.OpenStorage("/broken/"); err == nil {
		t.Errorf("OpenStorage(/broken/) succeeded; want an error")
	}
}
