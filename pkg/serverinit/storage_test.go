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
	"os"
	"path/filepath"
	"testing"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/serverinit"
	"perkeep.org/pkg/test"
	"perkeep.org/pkg/types/serverconfig"
)

// TestStorageHandlers checks that a config's storage, a blobpacked
// replica, makes a storage that works, with its packed blobs' index in its
// own dir, as blobPath's is.
func TestStorageHandlers(t *testing.T) {
	srcRoot, err := osutil.PkSourceRoot()
	if err != nil {
		t.Fatalf("source root folder not found: %v", err)
	}
	blobs, replica := t.TempDir(), t.TempDir()
	// Storages' dirs must exist already, as on a disk that's mounted.
	if err := os.Mkdir(filepath.Join(replica, "packed"), 0o700); err != nil {
		t.Fatal(err)
	}
	conf := serverinit.DefaultBaseConfig
	conf.Auth = "userpass:owner:pw"
	conf.Identity = "26F5ABDA"
	conf.IdentitySecretRing = filepath.Join(srcRoot, filepath.FromSlash("pkg/jsonsign/testdata/test-secring.gpg"))
	conf.BlobPath = blobs
	conf.PackRelated = true
	conf.SQLite = filepath.Join(blobs, "index.sqlite")
	conf.Storage = map[string]*serverconfig.Storage{
		"nas": {Type: "blobpacked", Local: &serverconfig.LocalStorage{Dir: replica}},
	}
	conf.ReplicateTo = []string{"nas"}
	confData, err := json.Marshal(conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAMLI_CONFIG_DIR", "whatever")
	lowConf, err := serverinit.Load(confData)
	if err != nil {
		t.Fatal(err)
	}
	// Only the replica's storage, so that everything it opens is closed
	// before its dirs are removed, which Windows requires.
	sto, shutdown, err := lowConf.OpenStorage("/sto-nas/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := shutdown.Close(); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()

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
	// The replica's layout is blobPath's, with its packed blobs' index there.
	if _, err := os.Stat(filepath.Join(replica, "packed", "packindex.sqlite")); err != nil {
		t.Errorf("replica's packed blob index: %v", err)
	}
}
