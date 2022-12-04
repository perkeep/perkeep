/*
Copyright 2016 The Perkeep Authors.

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

package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/test"
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
	w := test.GetWorld(t)
	c := client.NewOrFail(client.OptionServer(w.ServerBaseURL()))
	f := newFakeFile("foo.txt", "bar", time.Date(2011, 1, 28, 2, 3, 4, 0, time.Local))

	testUploadFile(t, c, f, false)
	testUploadFile(t, c, f, true)

	f.modTime.Add(time.Hour)
	testUploadFile(t, c, f, true)

	f.name = "baz.txt"
	testUploadFile(t, c, f, true)
}

// testUploadFile uploads a file and checks if it can be retrieved.
func testUploadFile(t *testing.T, c *client.Client, f *fakeFile, withFileOpts bool) *schema.Blob {
	var (
		ctxbg = context.Background()
		opts  *client.FileUploadOptions
	)
	if withFileOpts {
		opts = &client.FileUploadOptions{FileInfo: f}
	}
	bref, err := c.UploadFile(ctxbg, f.Name(), strings.NewReader(f.content), opts)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.FetchSchemaBlob(ctxbg, bref)
	if err != nil {
		t.Fatal(err)
	}
	if sb.Type() != "file" {
		t.Fatal(`schema blob from UploadFile must have "file" type`)
	}
	return sb
}
