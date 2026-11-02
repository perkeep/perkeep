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
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/serverinit"
	"perkeep.org/pkg/types/serverconfig"

	// For the in-process server's handlers.
	_ "perkeep.org/pkg/blobserver/cond"
	_ "perkeep.org/pkg/blobserver/replica"
	_ "perkeep.org/pkg/importer"
	_ "perkeep.org/pkg/search"
	_ "perkeep.org/pkg/server"
)

// newTestPerkeep returns a function making clients of an in-process
// Perkeep server with memory storage and index. If wrap is non-nil, it
// wraps the clients' transport.
func newTestPerkeep(t *testing.T, wrap func(http.RoundTripper) http.RoundTripper) func(Account) (*client.Client, string, error) {
	t.Helper()
	srcRoot, err := osutil.PkSourceRoot()
	if err != nil {
		t.Fatalf("source root folder not found: %v", err)
	}
	conf := serverconfig.Config{
		Listen:             ":3179",
		Auth:               "userpass:foo:bar",
		Identity:           "26F5ABDA",
		IdentitySecretRing: filepath.Join(srcRoot, filepath.FromSlash("pkg/jsonsign/testdata/test-secring.gpg")),
		MemoryStorage:      true,
		MemoryIndex:        true,
	}
	confData, err := json.Marshal(conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAMLI_CONFIG_DIR", "whatever")
	lowConf, err := serverinit.Load(confData)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	shutdown, err := lowConf.InstallHandlers(mux, "http://example.com")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shutdown.Close() })
	ts := httptest.NewTestServer(t, mux)
	return func(Account) (*client.Client, string, error) {
		const base = "http://example.com"
		c, err := client.New(client.OptionServer(base), client.OptionNoExternalConfig(),
			client.OptionAuthMode(auth.NewBasicAuth("foo", "bar")))
		if err != nil {
			return nil, "", err
		}
		hc := *ts.Client() // a copy, as ts shares it
		if wrap != nil {
			hc.Transport = wrap(hc.Transport)
		}
		c.SetHTTPClient(&hc)
		return c, base, nil
	}
}

func writeRandomFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	b := make([]byte, size)
	rand.Read(b)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

type uploadsStatus struct {
	InFlight []struct {
		Key string `json:"key"`
	} `json:"inFlight"`
	Done   []uploadDone `json:"done"`
	Totals uploadTotals `json:"totals"`
}

