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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRemoveHaveFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{haveFile("a", "gen1"), haveFile("a", "gen2"), haveFile("b", "gen1"), hashesFile} {
		os.WriteFile(filepath.Join(dir, name), nil, 0o600)
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	removeHaveFiles(dir, "a", "gen2")
	if exists(haveFile("a", "gen1")) || !exists(haveFile("a", "gen2")) || !exists(haveFile("b", "gen1")) || !exists(hashesFile) {
		t.Errorf("after keeping a's gen2: wrong files left")
	}
	removeHaveFiles(dir, "a", "")
	if exists(haveFile("a", "gen2")) || !exists(haveFile("b", "gen1")) {
		t.Errorf("after removing a's: wrong files left")
	}
}

// interruptingTransport calls interrupt, once, after the nth request
// uploading blobs.
type interruptingTransport struct {
	http.RoundTripper
	n int

	mu        sync.Mutex
	count     int
	interrupt func()
}

func (it *interruptingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := it.RoundTripper.RoundTrip(r)
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/camli/upload") {
		it.mu.Lock()
		it.count++
		var fn func()
		if it.count == it.n {
			fn = it.interrupt
		}
		it.mu.Unlock()
		if fn != nil {
			fn()
		}
	}
	return res, err
}

// TestResumeInterruptedUpload checks that uploading a file again, after
// an upload of it was canceled partway, sends only the rest of it.
func TestResumeInterruptedUpload(t *testing.T) {
	it := &interruptingTransport{n: 20}
	newClient := newTestPerkeep(t, func(rt http.RoundTripper) http.RoundTripper {
		it.mu.Lock()
		defer it.mu.Unlock()
		it.RoundTripper = rt // the same for each client
		return it
	})
	s := newTestServer(t, t.TempDir())
	s.testClient = newClient
	var a Account
	do(t, s, "POST", "/accounts", accountFields{Server: "example.com"}, http.StatusCreated, &a)
	path := "/accounts/" + a.ID

	const size = 24 << 20
	movie := writeRandomFile(t, t.TempDir(), "movie.mov", size)
	upload := func() uploadsStatus {
		t.Helper()
		do(t, s, "POST", path+"/connect", map[string]string{}, http.StatusOK, nil) // new totals
		do(t, s, "POST", path+"/uploads", map[string]any{"key": "movie", "path": movie, "name": "IMG_0001.MOV"}, http.StatusOK, nil)
		ac, err := s.conn(a.ID)
		if err != nil {
			t.Fatal(err)
		}
		ac.running.Wait()
		var st uploadsStatus
		do(t, s, "GET", path+"/uploads", nil, http.StatusOK, &st)
		if len(st.Done) != 1 {
			t.Fatalf("done = %+v", st.Done)
		}
		return st
	}

	it.mu.Lock()
	it.interrupt = func() { s.cancelUploads(a.ID) }
	it.mu.Unlock()
	first := upload()
	if first.Done[0].Error == "" {
		t.Fatalf("first upload wasn't interrupted: %+v", first)
	}
	it.mu.Lock()
	it.interrupt = nil
	it.mu.Unlock()
	second := upload()
	if second.Done[0].Error != "" {
		t.Fatalf("second upload: %v", second.Done[0].Error)
	}
	t.Logf("first:  %+v", first.Totals)
	t.Logf("second: %+v", second.Totals)
	if second.Totals.ChunksPresent == 0 || second.Totals.BytesUploaded+first.Totals.BytesUploaded > size+size/20 {
		t.Errorf("second upload sent %d bytes, after the first sent %d of %d, with %d of %d chunks already there; want it to skip the first's chunks",
			second.Totals.BytesUploaded, first.Totals.BytesUploaded, size, second.Totals.ChunksPresent, second.Totals.Chunks)
	}
}
