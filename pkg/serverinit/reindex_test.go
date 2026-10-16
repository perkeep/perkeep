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

package serverinit_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	_ "perkeep.org/pkg/blobserver/localdisk"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/serverinit"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/sqlite"
	"perkeep.org/pkg/test"
)

type reindexInstaller struct {
	patterns map[string]bool
}

func (hi *reindexInstaller) Handle(pattern string, _ http.Handler) {
	hi.patterns[pattern] = true
}

func sqliteReindexConfig(blobDir, dbFile string, reindex, keepGoing bool) *serverinit.Config {
	return serverinit.ExportNewConfigFromObj(jsonconfig.Obj{
		"auth": "none",
		"prefixes": map[string]any{
			"/bs/": map[string]any{
				"handler": "storage-filesystem", "internal": true,
				"handlerArgs": map[string]any{"path": blobDir},
			},
			"/index/": map[string]any{
				"handler": "storage-index",
				"handlerArgs": map[string]any{
					"blobSource": "/bs/", "reindex": reindex, "keepGoing": keepGoing,
					"storage": map[string]any{"type": "sqlite", "file": dbFile},
				},
			},
		},
	})
}

// Write directly to the filesystem storage's layout to model a damaged on-disk
// raw blob. Uploading through blobserver.Receive would reject the corruption
// before it could reach the startup reindex path being tested.
func writeReindexBlob(t *testing.T, root string, br blob.Ref, contents string) string {
	t.Helper()
	digest := br.Digest()
	path := filepath.Join(root, br.HashName(), digest[:2], digest[2:4], br.String()+".dat")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSQLiteReindexStartupRejectsCorruptBlobAndRecovers(t *testing.T) {
	test.TLog(t)
	t.Setenv("CAMLI_REINDEX_START", "")
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	root := t.TempDir()
	blobDir := filepath.Join(root, "blobs")
	dbFile := filepath.Join(root, "index.sqlite")
	contents := "raw blob whose original content is restored for recovery"
	br := blob.RefFromString(contents)
	blobPath := writeReindexBlob(t, blobDir, br, "corrupt raw bytes with the wrong digest")

	hi := &reindexInstaller{patterns: make(map[string]bool)}
	shutdown, err := sqliteReindexConfig(blobDir, dbFile, true, false).InstallHandlers(hi, "http://example.invalid")
	if shutdown != nil {
		shutdown.Close()
	}
	// InstallHandlers wraps its recovered setup panic as text, rather
	// than retaining an errors.Is chain to the original index error.
	if err == nil || !strings.Contains(err.Error(), index.ErrReindexIncomplete.Error()) {
		t.Fatalf("InstallHandlers with corrupt source = %v; want incomplete batched reindex", err)
	}
	if hi.patterns["/index/camli/"] {
		t.Error("startup published the failed index despite incomplete rebuild")
	}

	// The failed startup must have released the session and left its
	// persistent marker. Reopening returns a live KV for recovery.
	marked, err := sqlite.NewStorage(dbFile)
	var needWipe sorted.NeedWipeError
	if !errors.As(err, &needWipe) || marked == nil {
		if marked != nil {
			marked.Close()
		}
		t.Fatalf("ordinary SQLite reopen = %T, %v; want live KV plus NeedWipeError", marked, err)
	}
	if err := marked.Close(); err != nil {
		t.Fatal(err)
	}

	// Fixing the source alone cannot silently bless the partial DB:
	// normal startup must still reject it, even with keepGoing=true.
	if err := os.WriteFile(blobPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	hi = &reindexInstaller{patterns: make(map[string]bool)}
	shutdown, err = sqliteReindexConfig(blobDir, dbFile, false, true).InstallHandlers(hi, "http://example.invalid")
	if shutdown != nil {
		shutdown.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "incomplete reindex") {
		t.Fatalf("ordinary startup on marked DB = %v; want incomplete-reindex rejection", err)
	}
	if hi.patterns["/index/camli/"] {
		t.Error("normal startup published a marked index")
	}

	// --reindex must accept the returned NeedWipe KV, atomically wipe
	// and mark it again, finish the rebuild, and only then publish it.
	hi = &reindexInstaller{patterns: make(map[string]bool)}
	shutdown, err = sqliteReindexConfig(blobDir, dbFile, true, true).InstallHandlers(hi, "http://example.invalid")
	if err != nil {
		t.Fatalf("reindex startup recovery: %v", err)
	}
	if !hi.patterns["/index/camli/"] {
		t.Error("successful recovery did not publish index handler")
	}
	if err := shutdown.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.NewStorage(dbFile)
	if err != nil {
		t.Fatalf("ordinary reopen after startup recovery: %v", err)
	}
	defer reopened.Close()
	value, err := reopened.Get("have:" + br.String())
	if err != nil || !strings.HasSuffix(value, "|indexed") {
		t.Errorf("recovered have:%v = %q, %v; want indexed", br, value, err)
	}
	idx, err := index.New(reopened)
	if err != nil {
		t.Fatalf("index.New after recovery: %v", err)
	}
	if idx == nil {
		t.Fatal("index.New returned nil after recovery")
	}
}

// TestSQLiteReindexStartupKeepGoingSkipsCorruptBlob tests that with
// keepGoing, a blob that fails to index doesn't fail the whole rebuild: the
// index is committed as complete without it, and startup publishes it.
func TestSQLiteReindexStartupKeepGoingSkipsCorruptBlob(t *testing.T) {
	test.TLog(t)
	t.Setenv("CAMLI_REINDEX_START", "")
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	root := t.TempDir()
	blobDir := filepath.Join(root, "blobs")
	dbFile := filepath.Join(root, "index.sqlite")
	good := "raw blob with the right digest"
	goodRef := blob.RefFromString(good)
	writeReindexBlob(t, blobDir, goodRef, good)
	badRef := blob.RefFromString("raw blob's original contents")
	writeReindexBlob(t, blobDir, badRef, "corrupt raw bytes with the wrong digest")

	hi := &reindexInstaller{patterns: make(map[string]bool)}
	shutdown, err := sqliteReindexConfig(blobDir, dbFile, true, true).InstallHandlers(hi, "http://example.invalid")
	if err != nil {
		t.Fatalf("InstallHandlers with keepGoing and a corrupt blob: %v", err)
	}
	if !hi.patterns["/index/camli/"] {
		t.Error("keepGoing startup did not publish the index")
	}
	if err := shutdown.Close(); err != nil {
		t.Fatal(err)
	}

	// The rebuild must be committed as complete: an ordinary reopen
	// succeeds, with the good blob indexed and the corrupt one absent.
	reopened, err := sqlite.NewStorage(dbFile)
	if err != nil {
		t.Fatalf("ordinary reopen after keepGoing rebuild: %v", err)
	}
	defer reopened.Close()
	if value, err := reopened.Get("have:" + goodRef.String()); err != nil || !strings.HasSuffix(value, "|indexed") {
		t.Errorf("have:%v = %q, %v; want indexed", goodRef, value, err)
	}
	if value, err := reopened.Get("have:" + badRef.String()); !errors.Is(err, sorted.ErrNotFound) {
		t.Errorf("have:%v = %q, %v; want not found", badRef, value, err)
	}
}
