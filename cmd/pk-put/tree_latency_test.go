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
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go4.org/syncutil"

	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blobserver/handlers"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/blobserver/uploadsession"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/upload"
)

// latencyModel is the simulated latency of a server's requests.
type latencyModel struct {
	name       string
	perRequest time.Duration // of every request
	perUpload  time.Duration // more, of each upload request
	perDurable time.Duration // more, of each commit to durable storage: a plain upload, or a flush
}

var latencyModels = []latencyModel{
	{name: "100ms/upload+50ms/durable", perUpload: 100 * time.Millisecond, perDurable: 50 * time.Millisecond},
	{name: "100ms/request+50ms/durable", perRequest: 100 * time.Millisecond, perDurable: 50 * time.Millisecond},
}

// latencyServer is a server of blobs in memory, with upload sessions,
// whose requests take the time its latencyModel says, and which counts
// them.
type latencyServer struct {
	model latencyModel
	mux   *http.ServeMux

	mu sync.Mutex
	n  map[string]int
}

func newLatencyServer(model latencyModel) *latencyServer {
	sto := new(memory.Storage)
	sessions := uploadsession.NewManager(sto, uploadsession.Limits{})
	ls := &latencyServer{model: model, mux: http.NewServeMux(), n: make(map[string]int)}
	ls.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Write([]byte(`{"blobRoot": "/bs/", "uploadSessions": true}`))
	})
	ls.mux.Handle("/bs/camli/stat", handlers.CreateStatHandler(sto, nil))
	ls.mux.Handle("/bs/camli/upload", handlers.CreateBatchUploadHandler(sto, nil, sessions))
	ls.mux.Handle("/bs/camli/session", handlers.CreateSessionHandler(sessions))
	ls.mux.Handle("/bs/camli/session/", handlers.CreateSessionHandler(sessions))
	return ls
}

func (ls *latencyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Discovery, once per client, takes no time: the client makes it
	// holding a mutex, and goroutines waiting on a mutex keep a synctest
	// bubble's time from advancing.
	var d time.Duration
	if r.URL.Path != "/" {
		d = ls.model.perRequest
	}
	kind := "other"
	switch p := r.URL.Path; {
	case strings.HasSuffix(p, "camli/upload"):
		kind = "upload"
		d += ls.model.perUpload
		if r.URL.Query().Get("session") == "" {
			d += ls.model.perDurable
		}
	case strings.HasSuffix(p, "/flush"):
		kind = "flush"
		d += ls.model.perDurable
	case strings.HasSuffix(p, "camli/stat"):
		kind = "stat"
	}
	ls.mu.Lock()
	ls.n[kind]++
	ls.mu.Unlock()
	time.Sleep(d)
	ls.mux.ServeHTTP(w, r)
}

// newLatencyClient returns a client of a new latencyServer, which it also
// returns.
func newLatencyClient(t *testing.T, model latencyModel) (*client.Client, *latencyServer) {
	t.Helper()
	ls := newLatencyServer(model)
	ts := httptest.NewTestServer(t, ls)
	c, err := client.New(client.OptionServer("http://example.com"), client.OptionNoExternalConfig(),
		client.OptionAuthMode(auth.None{}))
	if err != nil {
		t.Fatal(err)
	}
	c.SetHTTPClient(ts.Client())
	t.Cleanup(func() { c.Close() })
	return c, ls
}

// writeTestTree writes a tree of dirs directories of files files each,
// of sizes from size/2 to size, under root.
func writeTestTree(t *testing.T, root string, dirs, files, size int) {
	t.Helper()
	rnd := rand.New(rand.NewChaCha8([32]byte{byte(dirs), byte(files), byte(size >> 10)}))
	for d := range dirs {
		dir := filepath.Join(root, fmt.Sprintf("dir%d", d))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for f := range files {
			data := make([]byte, size/2+rnd.IntN(size/2+1))
			for i := range data {
				data[i] = byte(rnd.Uint32())
			}
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%d", f)), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// TestTreeUploadSimulatedLatency compares, with simulated server latency,
// how long uploading a tree takes: as pk-put did before package upload,
// as it does with TreeUpload and package upload's File, and with
// upload.Tree. It logs the simulated times and request counts, and checks
// upload.Tree is the fastest.
//
// The client's HTTP transport here is HTTP/1, so it makes up to 5
// requests at once (with HTTP/2, 50), which bounds each way by its number
// of requests. upload.Tree makes the fewest, sending blobs together in
// its session's requests.
func TestTreeUploadSimulatedLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	type way struct {
		name                    string
		oldTree, filesViaStatRx bool
	}
	ways := []way{
		{"TreeUpload+WriteFileMap", true, true},
		{"TreeUpload+upload.File", true, false},
		{"upload.Tree", false, false},
	}
	workloads := []struct {
		name              string
		dirs, files, size int
	}{
		{"500 files of 2-4KB in 10 dirs", 10, 50, 4 << 10},
		{"40 files of 1-2MB in 4 dirs", 4, 10, 2 << 20},
	}
	for _, wl := range workloads {
		root := filepath.Join(t.TempDir(), "tree")
		writeTestTree(t, root, wl.dirs, wl.files, wl.size)
		for _, model := range latencyModels {
			var times []time.Duration
			for _, w := range ways {
				synctest.Test(t, func(t *testing.T) {
					c, ls := newLatencyClient(t, model)
					up := &Uploader{
						Client:   c,
						pwd:      root,
						fdGate:   syncutil.NewGate(100),
						limiter:  upload.NewLimiter(upload.Limits{}),
						fileOpts: &fileOptions{}, // as RunCommand sets them
					}
					testHookNoUploadTree, testHookFilesViaStatReceiver = w.oldTree, w.filesViaStatRx
					defer func() { testHookNoUploadTree, testHookFilesViaStatReceiver = false, false }()

					start := time.Now()
					var err error
					if up.canUploadTree() {
						_, err = up.uploadTree(context.Background(), root)
					} else {
						tu := up.NewTreeUpload(root)
						tu.Start()
						_, err = tu.Wait()
					}
					d := time.Since(start)
					if err != nil {
						t.Fatal(err)
					}
					times = append(times, d)
					ls.mu.Lock()
					t.Logf("%s, %s, %s: %v; requests %v", wl.name, model.name, w.name, d, ls.n)
					ls.mu.Unlock()
				})
			}
			if times[2] >= times[1] || times[2] >= times[0] {
				t.Errorf("%s, %s: times %v; want upload.Tree's, the last, the fastest", wl.name, model.name, times)
			}
		}
	}
}
