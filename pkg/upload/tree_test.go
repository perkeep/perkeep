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

package upload

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
)

// testTree returns a tree of files, directories, and a symlink.
func testTree() fstest.MapFS {
	return fstest.MapFS{
		"top/small.txt":          {Data: []byte("hello"), ModTime: testModTime},
		"top/big.bin":            {Data: randBytes(40, 1<<20), ModTime: testModTime},
		"top/link":               {Data: []byte("small.txt"), Mode: fs.ModeSymlink},
		"top/sub/a.txt":          {Data: []byte("a"), ModTime: testModTime},
		"top/sub/deeper/b.bin":   {Data: randBytes(41, 300<<10), ModTime: testModTime},
		"top/empty":              {Mode: fs.ModeDir | 0o755, ModTime: testModTime},
		"top/sub/deeper/ignored": {Data: []byte("not uploaded")},
	}
}

func ignoreIgnored(path string, fi fs.FileInfo) bool { return fi.Name() == "ignored" }

// checkTree checks that the directory schema blob dirRef has what dir of
// fsys has, but for files named "ignored".
func checkTree(t *testing.T, c *client.Client, fsys fs.FS, dir string, dirRef blob.Ref) {
	t.Helper()
	b, err := c.FetchSchemaBlob(context.Background(), dirRef)
	if err != nil {
		t.Fatalf("fetching directory %s: %v", dir, err)
	}
	setRef, ok := b.DirectoryEntries()
	if !ok || b.Type() != schema.TypeDirectory {
		t.Fatalf("%s: %v is a %q; want a directory", dir, dirRef, b.Type())
	}
	set, err := c.FetchSchemaBlob(context.Background(), setRef)
	if err != nil {
		t.Fatal(err)
	}
	ents, err := fs.ReadDir(fsys, dir)
	if err != nil {
		t.Fatal(err)
	}
	// Files before directories, each by name.
	var files, dirs []string
	for _, ent := range ents {
		switch {
		case ent.Name() == "ignored":
		case ent.IsDir():
			dirs = append(dirs, ent.Name())
		default:
			files = append(files, ent.Name())
		}
	}
	want := append(files, dirs...)
	members := set.StaticSetMembers()
	var got []string
	for _, m := range members {
		mb, err := c.FetchSchemaBlob(context.Background(), m)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, mb.FileName())
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s: entries %q; want %q", dir, got, want)
	}
	for _, m := range members {
		mb, err := c.FetchSchemaBlob(context.Background(), m)
		if err != nil {
			t.Fatal(err)
		}
		p := dir + "/" + mb.FileName()
		fi, err := fs.Lstat(fsys, p)
		if err != nil {
			t.Fatalf("entry %q of %s: %v", mb.FileName(), dir, err)
		}
		switch {
		case fi.IsDir():
			checkTree(t, c, fsys, p, m)
		case fi.Mode()&fs.ModeSymlink != 0:
			var fields struct{ SymlinkTarget string }
			json.Unmarshal([]byte(mb.JSON()), &fields)
			want, _ := fs.ReadLink(fsys, p)
			if mb.Type() != schema.TypeSymlink || fields.SymlinkTarget != want {
				t.Errorf("%s = %q to %q; want a symlink to %q", p, mb.Type(), fields.SymlinkTarget, want)
			}
		default:
			data, err := fs.ReadFile(fsys, p)
			if err != nil {
				t.Fatal(err)
			}
			checkContents(t, c, m, data)
		}
	}
}

// orderChecker is a server handler wrapper checking, of each static set
// and file schema blob uploaded into an upload session, that the client
// knew the blobs it refers to were durable: that a flush reported them
// durable, or a stat found them on the server.
type orderChecker struct {
	next http.Handler

	mu         sync.Mutex
	durable    map[blob.Ref]bool // known to the client to be durable
	checked    int
	violations []string
}

func (oc *orderChecker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "camli/upload") && r.URL.Query().Get("session") != "":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		oc.check(r, body)
	case strings.HasSuffix(p, "camli/stat"), strings.HasSuffix(p, "/flush"):
		rec := httptest.NewRecorder()
		oc.next.ServeHTTP(rec, r)
		var res struct {
			Stat    []blob.SizedRef `json:"stat"`
			Durable []blob.SizedRef `json:"durable"`
		}
		if json.Unmarshal(rec.Body.Bytes(), &res) == nil {
			oc.mu.Lock()
			for _, sb := range append(res.Stat, res.Durable...) {
				oc.durable[sb.Ref] = true
			}
			oc.mu.Unlock()
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		w.Write(rec.Body.Bytes())
		return
	}
	oc.next.ServeHTTP(w, r)
}

