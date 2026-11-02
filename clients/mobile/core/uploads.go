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

// Uploading media from the device to an account's server.
//
// The native app finds the media and, as needed, exports each item to a
// temporary file. The core hashes the files, asks the server which it
// has, and uploads the others with pkg/upload, keeping caches of the
// hashes and of what the server has, and the uploads' progress for the
// app to poll:
//
//	POST   /accounts/{id}/connect       {password, tailcat}: connect to the server
//	POST   /accounts/{id}/hashes/lookup {keys}: cached whole-file refs
//	POST   /accounts/{id}/hashes/file   {key, path}: hash a file
//	POST   /accounts/{id}/findmissing   {items: [{key, refs}]}: which the server lacks
//	POST   /accounts/{id}/uploads       {key, path, name, modTime, refs, noDupCheck}: start uploading a file
//	GET    /accounts/{id}/uploads       progress, and uploads finished since the last GET
//	DELETE /accounts/{id}/uploads       cancel the uploads in progress
//
// The item keys are the app's: strings that change whenever an item's
// contents might.

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/upload"
)

// hashesFile is the cache of items' whole-file refs, by item key: the
// refs of each hash function computed so far.
const hashesFile = "hashes.jsonl"

// haveFile returns the name of the cache of whole-file refs whose
// contents the account's server has a permanode of (see permanodes.go),
// in its storage generation gen (see
// client.Client.StorageGeneration). A server whose storage is reset or
// partly lost gets a new generation, and so a new, empty cache. Servers
// without generations use the generation "none".
func haveFile(accountID, gen string) string {
	return haveFilePrefix(accountID) + url.PathEscape(gen) + ".jsonl"
}

func haveFilePrefix(accountID string) string { return "permanodes-" + accountID + "-" }

// removeHaveFiles removes the account's caches of what its server has,
// except for generation keepGen, if non-empty.
func removeHaveFiles(dir, accountID, keepGen string) {
	// The "have-" caches were of whole-file refs the server had any file
	// of, permanode or not.
	old, _ := filepath.Glob(filepath.Join(dir, "have-"+accountID+"-*.jsonl"))
	for _, m := range old {
		os.Remove(m)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, haveFilePrefix(accountID)+"*.jsonl"))
	for _, m := range matches {
		if keepGen == "" || filepath.Base(m) != haveFile(accountID, keepGen) {
			os.Remove(m)
		}
	}
}

// accountConn is a connection to an account's server, for uploading.
type accountConn struct {
	c         *client.Client
	base      string             // the server's base URL, for c
	hashNames []string           // of the whole-file refs the server finds files by
	newHashes []func() hash.Hash // for hashNames
	have      *logCache[bool]    // whole-file refs whose contents the server has a permanode of
	// permanodeMu has a *sync.Mutex per whole-file ref, held while
	// making the permanode of its contents.
	permanodeMu sync.Map
	lim         *upload.Limiter

	ctx     context.Context // canceled by cancel or close
	cancel  context.CancelFunc
	running sync.WaitGroup // of the upload goroutines

	mu      sync.Mutex
	nextID  int
	uploads map[int]*fileUpload // in progress
	done    []uploadDone        // since the last poll
	totals  uploadTotals        // of finished uploads, since connecting
}

// fileUpload is an upload in progress.
type fileUpload struct {
	id     int
	key    string
	name   string
	size   int64
	start  time.Time
	f      *upload.File
	cancel context.CancelFunc
}

type uploadDone struct {
	ID            int    `json:"id"`
	Key           string `json:"key"`
	FileRef       string `json:"fileRef,omitempty"`
	Dup           bool   `json:"dup"` // the server had the contents
	BytesUploaded int64  `json:"bytesUploaded"`
	Error         string `json:"error,omitempty"`
}

type uploadTotals struct {
	FilesDone      int   `json:"filesDone"` // uploaded, or found on the server
	FilesDup       int   `json:"filesDup"`  // of FilesDone, found on the server
	FilesFailed    int   `json:"filesFailed"`
	BytesUploaded  int64 `json:"bytesUploaded"`
	Chunks         int   `json:"chunks"`
	ChunksPresent  int   `json:"chunksPresent"`
	ChunksUploaded int   `json:"chunksUploaded"`
}

func (t *uploadTotals) add(p upload.Progress) {
	t.BytesUploaded += p.BytesUploaded
	t.Chunks += p.Chunks
	t.ChunksPresent += p.ChunksPresent
	t.ChunksUploaded += p.ChunksUploaded
}

// close cancels the connection's uploads and closes its cache.
func (ac *accountConn) close() {
	ac.cancel()
	ac.have.close()
}

