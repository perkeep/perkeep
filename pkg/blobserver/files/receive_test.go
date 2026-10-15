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

	// The directories are known to be durable now.
	receive()
	check("second receive", afterMkdir...)

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
