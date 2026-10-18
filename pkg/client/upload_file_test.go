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

package client

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/handlers"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/schema"
)

type uploadFileTestStorage struct{ memory.Storage }

func (*uploadFileTestStorage) Config() *blobserver.Config { return nil }

// uploadFileTestServer uses real blob protocol handlers. Only search and the
// download helper's verification response are faked.
type uploadFileTestServer struct {
	storage  uploadFileTestStorage
	hits     map[string][]blob.Ref
	verify   bool
	onSearch func()
	queries  chan []string
	heads    atomic.Int64
	uploads  atomic.Int64
}

func newUploadFileTestClient(t *testing.T) (*Client, *uploadFileTestServer) {
	t.Helper()
	s := &uploadFileTestServer{verify: true, queries: make(chan []string, 8)}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript")
		json.NewEncoder(w).Encode(map[string]string{
			"blobRoot": "/bs/", "searchRoot": "/search/", "downloadHelper": "/download/",
		})
	})
	mux.HandleFunc("/search/camli/search/files", func(w http.ResponseWriter, r *http.Request) {
		refs := r.URL.Query()["wholedigest"]
		s.queries <- refs
		if s.onSearch != nil {
			s.onSearch()
		}
		files := make(map[string][]blob.Ref)
		for _, ref := range refs {
			if hits := s.hits[ref]; len(hits) > 0 {
				files[ref] = hits
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"files": files})
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		s.heads.Add(1)
		if r.Method != "HEAD" {
			t.Errorf("verification method = %s; want HEAD", r.Method)
		}
		if s.verify {
			w.Header().Set("X-Camli-Contents", r.URL.Query().Get("verifycontents"))
		}
	})
	mux.Handle("/bs/camli/stat", handlers.CreateStatHandler(&s.storage))
	upload := handlers.CreateBatchUploadHandler(&s.storage, nil)
	mux.HandleFunc("/bs/camli/upload", func(w http.ResponseWriter, r *http.Request) {
		s.uploads.Add(1)
		upload.ServeHTTP(w, r)
	})
	mux.Handle("/bs/camli/", handlers.CreateGetHandler(&s.storage))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c, err := New(OptionServer(ts.URL), OptionNoExternalConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, s
}

func uploadFileTestRefs(r io.Reader) []blob.Ref {
	h224, h1, h256 := sha256.New224(), sha1.New(), sha256.New()
	if _, err := io.Copy(io.MultiWriter(h224, h1, h256), r); err != nil {
		panic(err)
	}
	return []blob.Ref{blob.RefFromHash(h224), blob.RefFromHash(h1), blob.RefFromHash(h256)}
}

func uploadFileTestSchema(t *testing.T, s *uploadFileTestServer, name, contents string) blob.Ref {
	t.Helper()
	ref, err := schema.WriteFileFromReader(context.Background(), &s.storage, name, strings.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func checkUploadFileTestQuery(t *testing.T, s *uploadFileTestServer, refs []blob.Ref) {
	t.Helper()
	want := make([]string, len(refs))
	for i, ref := range refs {
		want[i] = ref.String()
	}
	select {
	case got := <-s.queries:
		if !reflect.DeepEqual(got, want) {
			t.Errorf("search refs = %v; want %v", got, want)
		}
	default:
		t.Error("no whole-file search")
	}
}

type uploadFileTestSeeker struct {
	io.ReadSeeker
	readBytes int64
	onRewind  func()
}

func (r *uploadFileTestSeeker) Read(p []byte) (int, error) {
	if len(p) > 32<<10 {
		return 0, fmt.Errorf("oversized read of %d bytes", len(p))
	}
	n, err := r.ReadSeeker.Read(p)
	r.readBytes += int64(n)
	return n, err
}

func (r *uploadFileTestSeeker) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekStart && r.onRewind != nil {
		r.onRewind()
	}
	return r.ReadSeeker.Seek(offset, whence)
}

// uploadFileTestZeros generates arbitrarily large inputs without allocating
// them. It deliberately implements neither Seeker nor WriterTo.
type uploadFileTestZeros struct {
	left       int64
	readBytes  int64
	beforeRead func(int64) error
}

func (r *uploadFileTestZeros) Read(p []byte) (int, error) {
	if len(p) > 32<<10 {
		return 0, fmt.Errorf("oversized read of %d bytes", len(p))
	}
	if r.beforeRead != nil {
		if err := r.beforeRead(r.readBytes); err != nil {
			return 0, err
		}
	}
	if r.left == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), r.left)
	clear(p[:n])
	r.left -= n
	r.readBytes += n
	return int(n), nil
}