func TestUploads(t *testing.T) {
	newClient := newTestPerkeep(t, nil)
	dataDir := t.TempDir()
	s := newTestServer(t, dataDir)
	s.testClient = newClient

	var a Account
	do(t, s, "POST", "/accounts", accountFields{Server: "example.com"}, http.StatusCreated, &a)
	path := "/accounts/" + a.ID
	do(t, s, "POST", path+"/hashes/lookup", map[string]any{"keys": []string{"x"}}, http.StatusConflict, nil) // not connected
	var conn struct{ Hashes []string }
	do(t, s, "POST", path+"/connect", map[string]string{}, http.StatusOK, &conn)
	if !slices.Contains(conn.Hashes, "sha224") {
		t.Fatalf("server's hashes = %v; want sha224", conn.Hashes)
	}

	files := t.TempDir()
	big := writeRandomFile(t, files, "big.heic", 3<<20) // over the session threshold, so chunked
	small := writeRandomFile(t, files, "small.jpg", 10<<10)

	type refsRes struct{ Refs map[string][]string }
	var cached refsRes
	do(t, s, "POST", path+"/hashes/lookup", map[string]any{"keys": []string{"big", "small"}}, http.StatusOK, &cached)
	if len(cached.Refs) != 0 {
		t.Fatalf("cached before hashing: %v", cached.Refs)
	}
	refs := map[string][]string{}
	for key, p := range map[string]string{"big": big, "small": small} {
		var res struct{ Refs []string }
		do(t, s, "POST", path+"/hashes/file", map[string]string{"key": key, "path": p}, http.StatusOK, &res)
		if len(res.Refs) != len(conn.Hashes) {
			t.Fatalf("hashing %s: refs %v; want one per hash %v", key, res.Refs, conn.Hashes)
		}
		refs[key] = res.Refs
	}
	do(t, s, "POST", path+"/hashes/lookup", map[string]any{"keys": []string{"big", "small", "other"}}, http.StatusOK, &cached)
	if !slices.Equal(cached.Refs["big"], refs["big"]) || !slices.Equal(cached.Refs["small"], refs["small"]) || cached.Refs["other"] != nil {
		t.Fatalf("cached = %v; want %v", cached.Refs, refs)
	}

	type missingRes struct {
		Present, Missing []string
		Error            string
	}
	findMissing := func() missingRes {
		t.Helper()
		var res missingRes
		do(t, s, "POST", path+"/findmissing", map[string]any{"items": []map[string]any{
			{"key": "big", "refs": refs["big"]},
			{"key": "small", "refs": refs["small"]},
		}}, http.StatusOK, &res)
		if res.Error != "" {
			t.Fatalf("findmissing error: %v", res.Error)
		}
		return res
	}
	if got := findMissing(); len(got.Present) != 0 || len(got.Missing) != 2 {
		t.Fatalf("before uploading: %+v; want both missing", got)
	}

	modTime := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	start := func(key, p, name string) {
		t.Helper()
		do(t, s, "POST", path+"/uploads", map[string]any{
			"key": key, "path": p, "name": name, "modTime": modTime, "refs": refs[key],
		}, http.StatusOK, nil)
	}
	start("big", big, "IMG_0001.HEIC")
	start("small", small, "IMG_0002.JPG")
	ac, err := s.conn(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	ac.running.Wait()

	var st uploadsStatus
	do(t, s, "GET", path+"/uploads", nil, http.StatusOK, &st)
	if len(st.InFlight) != 0 || len(st.Done) != 2 {
		t.Fatalf("status = %+v; want 2 done", st)
	}
	for _, d := range st.Done {
		if d.Error != "" || d.FileRef == "" || d.Dup {
			t.Errorf("done = %+v; want a new upload", d)
		}
	}
	if st.Totals.FilesDone != 2 || st.Totals.ChunksUploaded < 3 || st.Totals.BytesUploaded < 3<<20 {
		t.Errorf("totals = %+v", st.Totals)
	}
	do(t, s, "GET", path+"/uploads", nil, http.StatusOK, &st)
	if len(st.Done) != 0 {
		t.Errorf("done reported twice: %+v", st.Done)
	}

	// Now the server has both, as the cache says, and as a search finds
	// for a connection without the cache.
	if got := findMissing(); len(got.Present) != 2 {
		t.Errorf("after uploading: %+v; want both present", got)
	}
	removeHaveFiles(dataDir, a.ID, "")
	do(t, s, "POST", path+"/connect", map[string]string{}, http.StatusOK, nil)
	if got := findMissing(); len(got.Present) != 2 {
		t.Errorf("after uploading, uncached: %+v; want both present", got)
	}

	// Uploading again finds the contents on the server.
	start("small", small, "IMG_0002.JPG")
	ac, _ = s.conn(a.ID)
	ac.running.Wait()
	do(t, s, "GET", path+"/uploads", nil, http.StatusOK, &st)
	if len(st.Done) != 1 || st.Done[0].Error != "" {
		t.Fatalf("re-upload: %+v", st.Done)
	}

	// The hash cache persists.
	s2 := newTestServer(t, dataDir)
	s2.testClient = newClient
	do(t, s2, "POST", path+"/connect", map[string]string{}, http.StatusOK, nil)
	do(t, s2, "POST", path+"/hashes/lookup", map[string]any{"keys": []string{"big"}}, http.StatusOK, &cached)
	if !slices.Equal(cached.Refs["big"], refs["big"]) {
		t.Errorf("after restart, cached = %v", cached.Refs)
	}
}

func TestLogCache(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.jsonl")
	c, err := openLogCache[[]string](p)
	if err != nil {
		t.Fatal(err)
	}
	c.put("a", []string{"1"})
	c.put("b", []string{"2"})
	c.put("a", []string{"3"})
	c.close()
	// A partial last line, as from a crash, is skipped.
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.Write([]byte(`{"k":"c","v":[`))
	f.Close()

	c, err = openLogCache[[]string](p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if v, _ := c.get("a"); !slices.Equal(v, []string{"3"}) {
		t.Errorf("a = %v", v)
	}
	if v, _ := c.get("b"); !slices.Equal(v, []string{"2"}) {
		t.Errorf("b = %v", v)
	}
	if c.len() != 2 {
		t.Errorf("len = %d", c.len())
	}
}