// lookupAccount returns a copy of the account with the given ID.
func (s *server) lookupAccount(id string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accounts == nil {
		return Account{}, errorf(http.StatusServiceUnavailable, "core not initialized")
	}
	a := s.accounts.find(id)
	if a == nil {
		return Account{}, errorf(http.StatusNotFound, "no such account")
	}
	return *a, nil
}

// newPerkeepClient returns a Perkeep client for the account's server.
// It also returns the base URL that c uses.
func (s *server) newPerkeepClient(a Account, password, tailcatAddr string) (c *client.Client, base string, err error) {
	if s.testClient != nil {
		return s.testClient(a)
	}
	cn := conn{Server: a.Server, CertFP: a.CertFP, Username: a.Username, Password: password}
	if a.Tailcat {
		if tailcatAddr == "" {
			return nil, "", errors.New("account has no tailcat address")
		}
		cn = conn{Tailcat: tailcatAddr, Password: password}
	}
	hc, base, err := s.httpClient(cn)
	if err != nil {
		return nil, "", err
	}
	am := auth.AuthMode(auth.None{})
	if password != "" {
		user := cn.Username
		if user == "" {
			user = "perkeep" // access keys accept any username
		}
		am = auth.NewBasicAuth(user, password)
	}
	c, err = client.New(client.OptionServer(base), client.OptionNoExternalConfig(), client.OptionAuthMode(am))
	if err != nil {
		return nil, "", err
	}
	if hc != http.DefaultClient {
		c.SetHTTPClient(hc)
	}
	return c, base, nil
}

// conn returns the account's connection, made by POST connect.
func (s *server) conn(id string) (*accountConn, error) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	ac := s.conns[id]
	if ac == nil {
		return nil, errorf(http.StatusConflict, "not connected to the account's server")
	}
	return ac, nil
}

// dropConn closes and forgets the account's connection, if any.
func (s *server) dropConn(id string) {
	s.connMu.Lock()
	ac := s.conns[id]
	delete(s.conns, id)
	s.connMu.Unlock()
	if ac != nil {
		ac.close()
	}
}

// uploadHandler adapts a handler of an account's uploads.
func (s *server) uploadHandler(fn func(w http.ResponseWriter, r *http.Request, a Account) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := s.lookupAccount(r.PathValue("id"))
		if err == nil {
			err = fn(w, r, a)
		}
		if err != nil {
			writeError(w, err)
		}
	}
}

// connHandler is like uploadHandler for handlers that need the
// account's connection.
func (s *server) connHandler(fn func(w http.ResponseWriter, r *http.Request, ac *accountConn) error) http.HandlerFunc {
	return s.uploadHandler(func(w http.ResponseWriter, r *http.Request, a Account) error {
		ac, err := s.conn(a.ID)
		if err != nil {
			return err
		}
		return fn(w, r, ac)
	})
}

