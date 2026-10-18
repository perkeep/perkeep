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

package upload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"perkeep.org/internal/osutil"
	"perkeep.org/pkg/auth"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/serverinit"
	"perkeep.org/pkg/types/serverconfig"

	// The storage types and handlers of the default config.
	_ "perkeep.org/pkg/blobserver/cond"
	_ "perkeep.org/pkg/blobserver/replica"
	_ "perkeep.org/pkg/importer"
	_ "perkeep.org/pkg/search"
	_ "perkeep.org/pkg/server"
)

// flushCounter is an http.RoundTripper counting upload session flushes.
type flushCounter struct {
	http.RoundTripper
	flushes atomic.Int32
}

func (fc *flushCounter) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/flush") {
		fc.flushes.Add(1)
	}
	return fc.RoundTripper.RoundTrip(req)
}

// newTestClient returns a client of a new in-memory server with the
// default config: storage, index, search, and upload sessions.
func newTestClient(t *testing.T) *client.Client {
	t.Helper()
	c, _ := newCountingClient(t)
	return c
}

// newCountingClient is like newTestClient, also returning a count of the
// client's flushes.
func newCountingClient(t *testing.T) (*client.Client, *flushCounter) {
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
	c, err := client.New(client.OptionServer("http://example.com"), client.OptionNoExternalConfig(),
		client.OptionAuthMode(auth.NewBasicAuth("foo", "bar")))
	if err != nil {
		t.Fatal(err)
	}
	hc := ts.Client()
	fc := &flushCounter{RoundTripper: hc.Transport}
	hc.Transport = fc
	c.SetHTTPClient(hc)
	return c, fc
}

// fileInfo is a regular file's fs.FileInfo.
type fileInfo struct {
	name    string
	size    int64
	modTime time.Time
}

func (fi fileInfo) Name() string       { return fi.name }
func (fi fileInfo) Size() int64        { return fi.size }
func (fi fileInfo) Mode() fs.FileMode  { return 0o644 }
func (fi fileInfo) ModTime() time.Time { return fi.modTime }
func (fi fileInfo) IsDir() bool        { return false }
func (fi fileInfo) Sys() any           { return nil }

var testModTime = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// randBytes returns n random bytes, the same for the same seed.
func randBytes(seed uint64, n int) []byte {
	b := make([]byte, n)
	var s [32]byte
	s[0] = byte(seed)
	rand.NewChaCha8(s).Read(b)
	return b
}

// uploadFile uploads contents as the file name, checking that it can be
// read back.
func uploadFile(t *testing.T, c *client.Client, name string, contents []byte, opts *Options) (*Result, Progress) {
	t.Helper()
	f := NewFile(c, fileInfo{name, int64(len(contents)), testModTime}, bytes.NewReader(contents), opts)
	res, err := f.Run(context.Background())
	if err != nil {
		t.Fatalf("uploading %s: %v", name, err)
	}
	if res.Size != int64(len(contents)) {
		t.Errorf("Result.Size = %d; want %d", res.Size, len(contents))
	}
	if res.Durable {
		checkContents(t, c, res.FileRef, contents)
	}
	p := f.Progress()
	if p.Phase != PhaseDone {
		t.Errorf("Progress.Phase after Run = %v; want %v", p.Phase, PhaseDone)
	}
	return res, p
}

// checkContents checks that the file of schema blob fileRef has contents.
func checkContents(t *testing.T, fetcher blob.Fetcher, fileRef blob.Ref, contents []byte) {
	t.Helper()
	fr, err := schema.NewFileReader(context.Background(), fetcher, fileRef)
	if err != nil {
		t.Fatalf("reading file %v: %v", fileRef, err)
	}
	defer fr.Close()
	got, err := io.ReadAll(fr)
	if err != nil {
		t.Fatalf("reading file %v: %v", fileRef, err)
	}
	if !bytes.Equal(got, contents) {
		t.Errorf("file %v has %d bytes; want the %d uploaded", fileRef, len(got), len(contents))
	}
}

func TestUploadSmall(t *testing.T) {
	c, fc := newCountingClient(t)
	res, p := uploadFile(t, c, "small.txt", []byte("hello"), nil)
	if !res.Durable || res.Dup || len(res.WholeRefs) != 0 {
		t.Errorf("Result = %+v; want durable, not a dup, no whole-file hashing", res)
	}
	if p.ChunksUploaded != 1 || p.BytesHashed != 0 {
		t.Errorf("Progress = %+v; want 1 chunk uploaded, nothing hashed", p)
	}
	// One flush for the chunk to be durable before the file schema blob is
	// uploaded, and one for the file schema blob.
	if n := fc.flushes.Load(); n != 2 {
		t.Errorf("%d flushes; want 2", n)
	}
}

// TestUploadRepeatedChunks checks that a chunk repeated within a file is
// uploaded once.
func TestUploadRepeatedChunks(t *testing.T) {
	c := newTestClient(t)
	// Cut into the first chunk, five of the maximum size, and the rest.
	_, p := uploadFile(t, c, "zeros", make([]byte, 5<<20+7), nil)
	if p.Chunks != 6 || p.ChunksUploaded != 3 {
		t.Errorf("Progress = %+v; want 6 chunks, 3 of them distinct and uploaded", p)
	}
}

func TestUploadEmpty(t *testing.T) {
	c := newTestClient(t)
	uploadFile(t, c, "empty", nil, nil)
}

