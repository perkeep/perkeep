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
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/client"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/test"
)

// These tests are of the behaviors client.UploadFile had, which its
// callers moved to package upload rely on.

// readerFileRef returns the file schema blob client.UploadFile wrote for
// contents named name: a schema.NewFileMap of the name.
func readerFileRef(t *testing.T, name string, contents []byte) blob.Ref {
	t.Helper()
	br, err := schema.WriteFileMap(context.Background(), new(test.Fetcher), schema.NewFileMap(name), bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	return br
}

// runReader uploads contents read from r as the file name.
func runReader(t *testing.T, c *client.Client, name string, r io.Reader, opts *Options) (*Result, *File) {
	t.Helper()
	f := NewFileFromReader(c, name, r, opts)
	res, err := f.Run(context.Background())
	if err != nil {
		t.Fatalf("uploading %s: %v", name, err)
	}
	return res, f
}

// checkNotUploaded checks that the server doesn't have br.
func checkNotUploaded(t *testing.T, c *client.Client, br blob.Ref) {
	t.Helper()
	if _, err := blobserver.StatBlob(context.Background(), c, br); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat of %v = %v; want it not uploaded", br, err)
	}
}

// TestFromReader checks uploading contents from a reader: small (read
// ahead, and uploaded without a session) and big (streamed), with the
// file schema blob client.UploadFile wrote, and the default whole-file
// ref, and without asking whether the server has the contents.
func TestFromReader(t *testing.T) {
	c, rc := newCountingClient(t)
	for _, size := range []int{0, 5, 3 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			contents := randBytes(uint64(30+size%7), size)
			rc.take()
			res, _ := runReader(t, c, "doc.pdf", struct{ io.Reader }{bytes.NewReader(contents)}, nil)
			if n := rc.take()["search"]; n != 0 {
				t.Errorf("%d searches; want none", n)
			}
			if want := readerFileRef(t, "doc.pdf", contents); res.FileRef != want {
				t.Errorf("FileRef = %v; want %v", res.FileRef, want)
			}
			if want := []blob.Ref{blob.RefFromBytes(contents)}; !slices.Equal(res.WholeRefs, want) {
				t.Errorf("WholeRefs = %v; want %v", res.WholeRefs, want)
			}
			if res.Size != int64(size) || !res.Durable || res.Dup {
				t.Errorf("Result = %+v; want size %d, durable, not a dup", res, size)
			}
			checkContents(t, c, res.FileRef, contents)
		})
	}
}

// zeros is a reader of n zeros that neither seeks nor writes to, calling
// beforeRead, if set, before each read with how many it's read.
type zeros struct {
	n, read    int64
	beforeRead func(read int64) error
}

func (z *zeros) Read(p []byte) (int, error) {
	if z.beforeRead != nil {
		if err := z.beforeRead(z.read); err != nil {
			return 0, err
		}
	}
	if z.n == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), z.n)
	clear(p[:n])
	z.n -= n
	z.read += n
	return int(n), nil
}

