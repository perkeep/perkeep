/*
Copyright 2016 The Camlistore Authors.

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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"camlistore.org/pkg/osutil"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/serverinit"
	"camlistore.org/pkg/types/serverconfig"

	// For registering all the handler constructors needed in newTestServer
	_ "camlistore.org/pkg/blobserver/cond"
	_ "camlistore.org/pkg/blobserver/replica"
	_ "camlistore.org/pkg/importer/allimporters"
	_ "camlistore.org/pkg/search"
	_ "camlistore.org/pkg/server"
)

type fakeFile struct {
	name    string
	size    int64
	modTime time.Time

	content string
}

func newFakeFile(name, content string, modTime time.Time) *fakeFile {
	return &fakeFile{name, int64(len(content)), modTime, content}
}

func (f *fakeFile) Name() string       { return f.name }
func (f *fakeFile) Size() int64        { return f.size }
func (f *fakeFile) ModTime() time.Time { return f.modTime }
func (f *fakeFile) Mode() os.FileMode  { return 0666 }
func (f *fakeFile) IsDir() bool        { return false }
func (f *fakeFile) Sys() interface{}   { return nil }

// TestUploadFile checks if uploading a file with the same content
// but different metadata works, and whether camliType is set to "file".
func TestUploadFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	ts := newTestServer(t)
	defer ts.Close()

	c := New(ts.URL)

	f := newFakeFile("foo.txt", "bar", time.Date(2011, 1, 28, 2, 3, 4, 0, time.Local))

	testUploadFile(t, c, f, false)
	testUploadFile(t, c, f, true)

	f.modTime.Add(time.Hour)

	testUploadFile(t, c, f, true)

	f.name = "baz.txt"

	testUploadFile(t, c, f, true)
}

// testUploadFile uploads a file and checks if it can be retrieved.
func testUploadFile(t *testing.T, c *Client, f *fakeFile, withFileOpts bool) *schema.Blob {
	var opts *FileUploadOptions
	if withFileOpts {
		opts = &FileUploadOptions{FileInfo: f}
	}
	bref, err := c.UploadFile(f.Name(), strings.NewReader(f.content), opts)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.FetchSchemaBlob(bref)
	if err != nil {
		t.Fatal(err)
	}
	if sb.Type() != "file" {
		t.Fatal(`schema blob from UploadFile must have "file" type`)
	}
	return sb
}

// newTestServer creates a new test server with in memory storage for use in upload tests
func newTestServer(t *testing.T) *httptest.Server {
	camroot, err := osutil.GoPackagePath("camlistore.org")
	if err != nil {
		t.Fatalf("failed to find camlistore.org GOPATH root: %v", err)
	}

	conf := serverconfig.Config{
		Listen:             ":3179",
		HTTPS:              false,
		Auth:               "localhost",
		Identity:           "26F5ABDA",
		IdentitySecretRing: filepath.Join(camroot, filepath.FromSlash("pkg/jsonsign/testdata/test-secring.gpg")),
		MemoryStorage:      true,
		MemoryIndex:        true,
	}

	confData, err := json.MarshalIndent(conf, "", "    ")
	if err != nil {
		t.Fatalf("Could not json encode config: %v", err)
	}

	// Setting CAMLI_CONFIG_DIR to avoid triggering failInTests in osutil.CamliConfigDir
	defer os.Setenv("CAMLI_CONFIG_DIR", os.Getenv("CAMLI_CONFIG_DIR")) // restore after test
	os.Setenv("CAMLI_CONFIG_DIR", "whatever")
	lowConf, err := serverinit.Load(confData)
	if err != nil {
		t.Fatal(err)
	}
	// because these two are normally consumed in camlistored.go
	// TODO(mpl): serverinit.Load should consume these 2 as well. Once
	// consumed, we should keep all the answers as private fields, and then we
	// put accessors on serverinit.Config. Maybe we even stop embedding
	// jsonconfig.Obj in serverinit.Config too, so none of those methods are
	// accessible.
	lowConf.OptionalBool("https", true)
	lowConf.OptionalString("listen", "")

	reindex := false
	var context *http.Request // only used by App Engine. See handlerLoader in serverinit.go
	hi := http.NewServeMux()
	address := "http://" + conf.Listen
	_, err = lowConf.InstallHandlers(hi, address, reindex, context)
	if err != nil {
		t.Fatal(err)
	}

	return httptest.NewServer(hi)
}