func TestUploadFileSeekableDuplicate(t *testing.T) {
	const contents = "a ZIP archive's contents"
	refs := uploadFileTestRefs(strings.NewReader(contents))
	for _, ref := range refs {
		t.Run(ref.HashName(), func(t *testing.T) {
			c, s := newUploadFileTestClient(t)
			old := uploadFileTestSchema(t, s, "old.zip", contents)
			s.hits = map[string][]blob.Ref{ref.String(): {old}}
			reader := &uploadFileTestSeeker{ReadSeeker: strings.NewReader(contents)}
			got, err := c.UploadFile(context.Background(), "renamed.zip", reader, nil)
			if err != nil {
				t.Fatal(err)
			}
			checkUploadFileTestQuery(t, s, refs)
			if reader.readBytes != int64(len(contents)) {
				t.Errorf("read %d bytes; want one pass (%d)", reader.readBytes, len(contents))
			}
			if s.heads.Load() != 1 {
				t.Errorf("verification requests = %d; want 1", s.heads.Load())
			}
			if s.uploads.Load() != 1 {
				t.Errorf("uploads = %d; want only the renamed schema", s.uploads.Load())
			}
			oldMap, err := c.FetchSchemaBlob(context.Background(), old)
			if err != nil {
				t.Fatal(err)
			}
			newMap, err := c.FetchSchemaBlob(context.Background(), got)
			if err != nil {
				t.Fatal(err)
			}
			if newMap.FileName() != "renamed.zip" || !reflect.DeepEqual(newMap.ByteParts(), oldMap.ByteParts()) {
				t.Errorf("renamed file = %s, %v; want renamed.zip, %v", newMap.FileName(), newMap.ByteParts(), oldMap.ByteParts())
			}
		})
	}
}

