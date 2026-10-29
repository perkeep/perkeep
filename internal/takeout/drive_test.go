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

package takeout

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// fakeDrive serves the parts of the Drive API that Drive uses, for the
// zip files in a folder named Takeout.
type fakeDrive struct {
	zips   map[string][]byte
	ranges atomic.Int64 // number of ranged reads
	limit  atomic.Int64 // how many more requests to reject for a rate limit
}

func (fd *fakeDrive) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if fd.limit.Add(-1) >= 0 {
		// As Drive rejects requests beyond a rate limit.
		http.Error(w, `{"error": {"errors": [{"reason": "rateLimitExceeded"}], "status": "PERMISSION_DENIED"}}`, http.StatusForbidden)
		return
	}
	switch {
	case r.URL.Path == "/drive/v3/files":
		q := r.FormValue("q")
		var files []driveFile
		switch {
		case strings.Contains(q, "name = 'Takeout'"):
			files = []driveFile{{ID: "takeout-folder", Name: "Takeout"}}
		case strings.HasPrefix(q, "'takeout-folder' in parents"):
			for name, data := range fd.zips {
				files = append(files, driveFile{ID: "id-" + name, Name: name, Size: int64(len(data)), HeadRevisionID: "rev-" + name})
			}
			slices.SortFunc(files, func(a, b driveFile) int { return strings.Compare(a.Name, b.Name) })
		}
		// One file per page, to test paging.
		page := map[string]any{}
		start := 0
		fmt.Sscan(r.FormValue("pageToken"), &start)
		if start < len(files) {
			page["files"] = []map[string]string{{
				"id":             files[start].ID,
				"name":           files[start].Name,
				"size":           fmt.Sprint(files[start].Size),
				"headRevisionId": files[start].HeadRevisionID,
			}}
			if start+1 < len(files) {
				page["nextPageToken"] = fmt.Sprint(start + 1)
			}
		}
		json.NewEncoder(w).Encode(page)
	case strings.HasPrefix(r.URL.Path, "/drive/v3/files/id-"):
		name, rev, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/drive/v3/files/id-"), "/revisions/")
		data, found := fd.zips[name]
		if !ok || !found || rev != "rev-"+name || r.FormValue("alt") != "media" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Range") != "" {
			fd.ranges.Add(1)
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	default:
		http.NotFound(w, r)
	}
}

func TestDriveExports(t *testing.T) {
	fd := &fakeDrive{zips: testZips(t)}
	ts := httptest.NewTestServer(t, fd)
	cacheDir := t.TempDir()
	d := &Drive{Client: ts.Client(), CacheDir: cacheDir, apiBase: ts.URL}
	exps, err := d.Exports(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	checkExport(t, exps)
	if fd.ranges.Load() == 0 {
		t.Fatal("no ranged reads")
	}

	// With the tables of contents cached, loading them again reads
	// nothing, even by the folder's URL.
	fd.ranges.Store(0)
	d = &Drive{Client: ts.Client(), CacheDir: cacheDir, apiBase: ts.URL}
	exps, err = d.Exports(context.Background(), "https://drive.google.com/drive/folders/takeout-folder")
	if err != nil {
		t.Fatal(err)
	}
	if len(exps) != 1 {
		t.Fatalf("got %d exports by folder URL; want 1", len(exps))
	}
	if err := exps[0].LoadTOCs(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	if n := fd.ranges.Load(); n != 0 {
		t.Errorf("loading cached tables of contents made %d ranged reads; want 0", n)
	}
}

// TestDriveRateLimit checks that requests that Drive rejects for a rate
// limit are retried, after waiting.
func TestDriveRateLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fd := &fakeDrive{zips: testZips(t)}
		fd.limit.Store(3)
		ts := httptest.NewTestServer(t, fd)
		d := &Drive{Client: ts.Client(), apiBase: ts.URL}
		start := time.Now()
		exps, err := d.Exports(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(start); waited < 7*time.Second {
			t.Errorf("waited %v for 3 rejections; want at least 1s+2s+4s", waited)
		}
		checkExport(t, exps)
	})
}
