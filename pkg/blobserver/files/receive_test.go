/*
Copyright 2026 The Perkeep Authors.

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

package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"perkeep.org/pkg/blob"
)

// recordingFS is the host filesystem, recording the operations that
// matter for durability, with paths relative to root.
type recordingFS struct {
	osFS
	root string

	mu  sync.Mutex
	ops []string
}

func (fs *recordingFS) record(op, path string) {
	rel, err := filepath.Rel(fs.root, path)
	if err != nil {
		panic(err)
	}
	if op == "sync file in" {
		rel = filepath.Dir(rel) // temp file names are random
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.ops = append(fs.ops, op+" "+filepath.ToSlash(rel))
}

func (fs *recordingFS) takeOps() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	ops := fs.ops
	fs.ops = nil
	return ops
}

func (fs *recordingFS) MkdirAll(path string, perm os.FileMode) error {
	fs.record("mkdirall", path)
	return fs.osFS.MkdirAll(path, perm)
}

func (fs *recordingFS) Rename(oldname, newname string) error {
	fs.record("rename", newname)
	return fs.osFS.Rename(oldname, newname)
}

func (fs *recordingFS) SyncDir(dir string) error {
	fs.record("syncdir", dir)
	return fs.osFS.SyncDir(dir)
}

func (fs *recordingFS) TempFile(dir, prefix string) (WritableFile, error) {
	f, err := fs.osFS.TempFile(dir, prefix)
	if err != nil {
		return nil, err
	}
	return recordingFile{f, fs}, nil
}

type recordingFile struct {
	WritableFile
	fs *recordingFS
}

func (f recordingFile) Sync() error {
	f.fs.record("sync file in", f.Name())
	return f.WritableFile.Sync()
}

func TestReceiveSyncsDirs(t *testing.T) {
	root := t.TempDir()
	fs := &recordingFS{root: root}
	ds := NewStorage(fs, root)
	br := blob.RefFromString("foo")
	d := br.Digest()
	hashDir := br.HashName()
	dir := hashDir + "/" + d[0:2] + "/" + d[2:4]
	receive := func() {
		t.Helper()
		if _, err := ds.ReceiveBlob(context.Background(), br, strings.NewReader("foo")); err != nil {
			t.Fatal(err)
		}
	}
	check := func(what string, want ...string) {
		t.Helper()
		if got := fs.takeOps(); !slices.Equal(got, want) {
			t.Errorf("%s: ops =\n\t%s\nwant\n\t%s", what, strings.Join(got, "\n\t"), strings.Join(want, "\n\t"))
		}
	}
	afterMkdir := []string{
		"sync file in " + dir,
		"rename " + dir + "/" + blobFileBaseName(br),
		"syncdir " + dir,
	}

	receive()
	check("first receive", append([]string{
		"mkdirall " + dir,
		"syncdir .",
		"syncdir " + hashDir,
		"syncdir " + hashDir + "/" + d[0:2],
	}, afterMkdir...)...)

	// The directories are known to be durable now, and the blob is
	// already stored, so it isn't replaced; but its directory is synced,
	// in case another receive of it hasn't done that yet.
	receive()
	check("second receive", "syncdir "+dir)

	// A stored copy of the wrong size (say, truncated) is replaced.
	if err := os.WriteFile(ds.blobPath(br), []byte("fo"), 0600); err != nil {
		t.Fatal(err)
	}
	receive()
	check("receive over a truncated copy", afterMkdir...)
	if got, err := os.ReadFile(ds.blobPath(br)); err != nil || string(got) != "foo" {
		t.Fatalf("stored blob = %q, %v; want %q", got, err, "foo")
	}

	// Removing a directory forgets which ones are durable.
	ds.tryRemoveDir(filepath.Join(root, filepath.FromSlash(dir)))
	fs.takeOps()
	receive()
	check("receive after removal", append([]string{
		"mkdirall " + dir,
		"syncdir .",
		"syncdir " + hashDir,
		"syncdir " + hashDir + "/" + d[0:2],
	}, afterMkdir...)...)
}

func newBlob(s string) *blob.Blob {
	return blob.NewBlob(blob.RefFromString(s), uint32(len(s)), func(context.Context) ([]byte, error) {
		return []byte(s), nil
	})
}

// sameDirBlobs returns n blobs whose refs share a directory.
func sameDirBlobs(n int) []*blob.Blob {
	var blobs []*blob.Blob
	var prefix string
	for i := 0; len(blobs) < n; i++ {
		s := fmt.Sprintf("blob %d", i)
		p := blob.RefFromString(s).Digest()[:4]
		if prefix == "" {
			prefix = p
		}
		if p == prefix {
			blobs = append(blobs, newBlob(s))
		}
	}
	return blobs
}

// TestReceiveBlobsSyncsEachDirOnce checks that ReceiveBlobs syncs each
// directory holding its blobs once, after renaming its blobs into it.
func TestReceiveBlobsSyncsEachDirOnce(t *testing.T) {
	root := t.TempDir()
	fs := &recordingFS{root: root}
	ds := NewStorage(fs, root)
	blobs := append(sameDirBlobs(2), newBlob("other"))
	dirOf := func(b *blob.Blob) string {
		rel, err := filepath.Rel(root, ds.blobDirectory(b.Ref()))
		if err != nil {
			t.Fatal(err)
		}
		return filepath.ToSlash(rel)
	}
	if dirOf(blobs[0]) != dirOf(blobs[1]) || dirOf(blobs[0]) == dirOf(blobs[2]) {
		t.Fatal("test blobs aren't in the expected directories")
	}

	if err := ds.ReceiveBlobs(context.Background(), blobs); err != nil {
		t.Fatal(err)
	}
	ops := fs.takeOps()
	lastRename := map[string]int{} // dir to index in ops
	dirSyncs := map[string][]int{} // leaf dir to indexes in ops
	for i, op := range ops {
		if p, ok := strings.CutPrefix(op, "rename "); ok {
			lastRename[filepath.ToSlash(filepath.Dir(p))] = i
		}
		if d, ok := strings.CutPrefix(op, "syncdir "); ok {
			dirSyncs[d] = append(dirSyncs[d], i)
		}
	}
	for _, b := range blobs {
		dir := dirOf(b)
		syncs := dirSyncs[dir]
		if len(syncs) != 1 {
			t.Errorf("dir %s synced %d times; want once. ops:\n\t%s", dir, len(syncs), strings.Join(ops, "\n\t"))
			continue
		}
		if syncs[0] < lastRename[dir] {
			t.Errorf("dir %s synced before a rename into it. ops:\n\t%s", dir, strings.Join(ops, "\n\t"))
		}
	}
	for _, b := range blobs {
		got, err := os.ReadFile(ds.blobPath(b.Ref()))
		if err != nil {
			t.Fatal(err)
		}
		if blob.RefFromBytes(got) != b.Ref() {
			t.Errorf("%v has the wrong contents", b.Ref())
		}
	}

	// Receiving them again keeps the stored copies, still syncing
	// each directory once.
	if err := ds.ReceiveBlobs(context.Background(), blobs); err != nil {
		t.Fatal(err)
	}
	ops = fs.takeOps()
	syncs := map[string]int{}
	for _, op := range ops {
		if strings.HasPrefix(op, "rename ") || strings.HasPrefix(op, "sync file") {
			t.Errorf("receiving stored blobs again did %q", op)
		}
		if d, ok := strings.CutPrefix(op, "syncdir "); ok {
			syncs[d]++
		}
	}
	for _, b := range blobs {
		if n := syncs[dirOf(b)]; n != 1 {
			t.Errorf("receiving stored blobs again, dir %s synced %d times; want once", dirOf(b), n)
		}
	}
}

// plainFS hides osFS's SyncDir method.
type plainFS struct{ VFS }

func TestReceiveWithoutDirSyncer(t *testing.T) {
	root := t.TempDir()
	ds := NewStorage(plainFS{osFS{}}, root)
	br := blob.RefFromString("foo")
	for range 2 {
		if _, err := ds.ReceiveBlob(context.Background(), br, strings.NewReader("foo")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(ds.blobPath(br)); err != nil {
		t.Fatal(err)
	}
}