func TestUploadFileDuplicateUnchanged(t *testing.T) {
	c, s := newUploadFileTestClient(t)
	const contents = "same contents and filename"
	old := uploadFileTestSchema(t, s, "same.zip", contents)
	refs := uploadFileTestRefs(strings.NewReader(contents))
	s.hits = map[string][]blob.Ref{refs[0].String(): {old}}
	got, err := c.UploadFile(context.Background(), "same.zip", strings.NewReader(contents), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != old || s.uploads.Load() != 0 {
		t.Errorf("got %v, %d uploads; want %v, 0 uploads", got, s.uploads.Load(), old)
	}
}

func TestUploadFileLargeSeekableDuplicate(t *testing.T) {
	const size = 128 << 20
	c, s := newUploadFileTestClient(t)
	refs := uploadFileTestRefs(&uploadFileTestZeros{left: size})
	// A sparse local file and an already indexed file schema avoid allocating
	// either side's large content in this test.
	f, err := os.CreateTemp(t.TempDir(), "large-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	m := schema.NewFileMap("large.zip")
	if err := m.PopulateParts(size, []schema.BytesPart{{BlobRef: refs[0], Size: size}}); err != nil {
		t.Fatal(err)
	}
	old := m.Blob()
	if _, err := s.storage.ReceiveBlob(context.Background(), old.BlobRef(), strings.NewReader(old.JSON())); err != nil {
		t.Fatal(err)
	}
	s.hits = map[string][]blob.Ref{refs[2].String(): {old.BlobRef()}}
	reader := &uploadFileTestSeeker{ReadSeeker: f}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, err := c.UploadFile(context.Background(), "large.zip", reader, nil)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if got != old.BlobRef() || reader.readBytes != size || s.uploads.Load() != 0 {
		t.Errorf("got %v, read %d, uploads %d; want %v, %d, 0", got, reader.readBytes, s.uploads.Load(), old.BlobRef(), size)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
		t.Errorf("allocated %d bytes for a duplicate hit; must not buffer the %d-byte file", allocated, size)
	}
	checkUploadFileTestQuery(t, s, refs)
}

func TestUploadFileSeekableMiss(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprintf("unverifiedCandidate=%v", stale), func(t *testing.T) {
			c, s := newUploadFileTestClient(t)
			contents := strings.Repeat("ZIP contents\n", 100000)
			refs := uploadFileTestRefs(strings.NewReader(contents))
			if stale {
				old := uploadFileTestSchema(t, s, "old.zip", contents)
				s.hits = map[string][]blob.Ref{refs[1].String(): {old}}
				s.verify = false
			}
			// Upload from the current offset, not from the beginning of r.
			raw := strings.NewReader("skip this prefix" + contents)
			raw.Seek(int64(len("skip this prefix")), io.SeekStart)
			reader := &uploadFileTestSeeker{ReadSeeker: raw}
			got, err := c.UploadFile(context.Background(), "new.zip", reader, nil)
			if err != nil {
				t.Fatal(err)
			}
			checkUploadFileTestQuery(t, s, refs)
			if reader.readBytes != 2*int64(len(contents)) {
				t.Errorf("read %d bytes; want hash and upload passes (%d)", reader.readBytes, 2*len(contents))
			}
			fr, err := schema.NewFileReader(context.Background(), &s.storage, got)
			if err != nil {
				t.Fatal(err)
			}
			defer fr.Close()
			gotRefs := uploadFileTestRefs(fr)
			if !reflect.DeepEqual(gotRefs, refs) || fr.FileName() != "new.zip" {
				t.Errorf("uploaded file = %v, %q; want %v, new.zip", gotRefs, fr.FileName(), refs)
			}
		})
	}
}