// TestFromReaderStreams checks that contents from a reader are uploaded as
// they're read, not read whole first.
func TestFromReaderStreams(t *testing.T) {
	c := newTestClient(t)
	const size = 64 << 20
	var f *File
	r := &countingReader{r: io.LimitReader(rand.NewChaCha8([32]byte{36}), size)}
	checked := &checkingReader{r: r, check: func() error {
		if p := f.Progress(); r.read > 40<<20 && p.ChunksUploaded == 0 {
			return errors.New("read 40 MB before uploading any chunk")
		}
		return nil
	}}
	f = NewFileFromReader(c, "stream.zip", checked, nil)
	res, err := f.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.read != size || res.Size != size {
		t.Errorf("read %d bytes, Result.Size %d; want %d", r.read, res.Size, size)
	}
	fr, err := schema.NewFileReader(context.Background(), c, res.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	defer fr.Close()
	h := blob.NewHash()
	if _, err := io.Copy(h, fr); err != nil {
		t.Fatal(err)
	}
	if got := blob.RefFromHash(h); fr.Size() != size || !slices.Equal(res.WholeRefs, []blob.Ref{got}) {
		t.Errorf("uploaded %d bytes of %v; want %d of %v", fr.Size(), got, size, res.WholeRefs)
	}
}

// TestFromReaderPipe checks uploading from a pipe, which has Seek and Stat
// methods but can't seek, and whose Stat can change as it's read.
func TestFromReaderPipe(t *testing.T) {
	c := newTestClient(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	const contents = "a pipe implements Seek but is not seekable"
	go func() {
		io.WriteString(w, contents)
		w.Close()
	}()
	res, _ := runReader(t, c, "pipe.zip", r, nil)
	checkContents(t, c, res.FileRef, []byte(contents))
}

// TestFromReaderWholeRefs checks contents from a reader with their
// whole-file refs given: found on the server without reading them, or
// read once and checked against the refs.
func TestFromReaderWholeRefs(t *testing.T) {
	c, rc := newCountingClient(t)
	contents := randBytes(31, 1<<20)
	refs := []blob.Ref{blob.RefFromBytes(contents)}
	z := &countingReader{r: bytes.NewReader(contents)}
	first, _ := runReader(t, c, "a.zip", z, &Options{WholeRefs: refs})
	if first.Dup || z.read != int64(len(contents)) {
		t.Errorf("first upload: %+v, read %d bytes; want not a dup, read once", first, z.read)
	}

	rc.take()
	z = &countingReader{r: bytes.NewReader(contents)}
	again, _ := runReader(t, c, "b.zip", z, &Options{WholeRefs: refs})
	if !again.Dup || z.read != 0 {
		t.Errorf("again: %+v, read %d bytes; want a dup, unread", again, z.read)
	}
	// A dup needs no session: a search, a stat, and the file schema.
	if got := rc.take(); got["search"] != 1 || got["open"] != 0 || got["upload"] != 1 {
		t.Errorf("requests for a dup %v; want a search and one upload, no session", got)
	}

	// Refs that don't match the contents.
	wrong := randBytes(32, 1<<20)
	f := NewFileFromReader(c, "wrong.zip", bytes.NewReader(wrong), &Options{WholeRefs: []blob.Ref{blob.RefFromString("other")}})
	if _, err := f.Run(context.Background()); !errors.Is(err, ErrChanged) {
		t.Errorf("Run with wrong WholeRefs = %v; want %v", err, ErrChanged)
	}
	checkNotUploaded(t, c, readerFileRef(t, "wrong.zip", wrong))
}

// checkingReader is a reader of r that fails with check's error.
type checkingReader struct {
	r     io.Reader
	check func() error
}

func (r *checkingReader) Read(p []byte) (int, error) {
	if err := r.check(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

type countingReader struct {
	r    io.Reader
	read int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.read += int64(n)
	return n, err
}

// changingReaderAt is an io.ReaderAt of before, until it's read from the
// start a second time, and then of after.
type changingReaderAt struct {
	before, after []byte
	starts        int
}

func (r *changingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off == 0 {
		r.starts++
	}
	b := r.before
	if r.starts > 1 {
		b = r.after
	}
	return bytes.NewReader(b).ReadAt(p, off)
}

// TestUploadChangedContents checks that a File whose contents change
// between its hashing them and cutting them fails, uploading no file
// schema blob.
func TestUploadChangedContents(t *testing.T) {
	c := newTestClient(t)
	before, after := randBytes(33, 1<<20), randBytes(34, 1<<20)
	r := &changingReaderAt{before: before, after: after}
	f := NewFile(c, fileInfo{"changed.zip", int64(len(before)), testModTime}, r, nil)
	if _, err := f.Run(context.Background()); !errors.Is(err, ErrChanged) {
		t.Fatalf("Run = %v; want %v", err, ErrChanged)
	}
	for _, contents := range [][]byte{before, after} {
		file := schema.NewCommonFileMap("changed.zip", fileInfo{"changed.zip", int64(len(before)), testModTime})
		file.SetType(schema.TypeFile)
		br, err := schema.WriteFileMap(context.Background(), new(test.Fetcher), file, bytes.NewReader(contents))
		if err != nil {
			t.Fatal(err)
		}
		checkNotUploaded(t, c, br)
	}
}

// growingFile is an *os.File that grows once read past at.
type growingFile struct {
	*os.File
	at    int64
	grown bool
}

func (f *growingFile) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > f.at && !f.grown {
		f.grown = true
		fi, err := f.File.Stat()
		if err != nil {
			return 0, err
		}
		if err := f.File.Truncate(fi.Size() + 1); err != nil {
			return 0, err
		}
	}
	return f.File.ReadAt(p, off)
}

// TestUploadChangedLocalFile checks that a File of an *os.File that changes
// while it runs fails, uploading no file schema blob, whether it's small,
// or big and on the server already.
func TestUploadChangedLocalFile(t *testing.T) {
	c := newTestClient(t)
	for _, tt := range []struct {
		name string
		size int
		dup  bool
	}{
		{"small", 100, false},
		{"dup", 1 << 20, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			contents := randBytes(uint64(35+tt.size%5), tt.size)
			if tt.dup {
				uploadFile(t, c, "original.zip", contents, nil)
			}
			path := t.TempDir() + "/changed.zip"
			if err := os.WriteFile(path, contents, 0o644); err != nil {
				t.Fatal(err)
			}
			osf, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer osf.Close()
			fi, err := osf.Stat()
			if err != nil {
				t.Fatal(err)
			}
			// Opened for writing too, to grow it.
			rw, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer rw.Close()
			gf := &growingFile{File: rw, at: int64(tt.size / 2)}
			f := NewFile(c, fi, gf, nil)
			res, err := f.Run(context.Background())
			if !errors.Is(err, ErrChanged) {
				t.Fatalf("Run = %+v, %v; want %v", res, err, ErrChanged)
			}
			file := schema.NewCommonFileMap(fi.Name(), fi)
			file.SetType(schema.TypeFile)
			br, err := schema.WriteFileMap(context.Background(), new(test.Fetcher), file, bytes.NewReader(contents))
			if err != nil {
				t.Fatal(err)
			}
			checkNotUploaded(t, c, br)
		})
	}
}

// TestUploadCanceled checks that a File stops when its context is done
// while reading, from a reader or not, uploading no file schema blob.
func TestUploadCanceled(t *testing.T) {
	c := newTestClient(t)
	for _, fromReader := range []bool{false, true} {
		t.Run(fmt.Sprintf("fromReader=%v", fromReader), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			const size = 4 << 20
			z := &zeros{n: size, beforeRead: func(read int64) error {
				if read > 1<<20 {
					cancel()
				}
				return nil
			}}
			var f *File
			if fromReader {
				f = NewFileFromReader(c, "canceled.zip", z, nil)
			} else {
				f = NewFile(c, fileInfo{"canceled.zip", size, testModTime}, readerAt{z}, &Options{
					WholeRefs: []blob.Ref{blob.RefFromString("not hashed")}, // so it reads once, in order
				})
			}
			if _, err := f.Run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("Run = %v; want %v", err, context.Canceled)
			}
		})
	}
}

