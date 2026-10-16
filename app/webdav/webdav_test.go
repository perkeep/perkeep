/*
Copyright 2025 The Perkeep Authors

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

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"testing"
	"time"

	"github.com/studio-b12/gowebdav"

	"perkeep.org/pkg/app"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/test"
)

var w *test.World

func TestMain(m *testing.M) {
	flag.Parse()

	if testing.Short() {
		log.Println("Skipping WebDAV App tests in short mode")
		os.Exit(0)
	}

	var err error
	if w, err = test.NewWorld(); err != nil {
		log.Fatal(err)
	}
	if err = w.Start(); err != nil {
		log.Fatal(err)
	}
	defer w.Stop()
	if err := waitForApp(); err != nil {
		w.Stop()
		log.Fatal(err)
	}

	m.Run()
}

// waitForApp waits for the webdav app, which perkeepd starts
// asynchronously, to be serving requests through perkeepd.
func waitForApp() error {
	c := newClient()
	var err error
	for range 600 {
		if err = c.Connect(); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("webdav app never became reachable: %w", err)
}

func newClient() *gowebdav.Client {
	return gowebdav.NewClient(
		w.ServerBaseURL()+"/webdav/",
		"testuser",
		"passTestWorld",
	)
}

func TestWebdavFileLifecycle(t *testing.T) {
	c := newClient()

	fileName := fmt.Sprintf("lifecycle-%d.txt", time.Now().UnixNano())
	initialContent := []byte("Hello, WebDAV!")
	updatedContent := []byte("Updated content with more data")

	if err := c.Write(fileName, initialContent, 0644); err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	data, err := c.Read(fileName)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(data) != string(initialContent) {
		t.Errorf("Initial content mismatch: got %q, want %q", data, initialContent)
	}

	files, err := c.ReadDir("/")
	if err != nil {
		t.Fatalf("Failed to list root: %v", err)
	}
	found := false
	for _, f := range files {
		if f.Name() == fileName {
			found = true
			if f.Size() != int64(len(initialContent)) {
				t.Errorf("Size in listing wrong: got %d, want %d", f.Size(), len(initialContent))
			}
			break
		}
	}
	if !found {
		t.Error("File not found in directory listing")
	}

	if err := c.Write(fileName, updatedContent, 0644); err != nil {
		t.Fatalf("Failed to overwrite file: %v", err)
	}

	data, err = c.Read(fileName)
	if err != nil {
		t.Fatalf("Failed to read updated file: %v", err)
	}
	if string(data) != string(updatedContent) {
		t.Errorf("Updated content mismatch: got %q, want %q", data, updatedContent)
	}

	if err := c.Remove(fileName); err != nil {
		t.Fatalf("Failed to delete file: %v", err)
	}

	_, err = c.Stat(fileName)
	if !gowebdav.IsErrNotFound(err) {
		t.Errorf("File should not exist after delete, got error: %v", err)
	}
}

func TestWebdavDirectoryStructure(t *testing.T) {
	c := newClient()

	baseDir := fmt.Sprintf("photos-%d", time.Now().UnixNano())

	if err := c.MkdirAll(baseDir+"/2024/01", 0755); err != nil {
		t.Fatalf("Failed to create 2024/01: %v", err)
	}
	if err := c.MkdirAll(baseDir+"/2024/02", 0755); err != nil {
		t.Fatalf("Failed to create 2024/02: %v", err)
	}

	files := map[string][]byte{
		baseDir + "/2024/01/photo1.jpg": []byte("fake jpeg data 1"),
		baseDir + "/2024/01/photo2.jpg": []byte("fake jpeg data 2"),
		baseDir + "/2024/02/photo3.jpg": []byte("fake jpeg data 3"),
	}
	for path, content := range files {
		if err := c.Write(path, content, 0644); err != nil {
			t.Fatalf("Failed to write %s: %v", path, err)
		}
	}

	jan, err := c.ReadDir(baseDir + "/2024/01")
	if err != nil {
		t.Fatalf("Failed to list 2024/01: %v", err)
	}
	if len(jan) != 2 {
		t.Errorf("Expected 2 files in 2024/01, got %d", len(jan))
	}

	feb, err := c.ReadDir(baseDir + "/2024/02")
	if err != nil {
		t.Fatalf("Failed to list 2024/02: %v", err)
	}
	if len(feb) != 1 {
		t.Errorf("Expected 1 file in 2024/02, got %d", len(feb))
	}

	data, err := c.Read(baseDir + "/2024/01/photo1.jpg")
	if err != nil {
		t.Fatalf("Failed to read nested file: %v", err)
	}
	if string(data) != "fake jpeg data 1" {
		t.Errorf("Nested file content wrong: got %q", data)
	}

	if err := c.RemoveAll(baseDir); err != nil {
		t.Fatalf("Failed to remove directory tree: %v", err)
	}

	_, err = c.Stat(baseDir)
	if !gowebdav.IsErrNotFound(err) {
		t.Errorf("Directory tree should be gone after RemoveAll")
	}
}

func TestWebdavErrorHandling(t *testing.T) {
	c := newClient()

	_, err := c.Read("this-file-does-not-exist-anywhere.txt")
	if !gowebdav.IsErrNotFound(err) {
		t.Errorf("Expected NotFound for non-existent file, got: %v", err)
	}

	_, err = c.Stat("another-missing-file.txt")
	if !gowebdav.IsErrNotFound(err) {
		t.Errorf("Expected NotFound for stat on missing file, got: %v", err)
	}

	_, err = c.ReadDir("non-existent-directory")
	if !gowebdav.IsErrNotFound(err) {
		t.Errorf("Expected NotFound for non-existent directory, got: %v", err)
	}
}

func TestWebdavLargeFile(t *testing.T) {
	c := newClient()

	fileName := fmt.Sprintf("largefile-%d.bin", time.Now().UnixNano())

	size := 1024 * 1024
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 256)
	}

	if err := c.Write(fileName, content, 0644); err != nil {
		t.Fatalf("Failed to upload large file: %v", err)
	}

	info, err := c.Stat(fileName)
	if err != nil {
		t.Fatalf("Failed to stat large file: %v", err)
	}
	if info.Size() != int64(size) {
		t.Errorf("Size mismatch: got %d, want %d", info.Size(), size)
	}

	data, err := c.Read(fileName)
	if err != nil {
		t.Fatalf("Failed to download large file: %v", err)
	}
	if len(data) != size {
		t.Errorf("Downloaded size mismatch: got %d, want %d", len(data), size)
	}

	for _, i := range []int{0, 1000, 50000, size - 1} {
		if data[i] != byte(i%256) {
			t.Errorf("Content corruption at byte %d: got %d, want %d", i, data[i], i%256)
		}
	}

	if err := c.Remove(fileName); err != nil {
		t.Logf("Cleanup failed: %v", err)
	}
}

// newTestFS returns a perkeepFS rooted at a new, empty directory
// permanode, talking directly to the World's perkeepd.
func newTestFS(t *testing.T) *perkeepFS {
	t.Helper()
	test.TLog(t)
	cl, err := client.New(client.OptionServer(w.ServerBaseURL()))
	if err != nil {
		t.Fatal(err)
	}
	signer := app.NewSigner(cl, w.ServerBaseURL())
	conf := &config{RootName: fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())}
	root, err := findOrCreateRootPermanode(t.Context(), cl, signer, conf)
	if err != nil {
		t.Fatal(err)
	}
	return newPerkeepFS(cl, signer, root)
}

func writeFile(t *testing.T, pfs *perkeepFS, name, contents string) {
	t.Helper()
	f, err := pfs.OpenFile(t.Context(), name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(f, contents); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, pfs *perkeepFS, name string) string {
	t.Helper()
	f, err := pfs.OpenFile(t.Context(), name, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFSOpenFileExcl(t *testing.T) {
	pfs := newTestFS(t)
	ctx := t.Context()
	writeFile(t, pfs, "/f", "hi")

	_, err := pfs.OpenFile(ctx, "/f", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0644)
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("O_EXCL open of existing file: got %v, want ErrExist", err)
	}
	f, err := pfs.OpenFile(ctx, "/g", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		t.Fatalf("O_EXCL open of new file: %v", err)
	}
	f.Close()
}

func TestFSMkdir(t *testing.T) {
	pfs := newTestFS(t)
	ctx := t.Context()

	if err := pfs.Mkdir(ctx, "/d", 0755); err != nil {
		t.Fatal(err)
	}
	if err := pfs.Mkdir(ctx, "/d", 0755); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Mkdir of existing dir: got %v, want ErrExist", err)
	}
	if err := pfs.Mkdir(ctx, "/missing/d", 0755); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Mkdir in missing dir: got %v, want ErrNotExist", err)
	}
}

func TestFSRename(t *testing.T) {
	pfs := newTestFS(t)
	ctx := t.Context()

	writeFile(t, pfs, "/f", "f contents")
	writeFile(t, pfs, "/g", "g contents")
	for _, d := range []string{"/full", "/empty"} {
		if err := pfs.Mkdir(ctx, d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, pfs, "/full/child", "child contents")

	// Renaming to itself is a no-op, not a delete.
	if err := pfs.Rename(ctx, "/f", "/f"); err != nil {
		t.Fatalf("rename to self: %v", err)
	}
	if got := readFile(t, pfs, "/f"); got != "f contents" {
		t.Errorf("after rename to self, got %q", got)
	}

	for _, tt := range []struct{ from, to string }{
		{"/full", "/full/sub"}, // into itself
		{"/f", "/empty"},       // file onto dir
		{"/empty", "/f"},       // dir onto file
		{"/empty", "/full"},    // onto non-empty dir
		{"/f", "/g/x"},         // into a file
	} {
		if err := pfs.Rename(ctx, tt.from, tt.to); err == nil {
			t.Errorf("Rename(%q, %q) succeeded; want error", tt.from, tt.to)
		}
	}

	// Replacing an empty directory or a file works.
	if err := pfs.Rename(ctx, "/full", "/empty"); err != nil {
		t.Fatalf("rename onto empty dir: %v", err)
	}
	if got := readFile(t, pfs, "/empty/child"); got != "child contents" {
		t.Errorf("moved child contents = %q", got)
	}
	if _, err := pfs.Stat(ctx, "/full/child"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat of old child path: got %v, want ErrNotExist", err)
	}
	if err := pfs.Rename(ctx, "/f", "/g"); err != nil {
		t.Fatalf("rename onto file: %v", err)
	}
	if got := readFile(t, pfs, "/g"); got != "f contents" {
		t.Errorf("replaced file contents = %q", got)
	}
	if _, err := pfs.Stat(ctx, "/f"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat of old file path: got %v, want ErrNotExist", err)
	}
}

func TestFSModTime(t *testing.T) {
	pfs := newTestFS(t)
	ctx := t.Context()

	if err := pfs.Mkdir(ctx, "/d", 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, pfs, "/d/f", "x")

	for _, name := range []string{"/", "/d", "/d/f"} {
		fi, err := pfs.Stat(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if fi.ModTime().IsZero() {
			t.Errorf("Stat(%q).ModTime is zero", name)
		}
		f, err := pfs.OpenFile(ctx, name, os.O_RDONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		ffi, err := f.Stat()
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !ffi.ModTime().Equal(fi.ModTime()) {
			t.Errorf("%q: File.Stat ModTime = %v; FileSystem.Stat ModTime = %v", name, ffi.ModTime(), fi.ModTime())
		}
	}
}