func TestUploadFileNonSeekableStreams(t *testing.T) {
	const size = 64 << 20
	c, s := newUploadFileTestClient(t)
	reader := &uploadFileTestZeros{left: size}
	reader.beforeRead = func(n int64) error {
		// The chunk writer has at most 32 bounded chunks in flight. By this
		// point it must have uploaded some; a whole-input prehash cannot.
		if n > 40<<20 && s.storage.NumBlobs() == 0 {
			return errors.New("read ahead of all chunk uploads: buffering entire input")
		}
		return nil
	}
	got, err := c.UploadFile(context.Background(), "stream.zip", reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reader.readBytes != size {
		t.Errorf("read %d bytes; want %d", reader.readBytes, size)
	}
	select {
	case refs := <-s.queries:
		t.Errorf("unexpected prehash search for stream: %v", refs)
	default:
	}
	fr, err := schema.NewFileReader(context.Background(), &s.storage, got)
	if err != nil {
		t.Fatal(err)
	}
	defer fr.Close()
	if fr.Size() != size {
		t.Errorf("uploaded file size = %d; want %d", fr.Size(), size)
	}
	gotRefs := uploadFileTestRefs(fr)
	wantRefs := uploadFileTestRefs(&uploadFileTestZeros{left: size})
	if !reflect.DeepEqual(gotRefs, wantRefs) {
		t.Errorf("uploaded hashes = %v; want %v", gotRefs, wantRefs)
	}
}

// Pipe sizes can change during reads on macOS. Reproduce that metadata change
// on every platform while retaining a real pipe's unsupported Seek behavior.
type uploadFileTestChangingPipe struct {
	*os.File
	stats int64
}

type uploadFileTestPipeInfo struct {
	os.FileInfo
	size int64
}

func (i uploadFileTestPipeInfo) Size() int64 { return i.size }

func (r *uploadFileTestChangingPipe) Stat() (os.FileInfo, error) {
	info, err := r.File.Stat()
	if err != nil {
		return nil, err
	}
	r.stats++
	return uploadFileTestPipeInfo{info, r.stats}, nil
}

func TestUploadFilePipe(t *testing.T) {
	for _, changing := range []bool{false, true} {
		t.Run(fmt.Sprintf("changingMetadata=%v", changing), func(t *testing.T) {
			c, s := newUploadFileTestClient(t)
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			const contents = "a pipe implements Seek but is not seekable"
			if _, err := io.WriteString(w, contents); err != nil {
				w.Close()
				t.Fatal(err)
			}
			w.Close()
			var input io.Reader = r
			if changing {
				input = &uploadFileTestChangingPipe{File: r}
			}
			got, err := c.UploadFile(context.Background(), "pipe.zip", input, nil)
			if err != nil {
				t.Fatal(err)
			}
			fr, err := schema.NewFileReader(context.Background(), &s.storage, got)
			if err != nil {
				t.Fatal(err)
			}
			defer fr.Close()
			data, err := io.ReadAll(fr)
			if err != nil || string(data) != contents {
				t.Errorf("uploaded pipe = %q, %v; want %q, nil", data, err, contents)
			}
			select {
			case refs := <-s.queries:
				t.Errorf("unexpected whole-file search for pipe: %v", refs)
			default:
			}
		})
	}
}

func TestUploadFileEmpty(t *testing.T) {
	c, s := newUploadFileTestClient(t)
	got, err := c.UploadFile(context.Background(), "empty.zip", strings.NewReader(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkUploadFileTestQuery(t, s, uploadFileTestRefs(strings.NewReader("")))
	b, err := c.FetchSchemaBlob(context.Background(), got)
	if err != nil {
		t.Fatal(err)
	}
	if b.PartsSize() != 0 || b.FileName() != "empty.zip" {
		t.Errorf("empty schema = %d, %q; want 0, empty.zip", b.PartsSize(), b.FileName())
	}
}

func TestUploadFileSuppliedWholeRefs(t *testing.T) {
	const contents = "known ZIP contents"
	refs := uploadFileTestRefs(strings.NewReader(contents))
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate=%v", duplicate), func(t *testing.T) {
			c, s := newUploadFileTestClient(t)
			if duplicate {
				old := uploadFileTestSchema(t, s, "old.zip", contents)
				s.hits = map[string][]blob.Ref{refs[2].String(): {old}}
			}
			// A reader-only wrapper proves supplied hashes don't need seeking
			// or a preliminary data pass.
			reader := &uploadFileTestSeeker{ReadSeeker: strings.NewReader(contents)}
			opts := &FileUploadOptions{WholeRef: refs[0], WholeRefs: []blob.Ref{refs[1], refs[2], refs[0]}}
			_, err := c.UploadFile(context.Background(), "new.zip", struct{ io.Reader }{reader}, opts)
			if err != nil {
				t.Fatal(err)
			}
			checkUploadFileTestQuery(t, s, refs)
			wantRead := int64(len(contents))
			if duplicate {
				wantRead = 0
			}
			if reader.readBytes != wantRead {
				t.Errorf("read %d bytes; want %d", reader.readBytes, wantRead)
			}
		})
	}
}

func checkUploadFileNoSchema(t *testing.T, s *uploadFileTestServer, filename string) {
	t.Helper()
	for _, ref := range s.storage.BlobrefStrings() {
		contents, _ := s.storage.BlobContents(blob.MustParse(ref))
		b, err := schema.BlobFromReader(blob.MustParse(ref), strings.NewReader(contents))
		if err == nil && b.Type() == schema.TypeFile && b.FileName() == filename {
			t.Errorf("published file schema %v despite failed upload", ref)
		}
	}
}

func TestUploadFileChangedContents(t *testing.T) {
	c, s := newUploadFileTestClient(t)
	raw := strings.NewReader("before")
	reader := &uploadFileTestSeeker{ReadSeeker: raw, onRewind: func() { raw.Reset("after!") }}
	got, err := c.UploadFile(context.Background(), "changed.zip", reader, nil)
	if err == nil || got.Valid() {
		t.Fatalf("UploadFile = %v, %v; want changed-input error", got, err)
	}
	checkUploadFileNoSchema(t, s, "changed.zip")
}

