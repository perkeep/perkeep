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

package dropbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/filesource"
)

// fakeFile is a file or folder of a fakeDropbox.
type fakeFile struct {
	id, name, parent string // parent is "" for the root
	folder, deleted  bool
	revs             map[string][]byte // by revision
	rev              string            // the latest
}

// fakeDropbox serves the parts of the Dropbox API that Source uses.
type fakeDropbox struct {
	mu        sync.Mutex
	files     []*fakeFile
	limit     int  // how many more requests to reject for a rate limit
	fullFiles bool // whether downloads ignore Range headers
}

func (fd *fakeDropbox) meta(f *fakeFile) map[string]any {
	m := map[string]any{".tag": "file", "name": f.name, "id": f.id}
	switch {
	case f.deleted:
		m[".tag"] = "deleted"
	case f.folder:
		m[".tag"] = "folder"
	default:
		m["rev"] = f.rev
		m["size"] = len(f.revs[f.rev])
		m["server_modified"] = "2026-11-01T00:11:02Z"
	}
	return m
}

// path returns the path of f, such as "/Apps/x". fd.mu must be held.
func (fd *fakeDropbox) path(f *fakeFile) string {
	p := "/" + f.name
	for f.parent != "" {
		for _, pf := range fd.files {
			if pf.id == f.parent {
				f = pf
				break
			}
		}
		p = "/" + f.name + p
	}
	return p
}

func conflict(w http.ResponseWriter, summary string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	fmt.Fprintf(w, `{"error_summary": %q, "error": {}}`, summary)
}