// TestUploadDup checks that a file whose contents the server has gets
// only its file schema blob uploaded, and none if it's the same file.
func TestUploadDup(t *testing.T) {
	c := newTestClient(t)
	contents := randBytes(1, 3<<20)
	first, p := uploadFile(t, c, "a.bin", contents, nil)
	if first.Dup || p.BytesHashed != int64(len(contents)) || p.ChunksUploaded == 0 {
		t.Fatalf("first upload: %+v, %+v; want a new file, hashed and uploaded", first, p)
	}
	if want := blob.RefFromBytes(contents); !slices.Contains(first.WholeRefs, want) {
		t.Errorf("WholeRefs = %v; want them to have %v", first.WholeRefs, want)
	}

	other, p := uploadFile(t, c, "b.bin", contents, nil)
	if !other.Dup || other.FileRef == first.FileRef || p.ChunksUploaded != 0 || p.Chunks != 0 {
		t.Errorf("same contents, other name: %+v, %+v; want a dup with a new file schema, and no chunks", other, p)
	}
	again, _ := uploadFile(t, c, "a.bin", contents, nil)
	if !again.Dup || again.FileRef != first.FileRef {
		t.Errorf("same file again: %+v; want a dup of %v", again, first.FileRef)
	}
}

// TestUploadSharedChunks checks that chunks the server has aren't
// uploaded again, as when resuming an interrupted upload.
func TestUploadSharedChunks(t *testing.T) {
	c := newTestClient(t)
	contents := randBytes(2, 3<<20)
	uploadFile(t, c, "a.bin", contents, nil)
	more := append(slices.Clone(contents[:2<<20]), randBytes(3, 1<<20)...)
	res, p := uploadFile(t, c, "b.bin", more, nil)
	if res.Dup || p.ChunksPresent == 0 || p.ChunksUploaded == 0 || p.ChunksPresent+p.ChunksUploaded > p.Chunks {
		t.Errorf("upload sharing chunks: %+v, %+v; want some chunks present and some uploaded", res, p)
	}
}

// TestUploadCallerSession checks that a File uploading into the caller's
// session leaves the caller's Flush to report its file schema durable.
func TestUploadCallerSession(t *testing.T) {
	c, fc := newCountingClient(t)
	s, err := c.NewUploadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	contents := randBytes(4, 1<<20)
	res, _ := uploadFile(t, c, "a.bin", contents, &Options{Session: s})
	if res.Durable {
		t.Errorf("Result.Durable with the caller's session; want false")
	}
	// The chunks were made durable before the file schema blob was
	// uploaded, which the caller's Flush is left to make durable.
	if n := fc.flushes.Load(); n != 1 {
		t.Errorf("%d flushes during Run; want 1", n)
	}
	durable, err := s.Flush(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(durable, func(sb blob.SizedRef) bool { return sb.Ref == res.FileRef }) {
		t.Errorf("Flush didn't report file schema %v durable", res.FileRef)
	}
	checkContents(t, c, res.FileRef, contents)
}

// TestUploadLocalStorage checks uploading with a client of local storage,
// which has no upload sessions or search.
func TestUploadLocalStorage(t *testing.T) {
	sto := new(memory.Storage)
	c, err := client.New(client.OptionUseStorageClient(sto))
	if err != nil {
		t.Fatal(err)
	}
	contents := randBytes(5, 1<<20)
	res, _ := uploadFile(t, c, "a.bin", contents, nil)
	if !res.Durable || res.Dup {
		t.Errorf("Result = %+v; want durable, not a dup", res)
	}
	checkContents(t, sto, res.FileRef, contents)
}

// TestUploadSharedLimiter checks that Files sharing a small Limiter all
// finish, and give back all of its budget, whether they succeed or not.
func TestUploadSharedLimiter(t *testing.T) {
	c := newTestClient(t)
	lim := NewLimiter(Limits{HashCores: 1, BytesInFlight: 1})
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			contents := randBytes(uint64(10+i), 8<<20)
			uploadFile(t, c, "big.bin", contents, &Options{Limiter: lim})
		})
	}
	wg.Wait()

	// An upload canceled while cutting, with chunks batched and in flight.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	contents := randBytes(20, 24<<20)
	r := &cancelingReader{ReaderAt: bytes.NewReader(contents), at: 20 << 20, cancel: cancel}
	f := NewFile(c, fileInfo{"canceled.bin", int64(len(contents)), testModTime}, r, &Options{
		Limiter:   lim,
		WholeRefs: []blob.Ref{blob.RefFromBytes(contents)}, // not read twice
	})
	if _, err := f.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Run = %v; want %v", err, context.Canceled)
	}
	if p := f.Progress(); p.ChunksUploaded == 0 {
		t.Errorf("canceled Run uploaded no chunks; want it canceled mid-upload")
	}
	if !lim.cores.TryAcquire(int64(lim.limits.HashCores)) || !lim.bytes.TryAcquire(lim.limits.BytesInFlight) {
		t.Error("Limiter's budget not all given back")
	}
}

// cancelingReader is an io.ReaderAt calling cancel once it's read at.
type cancelingReader struct {
	io.ReaderAt
	at     int64
	cancel func()
}

func (r *cancelingReader) ReadAt(p []byte, off int64) (int, error) {
	if off >= r.at {
		r.cancel()
	}
	return r.ReaderAt.ReadAt(p, off)
}
