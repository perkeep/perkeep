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

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/internal/osutil"
	search "perkeep.org/internal/searchtypes"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/serverinit"
	"perkeep.org/pkg/types/serverconfig"

	_ "perkeep.org/pkg/blobserver/cond"
	_ "perkeep.org/pkg/blobserver/replica"
	_ "perkeep.org/pkg/importer"
	_ "perkeep.org/pkg/search"
	_ "perkeep.org/pkg/server"
)

// newTestClient returns a client of a server in memory, with an index,
// and a signer as its identity.
func newTestClient(t *testing.T) (*client.Client, *schema.Signer) {
	t.Helper()
	srcRoot, err := osutil.PkSourceRoot()
	if err != nil {
		t.Fatal(err)
	}
	secring := filepath.Join(srcRoot, filepath.FromSlash("pkg/jsonsign/testdata/test-secring.gpg"))
	confData, err := json.Marshal(serverconfig.Config{
		Listen:             ":3179",
		Auth:               "userpass:foo:bar",
		Identity:           "26F5ABDA",
		IdentitySecretRing: secring,
		MemoryStorage:      true,
		MemoryIndex:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAMLI_CONFIG_DIR", t.TempDir())
	lowConf, err := serverinit.Load(confData)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	shutdown, err := lowConf.InstallHandlers(mux, "http://example.com")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shutdown.Close() })
	ts := httptest.NewTestServer(t, mux)
	c, err := client.New(client.OptionServer("http://example.com"), client.OptionNoExternalConfig(),
		client.OptionAuthMode(auth.NewBasicAuth("foo", "bar")))
	if err != nil {
		t.Fatal(err)
	}
	c.SetHTTPClient(ts.Client())

	ent, err := jsonsign.EntityFromSecring("26F5ABDA", secring)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := jsonsign.ArmoredPublicKey(ent)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := schema.NewSigner(blob.RefFromString(pub), strings.NewReader(pub), ent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upload(context.Background(), client.NewUploadHandleFromString(pub)); err != nil {
		t.Fatal(err)
	}
	return c, signer
}

// permanodeAttrs returns the attributes of permanode pn.
func permanodeAttrs(t *testing.T, c *client.Client, pn blob.Ref) url.Values {
	t.Helper()
	res, err := c.Describe(context.Background(), &search.DescribeRequest{BlobRef: pn})
	if err != nil {
		t.Fatal(err)
	}
	db := res.Meta.Get(pn)
	if db == nil || db.Permanode == nil {
		t.Fatalf("permanode %v not described", pn)
	}
	return db.Permanode.Attr
}
