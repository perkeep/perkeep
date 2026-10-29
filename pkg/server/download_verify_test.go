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

package server

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/search"
	"perkeep.org/pkg/test"
)

// countingFetcher counts the fetches of one blob.
type countingFetcher struct {
	*test.Fetcher
	ref blob.Ref
	n   atomic.Int64
}

func (f *countingFetcher) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	if br == f.ref {
		f.n.Add(1)
	}
	return f.Fetcher.Fetch(ctx, br)
}

// TestDownloadVerifyContents checks that verifying a file's contents
// trusts the index, without reading them, when all of the file's chunks
// are stored.
func TestDownloadVerifyContents(t *testing.T) {
	idx := index.NewMemoryIndex()
	idx.Logger = test.NewLogger(t, "")
	id := indextest.NewIndexDeps(idx)
	id.Fataler = t
	defer id.Index.Close()
	contents := strings.Repeat("contents ", 1000)
	file, whole := id.UploadFile("a.txt", contents, time.Unix(1700000000, 0))
	h := search.NewHandler(id.Index, index.NewOwner(indextest.KeyID, id.SignerBlobRef))
	f := &countingFetcher{Fetcher: id.BlobSource, ref: whole}
	dh := &DownloadHandler{Fetcher: f, Search: h}
	verify := func(ref blob.Ref) string {
		t.Helper()
		rec := httptest.NewRecorder()
		dh.ServeFile(rec, httptest.NewRequest("HEAD", "/download/"+file.String()+"/a.txt?verifycontents="+ref.String(), nil), file)
		return rec.Header().Get("X-Camli-Contents")
	}

	if got := verify(whole); got != whole.String() {
		t.Errorf("verifying the contents: X-Camli-Contents = %q; want %v", got, whole)
	}
	if n := f.n.Load(); n != 0 {
		t.Errorf("verifying the contents read them %d times; want 0", n)
	}
	if got := verify(blob.RefFromString("other")); got != "" {
		t.Errorf("verifying other contents: X-Camli-Contents = %q; want none", got)
	}

	// Without its contents, the file can't be verified, whatever the
	// index says.
	if err := id.BlobSource.RemoveBlobs(context.Background(), []blob.Ref{whole}); err != nil {
		t.Fatal(err)
	}
	if got := verify(whole); got != "" {
		t.Errorf("verifying missing contents: X-Camli-Contents = %q; want none", got)
	}
}