func (fd *fakeDropbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	if fd.limit > 0 {
		fd.limit--
		w.Header().Set("Retry-After", "3")
		http.Error(w, `{"error_summary": "too_many_requests/"}`, http.StatusTooManyRequests)
		return
	}
	var arg map[string]any
	if r.URL.Path == "/2/files/download" {
		json.Unmarshal([]byte(r.Header.Get("Dropbox-API-Arg")), &arg)
	} else if err := json.NewDecoder(r.Body).Decode(&arg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p, _ := arg["path"].(string)
	switch r.URL.Path {
	case "/2/files/list_folder", "/2/files/list_folder/continue":
		start := 0
		if c, ok := arg["cursor"].(string); ok {
			fmt.Sscanf(c, "%s %d", &p, &start)
		}
		var entries []map[string]any
		for _, f := range fd.files {
			if f.parent == p {
				entries = append(entries, fd.meta(f))
			}
		}
		// Two entries a page, to test paging.
		end := min(start+2, len(entries))
		json.NewEncoder(w).Encode(map[string]any{
			"entries":  entries[start:end],
			"cursor":   fmt.Sprintf("%s %d", p, end),
			"has_more": end < len(entries),
		})
	case "/2/files/get_metadata":
		for _, f := range fd.files {
			if (f.id == p || strings.EqualFold(fd.path(f), p)) && !f.deleted {
				json.NewEncoder(w).Encode(fd.meta(f))
				return
			}
		}
		conflict(w, "path/not_found/")
	case "/2/files/download":
		rev, _ := strings.CutPrefix(p, "rev:")
		for _, f := range fd.files {
			if data, ok := f.revs[rev]; ok {
				if fd.fullFiles {
					r.Header.Del("Range")
				}
				http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
				return
			}
		}
		conflict(w, "path/not_found/")
	default:
		http.NotFound(w, r)
	}
}

func newFakeDropbox() *fakeDropbox {
	return &fakeDropbox{files: []*fakeFile{
		{id: "id:apps", name: "Apps", folder: true},
		{id: "id:gdyd", name: "Google Download Your Data", parent: "id:apps", folder: true},
		{id: "id:z1", name: "takeout-20261101T001102Z-001.zip", parent: "id:gdyd", rev: "0123456789a", revs: map[string][]byte{"0123456789a": []byte("zip one")}},
		{id: "id:z2", name: "takeout-20261101T001102Z-002.zip", parent: "id:gdyd", rev: "0123456789b", revs: map[string][]byte{"0123456789b": []byte("zip two")}},
		{id: "id:old", name: "deleted.zip", parent: "id:gdyd", deleted: true},
		{id: "id:notes", name: "Notes", folder: true},
	}}
}

func TestSource(t *testing.T) {
	ctx := context.Background()
	fd := newFakeDropbox()
	ts := httptest.NewTestServer(t, fd)
	c := ts.Client()
	s := &Source{Client: c, APIBase: ts.URL, ContentBase: ts.URL}

	root, err := s.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	top, err := s.List(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top[0].Name != "Apps" || !top[0].Dir || top[0].ID != "id:apps" {
		t.Fatalf("List(root) = %+v", top)
	}
	if st, err := s.Stat(ctx, "/"); err != nil || st != root {
		t.Errorf("Stat(/) = %+v, %v; want the root", st, err)
	}
	apps, err := s.List(ctx, top[0])
	if err != nil || len(apps) != 1 {
		t.Fatalf("List(Apps) = %+v, %v", apps, err)
	}
	// Two pages, and not the deleted file.
	zips, err := s.List(ctx, apps[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(zips) != 2 {
		t.Fatalf("List(Google Download Your Data) = %+v; want 2 zips", zips)
	}
	z := zips[1]
	if z.Dir || z.Size != 7 || z.Version != "0123456789b" || z.ModTime.IsZero() {
		t.Errorf("zip = %+v", z)
	}
	if st, err := s.Stat(ctx, "id:z2"); err != nil || st != z {
		t.Errorf("Stat = %+v, %v; want %+v", st, err, z)
	}
	if _, err := s.Stat(ctx, "id:old"); err == nil {
		t.Error("Stat of a deleted file succeeded")
	}
	// Folders can be found by the paths of their URLs, too.
	p, err := FolderPath("https://www.dropbox.com/home/Apps/Google%20Download%20Your%20Data")
	if err != nil || p != "/Apps/Google Download Your Data" {
		t.Fatalf("FolderPath = %q, %v", p, err)
	}
	if st, err := s.Stat(ctx, p); err != nil || st != apps[0] {
		t.Errorf("Stat(%q) = %+v, %v; want %+v", p, st, err, apps[0])
	}
	if got, err := filesource.ReadRange(ctx, s, z, 4, 3); err != nil || string(got) != "two" {
		t.Errorf("ReadRange = %q, %v", got, err)
	}
	fd.mu.Lock()
	fd.fullFiles = true
	fd.mu.Unlock()
	if got, err := filesource.ReadRange(ctx, s, z, 4, 3); err != nil || string(got) != "two" {
		t.Errorf("ReadRange, without ranged downloads = %q, %v", got, err)
	}

	// A revision that's gone is changed.
	fd.mu.Lock()
	fd.files[3].revs = map[string][]byte{"0123456789c": []byte("zip 2.0")}
	fd.files[3].rev = "0123456789c"
	fd.mu.Unlock()
	if _, err := s.OpenRange(ctx, z, 0, 3); !errors.Is(err, filesource.ErrChanged) {
		t.Errorf("OpenRange of a gone revision: %v; want ErrChanged", err)
	}
}

// TestRateLimit checks that requests that Dropbox rejects for a rate
// limit are retried, after the time it says to wait.
func TestRateLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fd := newFakeDropbox()
		fd.limit = 2
		ts := httptest.NewTestServer(t, fd)
		s := &Source{Client: ts.Client(), APIBase: ts.URL, ContentBase: ts.URL}
		start := time.Now()
		f, err := s.Stat(context.Background(), "id:z1")
		if err != nil || f.Version != "0123456789a" {
			t.Fatalf("Stat = %+v, %v", f, err)
		}
		if waited := time.Since(start); waited < 6*time.Second {
			t.Errorf("waited %v for 2 rejections; want 2 Retry-Afters of 3s", waited)
		}
	})
}

func TestFolderPath(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.dropbox.com/home":                        "/",
		"https://www.dropbox.com/home/Apps/Google%20Download": "/Apps/Google Download",
		"/Apps/x/":     "/Apps/x",
		"Apps":         "/Apps",
		" /Apps/../x ": "/x",
	} {
		if got, err := FolderPath(in); err != nil || got != want {
			t.Errorf("FolderPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"https://evil.example/home/x", "https://www.dropbox.com/scl/fo/abc"} {
		if got, err := FolderPath(in); err == nil {
			t.Errorf("FolderPath(%q) = %q; want an error", in, got)
		}
	}
}
