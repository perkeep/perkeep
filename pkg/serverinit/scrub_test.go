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

package serverinit_test

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"perkeep.org/pkg/blob"
	_ "perkeep.org/pkg/blobserver/cond"
	_ "perkeep.org/pkg/blobserver/localdisk"
	_ "perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/health"
	"perkeep.org/pkg/serverinit"
	"perkeep.org/pkg/test"
)

// TestScrub checks that a storage's scrub finds a blob whose contents
// on disk don't match its digest, and that the storage made of others
// isn't scrubbed itself.
func TestScrub(t *testing.T) {
	test.TLog(t)
	t.Setenv("CAMLI_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		confData, err := json.Marshal(map[string]any{
			"handlerConfig": true,
			"auth":          "none",
			"prefixes": map[string]any{
				"/a/": map[string]any{
					"handler":     "storage-filesystem",
					"handlerArgs": map[string]any{"path": dir},
				},
				"/b/": map[string]any{"handler": "storage-memory"},
				"/cond/": map[string]any{
					"handler":     "storage-cond",
					"handlerArgs": map[string]any{"write": "/a/", "read": "/a/"},
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		conf, err := serverinit.Load(confData)
		if err != nil {
			t.Fatal(err)
		}
		mon := health.New()
		defer mon.Close()
		conf.SetHealth(mon)
		mux := http.NewServeMux()
		shutdown, err := conf.InstallHandlers(mux, "http://example.invalid")
		if err != nil {
			t.Fatal(err)
		}
		defer shutdown.Close()

		put := func(pfx, contents string) blob.Ref {
			t.Helper()
			br := blob.RefFromString(contents)
			req := httptest.NewRequest("PUT", pfx+"camli/"+br.String(), strings.NewReader(contents))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code >= 300 {
				t.Fatalf("PUT to %s: %d %s", pfx, rec.Code, rec.Body)
			}
			return br
		}
		var refs []blob.Ref
		for _, s := range []string{"one", "two", "three", "four"} {
			refs = append(refs, put("/a/", s))
			put("/b/", s)
		}

		// Damage one blob on disk, keeping its size.
		bad := refs[2]
		var damaged bool
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.Contains(d.Name(), bad.String()) {
				if err := os.WriteFile(path, []byte("THREE"), 0644); err != nil {
					t.Fatal(err)
				}
				damaged = true
			}
			return nil
		})
		if !damaged {
			t.Fatalf("didn't find %v's file in %s", bad, dir)
		}

		mon.EndStartup()
		synctest.Wait() // for the census
		for _, id := range []string{"storage.scrub[a]", "storage.scrub[b]"} {
			if err := mon.RunNow(id); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()

		checks := map[string]health.CheckStatus{}
		for _, c := range mon.Status().Checks {
			checks[c.ID] = c
		}
		if _, ok := checks["storage.scrub[cond]"]; ok {
			t.Errorf("/cond/, made of /a/, has a scrub of its own")
		}
		a := checks["storage.scrub[a]"]
		if a.State != health.StateFailed || !strings.Contains(a.Message, bad.String()+": doesn't match its digest") || !strings.Contains(a.Message, "1 of 1 blobs are bad") {
			t.Errorf("scrub of /a/ = %v %q; want failed, naming %v", a.State, a.Message, bad)
		}
		if a.Fatal || mon.ReadOnlyError() != nil {
			t.Errorf("a bad blob made the server read-only")
		}
		if b := checks["storage.scrub[b]"]; b.State != health.StateOK {
			t.Errorf("scrub of /b/ = %v %q; want ok", b.State, b.Message)
		}
	})
}
