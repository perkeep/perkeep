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

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"perkeep.org/pkg/client"
)

// TestUploadTreeSameAsTreeUpload checks that uploading a directory with
// upload.Tree makes the same root directory schema blob TreeUpload does,
// and so the same blobs under it.
func TestUploadTreeSameAsTreeUpload(t *testing.T) {
	testWithTempDir(t, func(tempDir string) {
		root := filepath.Join(tempDir, "to_upload")
		for _, d := range []string{"", "sub", "sub/deeper", "empty"} {
			mustMkdir(t, filepath.Join(root, d), 0o700)
		}
		for name, contents := range map[string]string{
			"small.txt":        "hello",
			"big.bin":          strings.Repeat("big, but not a repeating chunk? ", 40000),
			"sub/a.txt":        "a",
			"sub/deeper/b.txt": "b",
		} {
			if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink("small.txt", filepath.Join(root, "link")); err != nil {
			t.Fatal(err)
		}

		upload := func(oldWay bool) string {
			t.Helper()
			testHookNoUploadTree = oldWay
			defer func() { testHookNoUploadTree = false }()
			blobDir := t.TempDir()
			e := &env{}
			stdout, stderr, exit := e.Run("--blobdir="+blobDir, "--havecache=false", "file", root)
			if exit != 0 {
				t.Fatalf("exit status %d: stdout=[%s], stderr=[%s]", exit, stdout, stderr)
			}
			refs := strings.Fields(string(stdout))
			if len(refs) == 0 {
				t.Fatalf("no output; stderr=[%s]", stderr)
			}
			return refs[len(refs)-1]
		}
		if got, want := upload(false), upload(true); got != want {
			t.Errorf("upload.Tree made root directory %s; TreeUpload made %s", got, want)
		}
	})
}

func TestTreeArgPath(t *testing.T) {
	for _, tt := range []struct {
		dir, rel, p, want string
	}{
		{"photos", "home/x/tmp/photos", "home/x/tmp/photos", "photos"},
		{"photos", "home/x/tmp/photos", "home/x/tmp/photos/a/b.jpg", "photos/a/b.jpg"},
		{"/home/x", "home/x", "home/x/.hidden", "/home/x/.hidden"},
		{"/", ".", ".hidden", "/.hidden"},
		{"/", ".", ".", "/"},
	} {
		want := filepath.FromSlash(tt.want)
		if got := treeArgPath(filepath.FromSlash(tt.dir), tt.rel, tt.p); got != want {
			t.Errorf("treeArgPath(%q, %q, %q) = %q; want %q", tt.dir, tt.rel, tt.p, got, want)
		}
	}
}

// TestTreeIgnoredFiles checks that the client's rules of ignored files
// apply to a tree's paths as given on the command line: a relative rule
// matching a component of the tree's absolute path, but not of its paths
// as given, ignores nothing.
func TestTreeIgnoredFiles(t *testing.T) {
	testWithTempDir(t, func(string) {
		t.Setenv("CAMLI_IGNORED_FILES", "tmp,*.go")
		c, err := client.New(client.OptionServer("http://example.com"))
		if err != nil {
			t.Fatal(err)
		}
		for _, tt := range []struct {
			p       string
			ignored bool
		}{
			{"home/x/tmp/photos/a.jpg", false},
			{"home/x/tmp/photos/tmp/a.jpg", true},
			{"home/x/tmp/photos/a.go", true},
		} {
			if got := c.IsIgnoredFile(treeArgPath("photos", "home/x/tmp/photos", tt.p)); got != tt.ignored {
				t.Errorf("ignored(%q) = %v; want %v", tt.p, got, tt.ignored)
			}
		}
	})
}
