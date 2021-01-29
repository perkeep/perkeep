//go:build linux

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

package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"bazil.org/fuse"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/test"
)

func TestRootLookupBlobRef(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		wantDir bool
		wantMut bool
		wantErr error
	}{
		{
			name:    "file",
			content: `{"camliVersion":1,"camliType":"file","fileName":"file"}`,
		},
		{
			name:    "directory",
			content: `{"camliVersion":1,"camliType":"directory","fileName":"dir"}`,
			wantDir: true,
		},
		{
			name:    "permanode",
			content: `{"camliVersion":1,"camliType":"permanode","random":"root-test"}`,
			wantDir: true,
			wantMut: true,
		},
		{
			name:    "invalid",
			content: "not a schema blob",
			wantErr: fuse.EIO,
		},
		{
			name:    "missing",
			wantErr: fuse.ENOENT,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := new(test.Fetcher)
			br := blob.RefFromString("missing")
			if tt.content != "" {
				b := &test.Blob{Contents: tt.content}
				fetcher.AddBlob(b)
				br = b.BlobRef()
			}
			cfs := newCamliFileSystem(fetcher)
			root := &root{fs: cfs}
			n, err := root.Lookup(context.Background(), br.String())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Lookup = %v; want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			var attr fuse.Attr
			if err := n.Attr(context.Background(), &attr); err != nil {
				t.Fatal(err)
			}
			if got := attr.Mode.IsDir(); got != tt.wantDir {
				t.Errorf("IsDir = %v; want %v", got, tt.wantDir)
			}
			dir, mutable := n.(*mutDir)
			if mutable != tt.wantMut {
				t.Fatalf("node type = %T; want mutable directory: %v", n, tt.wantMut)
			}
			if mutable && (dir.permanode != br || dir.children == nil) {
				t.Errorf("mutable directory not initialized: %#v", dir)
			}
		})
	}
}

func TestRootLookupFetchError(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want error
	}{
		{"missing", os.ErrNotExist, fuse.ENOENT},
		{"canceled", context.Canceled, fuse.EINTR},
		{"io", errors.New("fetch failed"), fuse.EIO},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := &test.Fetcher{FetchErr: func() error { return tt.err }}
			root := &root{fs: newCamliFileSystem(fetcher)}
			_, err := root.Lookup(context.Background(), blob.RefFromString(tt.name).String())
			if !errors.Is(err, tt.want) {
				t.Errorf("Lookup = %v; want %v", err, tt.want)
			}
		})
	}
}

func TestRootedFileRejected(t *testing.T) {
	fetcher := new(test.Fetcher)
	b := &test.Blob{Contents: `{"camliVersion":1,"camliType":"file","fileName":"file"}`}
	fetcher.AddBlob(b)
	if _, err := NewRootedCamliFileSystem(nil, fetcher, b.BlobRef()); err == nil {
		t.Fatal("mounting a file as the root should fail")
	}
}

func TestDirectoryPermanodeFromRoot(t *testing.T) {
	condSkip(t)
	pkmountTest(t, func(env *mountEnv) {
		br := env.world.NewPermanode(t)
		dir := filepath.Join(env.mountPoint, br.String(), "child")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		const content = "directory permanode accessed by blobref"
		name := filepath.Join(dir, "file")
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != content {
			t.Fatalf("ReadFile = %q; want %q", got, content)
		}
	})
}
