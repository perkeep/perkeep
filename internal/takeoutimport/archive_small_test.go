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
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"perkeep.org/internal/takeout"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
)

// countingServer is a Server that counts the calls of its PutFile and
// FetchSchemaBlob.
type countingServer struct {
	Server
	putFiles, fetches atomic.Int64
}

func (s *countingServer) PutFile(ctx context.Context, name string, r io.Reader) (file, whole blob.Ref, err error) {
	s.putFiles.Add(1)
	return s.Server.PutFile(ctx, name, r)
}

func (s *countingServer) FetchSchemaBlob(ctx context.Context, br blob.Ref) (*schema.Blob, error) {
	s.fetches.Add(1)
	return s.Server.FetchSchemaBlob(ctx, br)
}

// TestTakeoutArchiveSmallFiles checks that archiving an export's small
// files, which the server doesn't have, puts each in the archive's batch
// as a blob, rather than uploading it as a file and then asking the
// server for the file's parts.
func TestTakeoutArchiveSmallFiles(t *testing.T) {
	st := newTestImporter(t)
	ctx := context.Background()
	dir := t.TempDir()
	files := map[string]string{
		"Takeout/Mail/All mail.mbox":  "From x",
		"Takeout/Chrome/History.json": "{}",
		"Takeout/Chrome/Big.bin":      strings.Repeat("big", smallContentMax/3+1),
		"Takeout/empty.txt":           "",
	}
	writeTakeoutZip(t, dir, "takeout-20240101T000000Z-001.zip", files)
	exps, err := takeout.LocalExports(dir)
	if err != nil {
		t.Fatal(err)
	}
	exp := exps[0]
	if err := exp.LoadTOCs(ctx, 2); err != nil {
		t.Fatal(err)
	}
	cs := &countingServer{Server: st.Server}
	st.Server = cs
	root, err := st.Archive(ctx, exp)
	if err != nil {
		t.Fatal(err)
	}
	// Only the big file is uploaded as a file, whose parts are fetched.
	if n := cs.putFiles.Load(); n != 1 {
		t.Errorf("PutFile called %d times; want 1, for the big file", n)
	}
	if n := cs.fetches.Load(); n != 1 {
		t.Errorf("FetchSchemaBlob called %d times; want 1, for the big file's parts", n)
	}

	st.Server = cs.Server
	got, _ := readArchiveTree(t, st, root)
	for p, data := range files {
		if g := got["takeout-20240101T000000Z/takeout-20240101T000000Z-001.zip/"+p]; g != data {
			t.Errorf("archived %s has %d bytes; want %d", p, len(g), len(data))
		}
	}
}