// readerAt is an io.ReaderAt of r, for reading in order, once.
type readerAt struct{ r io.Reader }

func (r readerAt) ReadAt(p []byte, _ int64) (int, error) { return io.ReadFull(r.r, p) }

// TestUploadLocalMetadata checks that a File of an *os.File records its
// name and modification time.
func TestUploadLocalMetadata(t *testing.T) {
	c := newTestClient(t)
	path := t.TempDir() + "/chosen.zip"
	if err := os.WriteFile(path, []byte("archive contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	modTime := time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	osf, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer osf.Close()
	fi, err := osf.Stat()
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewFile(c, fi, osf, nil).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.FetchSchemaBlob(context.Background(), res.FileRef)
	if err != nil {
		t.Fatal(err)
	}
	if b.FileName() != "chosen.zip" || !b.ModTime().Equal(modTime) {
		t.Errorf("file metadata = %q, %v; want chosen.zip, %v", b.FileName(), b.ModTime(), modTime)
	}

	// The file schema blob client.UploadFile wrote with a FileInfo.
	file := schema.NewCommonFileMap("chosen.zip", fi)
	file.SetModTime(fi.ModTime())
	file.SetType(schema.TypeFile)
	want, err := schema.WriteFileMap(context.Background(), new(test.Fetcher), file, strings.NewReader("archive contents"))
	if err != nil {
		t.Fatal(err)
	}
	if res.FileRef != want {
		t.Errorf("FileRef = %v; want %v, as client.UploadFile wrote", res.FileRef, want)
	}
}

// TestUploadLargeDup checks that a File of a big file the server has reads
// it once, to hash it, and cuts no chunks.
func TestUploadLargeDup(t *testing.T) {
	c := newTestClient(t)
	const size = 128 << 20
	path := t.TempDir() + "/large.zip"
	osf, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer osf.Close()
	if err := osf.Truncate(size); err != nil { // sparse zeros
		t.Fatal(err)
	}
	fi, err := osf.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewFile(c, fi, osf, nil).Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	r := &countingReaderAt{r: osf}
	f := NewFile(c, fileInfo{"other.zip", size, testModTime}, r, nil)
	res, err := f.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p := f.Progress(); !res.Dup || r.read.Load() != size || p.Chunks != 0 {
		t.Errorf("Result = %+v, read %d bytes, %d chunks; want a dup, read once, no chunks", res, r.read.Load(), p.Chunks)
	}
}

type countingReaderAt struct {
	r    io.ReaderAt
	read atomic.Int64
}

func (r *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.r.ReadAt(p, off)
	r.read.Add(int64(n))
	return n, err
}
