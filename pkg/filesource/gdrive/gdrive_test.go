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

package gdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/filesource"
)

// fakeFile is a file or folder of a fakeDrive.
type fakeFile struct {
	id, name, parent, mimeType, rev string
	data                            []byte
}

// fakeDrive serves the parts of the Drive API that Source uses.
type fakeDrive struct {
	files []fakeFile
	limit atomic.Int64 // how many more requests to reject for a rate limit
}

var (
	parentRx = regexp.MustCompile(`^'((?:[^'\\]|\\.)*)' in parents and trashed = false$`)
	nameRx   = regexp.MustCompile(`^name = '((?:[^'\\]|\\.)*)' and (.*)$`)
)

func unquote(s string) string { return strings.NewReplacer(`\'`, `'`, `\\`, `\`).Replace(s) }

func (fd *fakeDrive) json(f fakeFile) map[string]string {
	m := map[string]string{"id": f.id, "name": f.name, "mimeType": f.mimeType, "modifiedTime": "2026-11-01T00:11:02.000Z"}
	if f.rev != "" {
		m["headRevisionId"] = f.rev
		m["size"] = fmt.Sprint(len(f.data))
	}
	return m
}

func (fd *fakeDrive) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if fd.limit.Add(-1) >= 0 {
		// As Drive rejects requests beyond a rate limit.
		http.Error(w, `{"error": {"errors": [{"reason": "rateLimitExceeded"}]}}`, http.StatusForbidden)
		return
	}
	switch rest, _ := strings.CutPrefix(r.URL.Path, "/drive/v3/files"); {
	case rest == "":
		q, name := r.FormValue("q"), ""
		if m := nameRx.FindStringSubmatch(q); m != nil {
			name, q = unquote(m[1]), m[2]
		}
		m := parentRx.FindStringSubmatch(q)
		if m == nil {
			http.Error(w, "bad query "+r.FormValue("q"), http.StatusBadRequest)
			return
		}
		var matches []fakeFile
		for _, f := range fd.files {
			if f.parent == unquote(m[1]) && (name == "" || f.name == name) {
				matches = append(matches, f)
			}
		}
		// One file per page, to test paging.
		start := 0
		fmt.Sscan(r.FormValue("pageToken"), &start)
		page := map[string]any{"files": []map[string]string{}}
		if start < len(matches) {
			page["files"] = []map[string]string{fd.json(matches[start])}
			if start+1 < len(matches) {
				page["nextPageToken"] = fmt.Sprint(start + 1)
			}
		}
		json.NewEncoder(w).Encode(page)
	default:
		id, rev, isRev := strings.Cut(strings.TrimPrefix(rest, "/"), "/revisions/")
		for _, f := range fd.files {
			if f.id != id {
				continue
			}
			switch {
			case !isRev:
				json.NewEncoder(w).Encode(fd.json(f))
			case rev == f.rev && r.FormValue("alt") == "media":
				http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.data))
			default:
				http.NotFound(w, r)
			}
			return
		}
		http.NotFound(w, r)
	}
}

func newFakeDrive() *fakeDrive {
	return &fakeDrive{files: []fakeFile{
		{id: "takeout", name: "Takeout", parent: "root", mimeType: folderType},
		{id: "other", name: "Brad's stuff", parent: "root", mimeType: folderType},
		{id: "doc", name: "Notes", parent: "root", mimeType: "application/vnd.google-apps.document"},
		{id: "z1", name: "takeout-20261101T001102Z-001.zip", parent: "takeout", mimeType: "application/zip", rev: "r1", data: []byte("zip one")},
		{id: "z2", name: "takeout-20261101T001102Z-002.zip", parent: "takeout", mimeType: "application/zip", rev: "r2", data: []byte("zip two")},
	}}
}

func TestSource(t *testing.T) {
	ctx := context.Background()
	fd := newFakeDrive()
	ts := httptest.NewTestServer(t, fd)
	s := &Source{Client: ts.Client(), APIBase: ts.URL}

	root, err := s.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	top, err := s.List(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	// Not the Google Doc.
	if len(top) != 2 || top[0].Name != "Takeout" || !top[0].Dir || top[1].Name != "Brad's stuff" {
		t.Fatalf("List(root) = %+v; want the two folders", top)
	}
	for _, name := range []string{"Takeout", "Brad's stuff"} {
		found, err := filesource.Find(ctx, s, root, name)
		if err != nil || len(found) != 1 || found[0].Name != name {
			t.Errorf("Find(%q) = %+v, %v", name, found, err)
		}
	}
	zips, err := s.List(ctx, top[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(zips) != 2 {
		t.Fatalf("List(Takeout) = %+v; want 2 zips", zips)
	}
	z := zips[1]
	if z.Dir || z.Size != 7 || z.Version != "r2" || z.ModTime.IsZero() {
		t.Errorf("zip = %+v", z)
	}
	if st, err := s.Stat(ctx, "z2"); err != nil || st != z {
		t.Errorf("Stat = %+v, %v; want %+v", st, err, z)
	}
	if got, err := filesource.ReadRange(ctx, s, z, 4, 3); err != nil || string(got) != "two" {
		t.Errorf("ReadRange = %q, %v", got, err)
	}

	// After a new revision, the old one is gone.
	fd.files[4].rev = "r3"
	if _, err := s.OpenRange(ctx, z, 0, 3); !errors.Is(err, filesource.ErrChanged) {
		t.Errorf("OpenRange of a replaced revision: %v; want ErrChanged", err)
	}
}

// TestRateLimit checks that requests that Drive rejects for a rate limit
// are retried, after waiting.
func TestRateLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fd := newFakeDrive()
		fd.limit.Store(3)
		ts := httptest.NewTestServer(t, fd)
		s := &Source{Client: ts.Client(), APIBase: ts.URL}
		start := time.Now()
		f, err := s.Stat(context.Background(), "z1")
		if err != nil || f.Version != "r1" {
			t.Fatalf("Stat = %+v, %v", f, err)
		}
		if waited := time.Since(start); waited < 7*time.Second {
			t.Errorf("waited %v for 3 rejections; want at least 1s+2s+4s", waited)
		}
	})
}

func TestFolderID(t *testing.T) {
	const id = "1tC6XLonVc2kyD-JHbu6_L59cOwFPuI4B"
	for _, s := range []string{
		id,
		" " + id + "\n",
		"https://drive.google.com/drive/folders/" + id,
		"https://drive.google.com/drive/folders/" + id + "?usp=sharing",
		"https://drive.google.com/drive/u/1/folders/" + id + "/",
		"https://drive.google.com/open?id=" + id,
	} {
		if got, err := FolderID(s); err != nil || got != id {
			t.Errorf("FolderID(%q) = %q, %v; want %q", s, got, err, id)
		}
	}
	for _, s := range []string{
		"",
		"https://drive.google.com/drive/my-drive",
		"https://evil.example/drive/folders/" + id,
		"https://drive.google.com/drive/folders/a'b",
		"not an id",
	} {
		if got, err := FolderID(s); err == nil {
			t.Errorf("FolderID(%q) = %q; want an error", s, got)
		}
	}
}
