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
	"flag"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/studio-b12/gowebdav"

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

	m.Run()
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