func TestUploadFileIncorrectWholeRefs(t *testing.T) {
	c, s := newUploadFileTestClient(t)
	refs := uploadFileTestRefs(strings.NewReader("wrong"))
	got, err := c.UploadFile(context.Background(), "wrong.zip", strings.NewReader("actual"), &FileUploadOptions{WholeRefs: refs})
	if err == nil || got.Valid() {
		t.Fatalf("UploadFile = %v, %v; want wrong-digest error", got, err)
	}
	checkUploadFileNoSchema(t, s, "wrong.zip")
}

func TestUploadFileChangedLocalFile(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate=%v", duplicate), func(t *testing.T) {
			c, s := newUploadFileTestClient(t)
			const contents = "original contents"
			f, err := os.CreateTemp(t.TempDir(), "change-*.zip")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := io.WriteString(f, contents); err != nil {
				t.Fatal(err)
			}
			f.Seek(0, io.SeekStart)
			if duplicate {
				old := uploadFileTestSchema(t, s, "old.zip", contents)
				refs := uploadFileTestRefs(strings.NewReader(contents))
				s.hits = map[string][]blob.Ref{refs[0].String(): {old}}
			}
			s.onSearch = func() {
				if err := f.Truncate(int64(len(contents) + 1)); err != nil {
					t.Error(err)
				}
			}
			got, err := c.UploadFile(context.Background(), "changed.zip", f, nil)
			if err == nil || got.Valid() {
				t.Fatalf("UploadFile = %v, %v; want changed-file error", got, err)
			}
			checkUploadFileNoSchema(t, s, "changed.zip")
		})
	}
}

func TestUploadFileCancellation(t *testing.T) {
	for _, seekable := range []bool{false, true} {
		t.Run(fmt.Sprintf("seekable=%v", seekable), func(t *testing.T) {
			c, s := newUploadFileTestClient(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			zeros := &uploadFileTestZeros{left: 1 << 20}
			zeros.beforeRead = func(n int64) error {
				if n > 0 {
					cancel()
				}
				return nil
			}
			var reader io.Reader = zeros
			if seekable {
				// SectionReader supplies seeking while reading generated zeros.
				reader = io.NewSectionReader(uploadFileTestReaderAt{zeros}, 0, 1<<20)
			}
			got, err := c.UploadFile(ctx, "canceled.zip", reader, nil)
			if !errors.Is(err, context.Canceled) || got.Valid() {
				t.Fatalf("UploadFile = %v, %v; want context.Canceled", got, err)
			}
			checkUploadFileNoSchema(t, s, "canceled.zip")
		})
	}
}

// Used only for the sequential hash pass of the cancellation test.
type uploadFileTestReaderAt struct{ r io.Reader }

func (r uploadFileTestReaderAt) ReadAt(p []byte, _ int64) (int, error) { return r.r.Read(p) }

func TestUploadFileLocalMetadata(t *testing.T) {
	c, s := newUploadFileTestClient(t)
	f, err := os.CreateTemp(t.TempDir(), "metadata-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := io.WriteString(f, "archive contents"); err != nil {
		t.Fatal(err)
	}
	f.Seek(0, io.SeekStart)
	modTime := time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(f.Name(), modTime, modTime); err != nil {
		t.Fatal(err)
	}
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.UploadFile(context.Background(), "chosen.zip", f, &FileUploadOptions{FileInfo: fi})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.FetchSchemaBlob(context.Background(), got)
	if err != nil {
		t.Fatal(err)
	}
	if b.FileName() != "chosen.zip" || !b.ModTime().Equal(modTime) {
		t.Errorf("file metadata = %q, %v; want chosen.zip, %v", b.FileName(), b.ModTime(), modTime)
	}
	if s.storage.NumBlobs() < 2 {
		t.Error("missing uploaded file content or schema")
	}
}
