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

package takeoutimport

import (
	"archive/zip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"perkeep.org/internal/osutil"
	search "perkeep.org/internal/searchtypes"
	"perkeep.org/internal/takeout"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/jsonsign"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/serverinit"
	"perkeep.org/pkg/types/serverconfig"
	"perkeep.org/pkg/upload"

	_ "perkeep.org/pkg/blobserver/cond"
	_ "perkeep.org/pkg/blobserver/replica"
	_ "perkeep.org/pkg/importer"
	_ "perkeep.org/pkg/search"
	_ "perkeep.org/pkg/server"
)

// newTestImporter returns an Importer of a server in memory, with an
// index, signing as its identity.
func newTestImporter(t *testing.T) *Importer {
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
	return &Importer{Server: &Client{Client: c}, Signer: signer, Workers: 2}
}

// client returns the client of im's server, a Client.
func (im *Importer) client() *client.Client { return im.Server.(*Client).Client }

func TestStore(t *testing.T) {
	ctx := context.Background()
	st := newTestImporter(t)
	c := st.client()
	if err := st.CheckServer(ctx); err != nil {
		t.Fatal(err)
	}

	photo := strings.Repeat("photo ", 1000)
	video := strings.Repeat("video ", 100000) // more than one chunk
	dir := t.TempDir()
	zf, err := os.Create(filepath.Join(dir, "takeout-20240101T120000Z-001.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	for _, f := range []struct{ name, data string }{
		{"Takeout/Google Photos/Photos from 2019/IMG_0001.JPG", photo},
		{"Takeout/Google Photos/Photos from 2019/IMG_0001.JPG.supplemental-metadata.json", "{}"},
		{"Takeout/Google Photos/Photos from 2019/VID_0002.MP4", video},
	} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: time.Unix(1700000000, 0)})
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(f.data))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zf.Close()

	exps, err := takeout.LocalExports(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := exps[0].LoadTOCs(ctx, 2); err != nil {
		t.Fatal(err)
	}
	var ents []takeout.Entry
	for _, e := range exps[0].Entries() {
		if takeout.IsPhotosMedia(e.File.Name) {
			ents = append(ents, e)
		}
	}
	cs := takeout.Contents(ents)
	if len(cs) != 2 {
		t.Fatalf("got %d contents; want 2", len(cs))
	}

	// The server already has the photo, by another name.
	if _, err := upload.NewFileFromReader(c, "photo.jpg", strings.NewReader(photo), nil).Run(ctx); err != nil {
		t.Fatal(err)
	}
	have, missing, err := takeout.Check(ctx, st.store(false), cs, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(have) != 1 || have[0].Size != int64(len(photo)) || len(missing) != 1 || missing[0].Size != int64(len(video)) {
		t.Fatalf("Check: have %d, missing %d; want the photo had and the video missing", len(have), len(missing))
	}

	failed, err := takeout.Upload(ctx, st.store(true), missing, 2, nil)
	if err != nil || failed != 0 {
		t.Fatalf("Upload: %d failed, error %v", failed, err)
	}
	if _, missing, err := takeout.Check(ctx, st.store(false), cs, 2, nil); err != nil || len(missing) != 0 {
		t.Fatalf("after Upload, %d missing, error %v; want none", len(missing), err)
	}

	// The video has a permanode, with its name.
	size := int64(len(video))
	res, err := c.Query(ctx, &search.SearchQuery{
		Constraint: &search.Constraint{Permanode: &search.PermanodeConstraint{
			Attr:       "camliContent",
			ValueInSet: &search.Constraint{File: &search.FileConstraint{FileSize: &search.IntConstraint{Equals: &size}}},
		}},
		Describe: &search.DescribeRequest{Rules: []*search.DescribeRule{{Attrs: []string{"camliContent"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blobs) != 1 {
		t.Fatalf("got %d permanodes of the video; want 1", len(res.Blobs))
	}
	pn := res.Describe.Meta.Get(res.Blobs[0].Blob)
	if pn == nil || pn.Permanode == nil {
		t.Fatalf("permanode not described")
	}
	file := res.Describe.Meta[pn.Permanode.Attr.Get("camliContent")]
	if file == nil || file.File == nil || file.File.FileName != "VID_0002.MP4" {
		t.Fatalf("permanode's camliContent = %+v; want the file VID_0002.MP4", file)
	}
	// The zip entry's time is when the export was made, not the video's.
	if file.File.Time != nil || file.File.ModTime != nil {
		t.Errorf("file has time %v, modTime %v, from its zip entry; want none", file.File.Time, file.File.ModTime)
	}
}