func (oc *orderChecker) check(r *http.Request, body []byte) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := mr.NextPart()
		if err != nil {
			return
		}
		br, ok := blob.Parse(part.FormName())
		data, _ := io.ReadAll(part)
		if !ok {
			continue
		}
		b, err := schema.BlobFromReader(br, bytes.NewReader(data))
		if err != nil {
			continue // a chunk
		}
		var refs []blob.Ref
		switch b.Type() {
		case schema.TypeStaticSet:
			refs = append(b.StaticSetMembers(), b.StaticSetMergeSets()...)
		case schema.TypeFile:
			for _, p := range b.ByteParts() {
				refs = append(refs, p.BlobRef, p.BytesRef)
			}
		default:
			continue
		}
		oc.mu.Lock()
		for _, ref := range refs {
			if ref.Valid() && !oc.durable[ref] {
				oc.violations = append(oc.violations, fmt.Sprintf("%s %v uploaded before %v was known durable", b.Type(), br, ref))
			}
		}
		oc.checked++
		oc.mu.Unlock()
	}
}

// mapStatCache is a StatCache in a map.
type mapStatCache struct {
	mu sync.Mutex
	m  map[string]blob.Ref
}

func (c *mapStatCache) FileRef(path string, fi fs.FileInfo) (blob.Ref, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ref, ok := c.m[path]
	return ref, ok
}

func (c *mapStatCache) AddFileRef(path string, fi fs.FileInfo, fileRef blob.Ref) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[path] = fileRef
}

// TestTree checks uploading a tree: what it uploads, that nothing is
// uploaded referring to blobs not yet durable, and that a second upload
// with a StatCache reads no files and uploads nothing.
func TestTree(t *testing.T) {
	oc := &orderChecker{durable: make(map[blob.Ref]bool)}
	c, rc := newWrappedClient(t, func(h http.Handler) http.Handler {
		oc.next = h
		return oc
	})
	fsys := testTree()
	cache := &mapStatCache{m: make(map[string]blob.Ref)}
	tr := NewTree(c, fsys, "top", &TreeOptions{Ignore: ignoreIgnored, Cache: cache, OpenFiles: 2})
	res, err := tr.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rc.setPaused(true)
	checkTree(t, c, fsys, "top", res.DirRef)
	rc.setPaused(false)
	oc.mu.Lock()
	if len(oc.violations) > 0 || oc.checked == 0 {
		t.Errorf("checked %d blobs; violations: %q", oc.checked, oc.violations)
	}
	oc.mu.Unlock()
	p := tr.Progress()
	want := TreeProgress{Walked: true, Files: 5, Dirs: 4, FilesDone: 5, DirsDone: 4}
	want.Bytes = int64(len("hello") + 1<<20 + len("a") + 300<<10)
	want.BytesDone = want.Bytes
	if p != want {
		t.Errorf("Progress = %+v; want %+v", p, want)
	}
	if len(cache.m) != 4 {
		t.Errorf("cache has %d files; want the 4 regular ones: %v", len(cache.m), cache.m)
	}

	rc.take()
	tr = NewTree(c, fsys, "top", &TreeOptions{Ignore: ignoreIgnored, Cache: cache})
	again, err := tr.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again.DirRef != res.DirRef {
		t.Errorf("DirRef again = %v; want %v", again.DirRef, res.DirRef)
	}
	if p := tr.Progress(); p.FilesCached != 4 {
		t.Errorf("FilesCached = %d; want 4", p.FilesCached)
	}
	if n := rc.take()["upload"]; n != 0 {
		t.Errorf("uploading again made %d uploads; want none", n)
	}
}

// TestTreeLocalStorage checks uploading a tree with a client of local
// storage, which has no upload sessions.
func TestTreeLocalStorage(t *testing.T) {
	sto := new(memory.Storage)
	c, err := client.New(client.OptionUseStorageClient(sto))
	if err != nil {
		t.Fatal(err)
	}
	fsys := testTree()
	res, err := NewTree(c, fsys, "top", &TreeOptions{Ignore: ignoreIgnored}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkTree(t, c, fsys, "top", res.DirRef)
}

// TestTreeNotDir checks that a Tree of a file fails.
func TestTreeNotDir(t *testing.T) {
	c := newTestClient(t)
	if _, err := NewTree(c, testTree(), "top/small.txt", nil).Run(context.Background()); err == nil {
		t.Error("Run of a file succeeded")
	}
}