func (s *server) serveConnect(w http.ResponseWriter, r *http.Request, a Account) error {
	var req struct {
		Password string `json:"password"`
		Tailcat  string `json:"tailcat"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	c, base, err := s.newPerkeepClient(a, req.Password, req.Tailcat)
	if err != nil {
		return errorf(http.StatusBadRequest, err.Error())
	}
	newHashes, err := c.WholeRefHashes() // does discovery
	if err != nil {
		return errorf(http.StatusBadGateway, "couldn't get the server's configuration: "+err.Error())
	}
	var names []string
	for _, nh := range newHashes {
		names = append(names, blob.RefFromHash(nh()).HashName())
	}
	gen, err := c.StorageGeneration()
	if errors.Is(err, client.ErrNoStorageGeneration) {
		gen = "none"
	} else if err != nil {
		return errorf(http.StatusBadGateway, err.Error())
	}
	removeHaveFiles(s.dataPath(""), a.ID, gen)
	have, err := openLogCache[bool](s.dataPath(haveFile(a.ID, gen)))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	ac := &accountConn{
		c:         c,
		base:      base,
		hashNames: names,
		newHashes: newHashes,
		have:      have,
		lim:       upload.NewLimiter(upload.Limits{}),
		ctx:       ctx,
		cancel:    cancel,
		uploads:   map[int]*fileUpload{},
	}
	s.dropConn(a.ID)
	s.connMu.Lock()
	if s.conns == nil {
		s.conns = map[string]*accountConn{}
	}
	s.conns[a.ID] = ac
	s.connMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"hashes": names, "generation": gen})
	return nil
}

// dataPath returns the path of the named file in the data directory.
func (s *server) dataPath(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return filepath.Join(s.dataDir, name)
}

// cachedRefs returns the item's cached whole-file refs of each of ac's
// hashes, or nil if any aren't cached.
func (s *server) cachedRefs(ac *accountConn, key string) []string {
	got, _ := s.hashes.get(key)
	var refs []string
	for _, name := range ac.hashNames {
		found := false
		for _, ref := range got {
			if br, ok := blob.Parse(ref); ok && br.HashName() == name {
				refs = append(refs, ref)
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	return refs
}

func (s *server) serveHashLookup(w http.ResponseWriter, r *http.Request, ac *accountConn) error {
	var req struct {
		Keys []string `json:"keys"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	res := map[string][]string{}
	for _, k := range req.Keys {
		if refs := s.cachedRefs(ac, k); refs != nil {
			res[k] = refs
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"refs": res})
	return nil
}

func (s *server) serveHashFile(w http.ResponseWriter, r *http.Request, ac *accountConn) error {
	var req struct {
		Key  string `json:"key"`
		Path string `json:"path"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	refs, err := hashFile(r.Context(), req.Path, ac.newHashes)
	if err != nil {
		return err
	}
	// Keep cached refs of other hash functions, as for another server.
	merged := slices.Clone(refs)
	old, _ := s.hashes.get(req.Key)
	for _, ref := range old {
		if !slices.ContainsFunc(merged, func(r string) bool { return sameHash(r, ref) }) {
			merged = append(merged, ref)
		}
	}
	if err := s.hashes.put(req.Key, merged); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"refs": refs})
	return nil
}

// sameHash reports whether refs a and b are of the same hash function.
func sameHash(a, b string) bool {
	ba, ok1 := blob.Parse(a)
	bb, ok2 := blob.Parse(b)
	return ok1 && ok2 && ba.HashName() == bb.HashName()
}

// hashFile returns the whole-file refs of the file at path, of each of
// newHashes, reading the file once.
func hashFile(ctx context.Context, path string, newHashes []func() hash.Hash) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hashes := make([]hash.Hash, len(newHashes))
	for i, nh := range newHashes {
		hashes[i] = nh()
	}
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := io.ReadFull(f, buf)
		if n > 0 {
			var wg sync.WaitGroup
			for _, h := range hashes {
				wg.Go(func() { h.Write(buf[:n]) })
			}
			wg.Wait()
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	refs := make([]string, len(hashes))
	for i, h := range hashes {
		refs[i] = blob.RefFromHash(h).String()
	}
	return refs, nil
}

// serveFindMissing reports which items the server has, by their
// whole-file refs: those whose contents have a permanode. It asks the
// server about those not known to be there. If asking fails, it reports
// the error, and the items not yet asked about as missing.
func (s *server) serveFindMissing(w http.ResponseWriter, r *http.Request, ac *accountConn) error {
	var req struct {
		Items []struct {
			Key  string   `json:"key"`
			Refs []string `json:"refs"`
		} `json:"items"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	present := map[string]bool{} // by key
	var ask []string             // refs
	for _, it := range req.Items {
		if slices.ContainsFunc(it.Refs, func(ref string) bool { have, _ := ac.have.get(ref); return have }) {
			present[it.Key] = true
		} else {
			ask = append(ask, it.Refs...)
		}
	}

	// Ask a few batches at once.
	var mu sync.Mutex
	have := map[string]bool{}
	g, ctx := errgroup.WithContext(r.Context())
	g.SetLimit(4)
	for i := 0; i < len(ask); i += contentPermanodesBatch {
		batch := ask[i:min(i+contentPermanodesBatch, len(ask))]
		g.Go(func() error {
			got, err := ac.contentPermanodes(ctx, batch)
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			for ref := range got {
				have[ref] = true
			}
			return nil
		})
	}
	askErr := g.Wait()
	for _, it := range req.Items {
		if !present[it.Key] && slices.ContainsFunc(it.Refs, func(ref string) bool { return have[ref] }) {
			present[it.Key] = true
			for _, ref := range it.Refs {
				ac.have.put(ref, true)
			}
		}
	}

	res := struct {
		Present []string `json:"present"`
		Missing []string `json:"missing"`
		Error   string   `json:"error,omitempty"`
	}{Present: []string{}, Missing: []string{}}
	for _, it := range req.Items {
		if present[it.Key] {
			res.Present = append(res.Present, it.Key)
		} else {
			res.Missing = append(res.Missing, it.Key)
		}
	}
	if askErr != nil {
		res.Error = fmt.Sprintf("Couldn't ask the server which files it has: %v", askErr)
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

// namedFileInfo is a FileInfo with another name, for temporary files
// exported from the photo library.
type namedFileInfo struct {
	fs.FileInfo
	name string
}

func (fi namedFileInfo) Name() string { return fi.name }

func (s *server) serveStartUpload(w http.ResponseWriter, r *http.Request, ac *accountConn) error {
	var req struct {
		Key     string    `json:"key"`
		Path    string    `json:"path"`
		Name    string    `json:"name"`
		ModTime time.Time `json:"modTime"`
		Refs    []string  `json:"refs"`
		// NoDupCheck says the app already asked the server about the
		// file, or found it can't ask, so the upload needn't.
		NoDupCheck bool `json:"noDupCheck"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	var refs []blob.Ref
	for _, s := range req.Refs {
		br, ok := blob.Parse(s)
		if !ok {
			return errorf(http.StatusBadRequest, "bad ref "+s)
		}
		refs = append(refs, br)
	}
	f, err := os.Open(req.Path)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if req.Name != "" {
		fi = namedFileInfo{fi, req.Name}
	}
	uf := upload.NewFile(ac.c, fi, f, &upload.Options{
		Limiter:    ac.lim,
		ModTime:    req.ModTime,
		CapCtime:   true,
		WholeRefs:  refs,
		NoDupCheck: req.NoDupCheck,
	})

	ctx, cancel := context.WithCancel(ac.ctx)
	ac.mu.Lock()
	ac.nextID++
	u := &fileUpload{id: ac.nextID, key: req.Key, name: fi.Name(), size: fi.Size(), start: time.Now(), f: uf, cancel: cancel}
	ac.uploads[u.id] = u
	ac.mu.Unlock()

	ac.running.Go(func() {
		defer cancel()
		res, err := uf.Run(ctx)
		f.Close()
		if err == nil {
			// The file's no use in the web UI without a permanode.
			if perr := ac.ensurePermanode(ctx, res.WholeRefs, res.FileRef, req.ModTime); perr != nil {
				err = fmt.Errorf("uploaded the file, but couldn't make its permanode: %w", perr)
			}
		}
		p := uf.Progress()
		d := uploadDone{ID: u.id, Key: u.key, BytesUploaded: p.BytesUploaded}
		if err != nil {
			d.Error = err.Error()
		} else {
			d.FileRef = res.FileRef.String()
			d.Dup = res.Dup
		}
		ac.mu.Lock()
		defer ac.mu.Unlock()
		delete(ac.uploads, u.id)
		ac.done = append(ac.done, d)
		ac.totals.add(p)
		switch {
		case err != nil:
			ac.totals.FilesFailed++
		case res.Dup:
			ac.totals.FilesDone++
			ac.totals.FilesDup++
		default:
			ac.totals.FilesDone++
		}
	})
	writeJSON(w, http.StatusOK, map[string]int{"id": u.id})
	return nil
}

// serveUploads reports the uploads' progress, and those finished since
// the last call.
func (s *server) serveUploads(w http.ResponseWriter, r *http.Request, ac *accountConn) error {
	type inFlight struct {
		ID    int    `json:"id"`
		Key   string `json:"key"`
		Name  string `json:"name"`
		Size  int64  `json:"size"`
		Phase string `json:"phase"`
		upload.Progress
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	res := struct {
		InFlight []inFlight   `json:"inFlight"`
		Done     []uploadDone `json:"done"`
		Totals   uploadTotals `json:"totals"` // including those in flight
		Hashes   []string     `json:"hashes"`
	}{InFlight: []inFlight{}, Done: ac.done, Totals: ac.totals, Hashes: ac.hashNames}
	if res.Done == nil {
		res.Done = []uploadDone{}
	}
	ac.done = nil
	var ups []*fileUpload
	for _, u := range ac.uploads {
		ups = append(ups, u)
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].id < ups[j].id })
	for _, u := range ups {
		p := u.f.Progress()
		res.Totals.add(p)
		res.InFlight = append(res.InFlight, inFlight{u.id, u.key, u.name, u.size, p.Phase.String(), p})
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *server) serveCancelUploads(w http.ResponseWriter, r *http.Request, ac *accountConn) error {
	ac.cancelUploads()
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// cancelUploads cancels the uploads in progress.
func (ac *accountConn) cancelUploads() {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	for _, u := range ac.uploads {
		u.cancel()
	}
}

// cancelUploads cancels the account's uploads in progress, if any.
func (s *server) cancelUploads(accountID string) {
	if ac, err := s.conn(accountID); err == nil {
		ac.cancelUploads()
	}
}
