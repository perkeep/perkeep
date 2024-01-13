/*
Copyright 2018 The Perkeep Authors.

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

package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"perkeep.org/internal/httputil"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/index"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/types/camtypes"

	"go4.org/types"
)

// numWorkers is the number of "long-lived" goroutines that run during the
// importing process. In addition, a goroutine is created for each directory
// visited (but terminates quickly).
const numWorkers = 10

// shareImporter imports shared blobs from src into dest.
// a shareImporter is not meant to be ephemeral, but it only handles one
// importing at a time.
type shareImporter struct {
	shareURL string
	src      *client.Client
	dest     blobserver.Storage

	mu        sync.RWMutex
	seen      int      // files seen. for statistics, as web UI feedback.
	copied    int      // files actually copied. for statistics, as web UI feedback.
	running   bool     // whether an importing is currently going on.
	assembled bool     // whether the shared blob is of an assembled file.
	br        blob.Ref // the resulting imported file or directory schema.
	err       error    // any error encountered by one of the importing workers.

	wg    sync.WaitGroup // to wait for all the work assigners, spawned for each directory, to finish.
	workc chan work      // workers read from it in order to do the importing work.
}

// work is the unit of work to be done, sent on workc to one of the workers
type work struct {
	br   blob.Ref     // blob to import
	errc chan<- error // chan on which the worker sends back any error (or nil)
}

// imprt fetches and copies br and recurses on the contents or children of br.
func (si *shareImporter) imprt(ctx context.Context, br blob.Ref) error {
	// A little less than the sniffer will take, so we don't truncate.
	const sniffSize = 900 * 1024
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	src := si.src
	dest := si.dest
	rc, _, err := src.Fetch(ctx, br)
	if err != nil {
		return err
	}
	rcc := types.NewOnceCloser(rc)
	defer rcc.Close()

	sniffer := index.NewBlobSniffer(br)
	_, err = io.CopyN(sniffer, rc, sniffSize)
	if err != nil && err != io.EOF {
		return err
	}
	sniffer.Parse()
	b, ok := sniffer.SchemaBlob()
	if !ok {
		return fmt.Errorf("%q: not a Perkeep schema.", br)
	}
	body, err := sniffer.Body()
	if err != nil {
		return err
	}
	rc = io.NopCloser(io.MultiReader(bytes.NewReader(body), rc))

	switch b.Type() {
	case "directory":
		if _, err := blobserver.Receive(ctx, dest, br, rc); err != nil {
			return err
		}
		ssbr, ok := b.DirectoryEntries()
		if !ok {
			return fmt.Errorf("%q not actually a directory", br)
		}
		rcc.Close()
		return si.imprt(ctx, ssbr)
	case "static-set":
		if _, err := blobserver.Receive(ctx, dest, br, rc); err != nil {
			return err
		}
		rcc.Close()
		si.wg.Add(1)
		// asynchronous work assignment through w.workc
		go func() {
			defer si.wg.Done()
			si.mu.RLock()
			// do not pile in more work to do if there's already been an error
			if si.err != nil {
				si.mu.RUnlock()
				return
			}
			si.mu.RUnlock()
			var errcs []<-chan error
			for _, mref := range b.StaticSetMembers() {
				errc := make(chan error, 1)
				errcs = append(errcs, errc)
				si.workc <- work{mref, errc}
			}
			for _, errc := range errcs {
				if err := <-errc; err != nil {
					si.mu.Lock()
					si.err = err
					si.mu.Unlock()
				}
			}
		}()
		return nil
	case "file":
		rcc.Close()
		fr, err := schema.NewFileReader(ctx, src, br)
		if err != nil {
			return fmt.Errorf("NewFileReader: %v", err)
		}
		defer fr.Close()
		si.mu.Lock()
		si.seen++
		si.mu.Unlock()
		if _, err := schema.WriteFileMap(ctx, dest, b.Builder(), fr); err != nil {
			return err
		}
		si.mu.Lock()
		si.copied++
		si.mu.Unlock()
		return nil
	// TODO(mpl): other camliTypes, at least symlink.
	default:
		return errors.New("unknown blob type: " + string(b.Type()))
	}
}

// importAll imports all the shared contents transitively reachable under
// si.shareURL.
func (si *shareImporter) importAll(ctx context.Context) error {
	src, shared, err := client.NewFromShareRoot(ctx, si.shareURL, client.OptionNoExternalConfig())
	if err != nil {
		return err
	}
	si.src = src
	si.br = shared
	si.workc = make(chan work, 2*numWorkers)
	defer close(si.workc)

	// fan out over a pool of numWorkers workers overall
	for i := 0; i < numWorkers; i++ {
		go func() {
			for wi := range si.workc {
				wi.errc <- si.imprt(ctx, wi.br)
			}
		}()
	}
	// work assignment is done asynchronously, so imprt returns before all the work is finished.
	err = si.imprt(ctx, shared)
	si.wg.Wait()
	if err == nil {
		si.mu.RLock()
		err = si.err
		si.mu.RUnlock()
	}
	log.Print("share importer: all workers done")
	if err != nil {
		return err
	}
	return nil
}

// importAssembled imports the assembled file shared at si.shareURL.
func (si *shareImporter) importAssembled(ctx context.Context) {
	res, err := http.Get(si.shareURL)
	if err != nil {
		return
	}
	defer res.Body.Close()
	br, err := schema.WriteFileFromReader(ctx, si.dest, "", res.Body)
	if err != nil {
		return
	}
	si.mu.Lock()
	si.br = br
	si.mu.Unlock()
}

// isAssembled reports whether si.shareURL is of a shared assembled file.
func (si *shareImporter) isAssembled() (bool, error) {
	u, err := url.Parse(si.shareURL)
	if err != nil {
		return false, err
	}
	isAs, _ := strconv.ParseBool(u.Query().Get("assemble"))
	return isAs, nil
}

// ServeHTTP answers the following queries:
//
// POST:
//
//	?shareurl=https://yourfriendserver/share/sha224-shareclaim
//
// Imports all the contents transitively reachable under that shared URL.
//
// GET:
// Serves as JSON the state of the currently running import process, encoded
// from a camtypes.ShareImportProgress.
//
// If an import is already running, POST requests are served a 503.
func (si *shareImporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if si.dest == nil {
		http.Error(w, "shareImporter without a dest", 500)
		return
	}
	if r.Method == "GET" {
		si.serveProgress(w, r)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Not a POST", http.StatusBadRequest)
		return
	}
	si.mu.Lock()
	if si.running {
		http.Error(w, "an import is already in progress", http.StatusServiceUnavailable)
		si.mu.Unlock()
		return
	}
	si.running = true
	si.seen = 0
	si.copied = 0
	si.assembled = false
	si.br = blob.Ref{}
	si.mu.Unlock()

	shareURL := r.FormValue("shareurl")
	if shareURL == "" {
		http.Error(w, "No shareurl parameter", http.StatusBadRequest)
		return
	}
	si.shareURL = shareURL
	isAs, err := si.isAssembled()
	if err != nil {
		http.Error(w, "Could not parse shareurl", http.StatusInternalServerError)
		return
	}

	go func() {
		defer func() {
			si.mu.Lock()
			si.running = false
			si.mu.Unlock()
		}()
		if isAs {
			si.mu.Lock()
			si.assembled = true
			si.mu.Unlock()
			si.importAssembled(context.Background())
			return
		}
		si.importAll(context.Background())
	}()
	w.WriteHeader(200)
}

// serveProgress serves the state of the currently running importing process
func (si *shareImporter) serveProgress(w http.ResponseWriter, r *http.Request) {
	si.mu.RLock()
	defer si.mu.RUnlock()
	httputil.ReturnJSON(w, camtypes.ShareImportProgress{
		FilesSeen:   si.seen,
		FilesCopied: si.copied,
		Running:     si.running,
		Assembled:   si.assembled,
		BlobRef:     si.br,
	})
}
