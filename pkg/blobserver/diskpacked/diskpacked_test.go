/*
Copyright 2013 The Perkeep Authors

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

package diskpacked

import (
	"bufio"
	"context"
	"errors"
	"expvar"
	"fmt"
	"io"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

var ctxbg = context.Background()

func init() {
	debug = debugT(env.IsDebug())
}

func newTempDiskpacked(t *testing.T) blobserver.Storage {
	return newTempDiskpackedWithIndex(t, jsonconfig.Obj{})
}

func newTempDiskpackedMemory(t *testing.T) blobserver.Storage {
	return newTempDiskpackedWithIndex(t, jsonconfig.Obj{
		"type": "memory",
	})
}

const (
	testMaxFileSize  = 8192
	testFdCacheLimit = 3
)

func newTempDiskpackedWithIndex(t *testing.T, indexConf jsonconfig.Obj) blobserver.Storage {
	test.TLog(t)
	dir, err := os.MkdirTemp("", "diskpacked-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("diskpacked test dir is %q", dir)
	s, err := newStorage(dir, testMaxFileSize, testFdCacheLimit, indexConf)
	if err != nil {
		t.Fatalf("newStorage: %v", err)
	}
	t.Cleanup(func() {
		s.Close()
		if env.IsDebug() {
			t.Logf("CAMLI_DEBUG set, skipping cleanup of dir %q", dir)
		} else {
			os.RemoveAll(dir)
		}
	})
	return s
}

func TestDiskpacked(t *testing.T) {
	storagetest.Test(t, newTempDiskpacked)
}

func TestDiskpackedAltIndex(t *testing.T) {
	storagetest.Test(t, newTempDiskpackedMemory)
}

func TestDoubleReceive(t *testing.T) {
	sto := newTempDiskpacked(t)

	size := func(n int) int64 {
		path := sto.(*storage).filename(n)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}

	const blobSize = 5 << 10
	b := &test.Blob{Contents: strings.Repeat("a", blobSize)}
	br := b.BlobRef()

	_, err := blobserver.Receive(ctxbg, sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if size(0) < blobSize {
		t.Fatalf("size = %d; want at least %d", size(0), blobSize)
	}
	if err = sto.(*storage).nextPack(); err != nil {
		t.Fatal(err)
	}

	_, err = blobserver.Receive(ctxbg, sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	sizePostDup := size(1)
	if sizePostDup >= blobSize {
		t.Fatalf("size(pack1) = %d; appeared to double-write.", sizePostDup)
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	sto := newTempDiskpacked(t)

	var (
		A = &test.Blob{Contents: "some small blob"}
		B = &test.Blob{Contents: strings.Repeat("some middle blob", 100)}
		C = &test.Blob{Contents: strings.Repeat("A 8192 bytes length largish blob", 8192/32)}
	)

	type step func() error

	stepAdd := func(tb *test.Blob) step { // add the blob
		return func() error {
			sb, err := sto.ReceiveBlob(ctxbg, tb.BlobRef(), tb.Reader())
			if err != nil {
				return fmt.Errorf("ReceiveBlob of %s: %w", sb, err)
			}
			if sb != tb.SizedRef() {
				return fmt.Errorf("Received %v; want %v", sb, tb.SizedRef())
			}
			return nil
		}
	}

	stepCheck := func(want ...*test.Blob) step { // check the blob
		wantRefs := make([]blob.SizedRef, len(want))
		for i, tb := range want {
			wantRefs[i] = tb.SizedRef()
		}
		return func() error {
			if err := storagetest.CheckEnumerate(sto, wantRefs); err != nil {
				return err
			}
			return nil
		}
	}

	stepDelete := func(tb *test.Blob) step {
		return func() error {
			if err := sto.RemoveBlobs(ctx, []blob.Ref{tb.BlobRef()}); err != nil {
				return fmt.Errorf("RemoveBlob(%s): %w", tb.BlobRef(), err)
			}
			return nil
		}
	}

	var deleteTests = [][]step{
		{
			stepAdd(A),
			stepDelete(A),
			stepCheck(),
			stepAdd(B),
			stepCheck(B),
			stepDelete(B),
			stepCheck(),
			stepAdd(C),
			stepCheck(C),
			stepAdd(A),
			stepCheck(A, C),
			stepDelete(A),
			stepDelete(C),
			stepCheck(),
		},
		{
			stepAdd(A),
			stepAdd(B),
			stepAdd(C),
			stepCheck(A, B, C),
			stepDelete(C),
			stepCheck(A, B),
		},
	}
	for i, steps := range deleteTests {
		for j, s := range steps {
			if err := s(); err != nil {
				t.Errorf("error at test %d, step %d: %v", i+1, j+1, err)
			}
		}
	}
}

var errDummy = errors.New("dummy fail")

func TestDoubleReceiveFailingIndex(t *testing.T) {
	sto := newTempDiskpacked(t)

	sto.(*storage).index = &failingIndex{KeyValue: sto.(*storage).index}

	size := func(n int) int64 {
		path := sto.(*storage).filename(n)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}

	const blobSize = 5 << 10
	b := &test.Blob{Contents: strings.Repeat("a", blobSize)}
	br := b.BlobRef()

	_, err := blobserver.Receive(ctxbg, sto, br, b.Reader())
	if err != nil {
		if !errors.Is(err, errDummy) {
			t.Fatal(err)
		}
		t.Logf("dummy fail")
	}
	if size(0) >= blobSize {
		t.Fatalf("size = %d; want zero (at most %d)", size(0), blobSize-1)
	}

	_, err = blobserver.Receive(ctxbg, sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if size(0) < blobSize {
		t.Fatalf("size = %d; want at least %d", size(0), blobSize)
	}
}

// TestFailingIndexAtRollover checks that when indexing a blob that fills
// its pack file fails, the blob is removed from that pack, and the next
// pack isn't started (or truncated) on its behalf.
func TestFailingIndexAtRollover(t *testing.T) {
	sto := newTempDiskpacked(t)
	s := sto.(*storage)
	small := &test.Blob{Contents: "small"}
	if _, err := blobserver.Receive(ctxbg, sto, small.BlobRef(), small.Reader()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(s.filename(0))
	if err != nil {
		t.Fatal(err)
	}
	sizeAfterSmall := fi.Size()

	s.index = &failingIndex{KeyValue: s.index}
	big := &test.Blob{Contents: strings.Repeat("b", testMaxFileSize)}
	if _, err := blobserver.Receive(ctxbg, sto, big.BlobRef(), big.Reader()); !errors.Is(err, errDummy) {
		t.Fatalf("Receive with failing index = %v; want %v", err, errDummy)
	}
	if fi, err := os.Stat(s.filename(0)); err != nil || fi.Size() != sizeAfterSmall {
		t.Fatalf("pack 0 after failed Receive: %v, %v; want size %d", fi, err, sizeAfterSmall)
	}
	if _, err := os.Stat(s.filename(1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pack 1 after failed Receive: %v; want it not to exist", err)
	}

	if _, err := blobserver.Receive(ctxbg, sto, big.BlobRef(), big.Reader()); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(s.filename(1)); err != nil || fi.Size() != 0 {
		t.Fatalf("new pack 1: %v, %v; want it empty", fi, err)
	}
	for _, b := range []*test.Blob{small, big} {
		rc, _, err := sto.Fetch(ctxbg, b.BlobRef())
		if err != nil {
			t.Fatalf("Fetch %v: %v", b.BlobRef(), err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || string(got) != b.Contents {
			t.Fatalf("Fetch %v = %d bytes, %v", b.BlobRef(), len(got), err)
		}
	}
}

// TestFailingRollover checks that a blob filling its pack file is
// received even if starting the next pack file fails, and that a later
// Receive starts it.
func TestFailingRollover(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping directory permission test on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("skipping directory permission test as root")
	}
	sto := newTempDiskpackedMemory(t)
	s := sto.(*storage)
	// Make creating the next pack file fail.
	if err := os.Chmod(s.root, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(s.root, 0700)

	big := &test.Blob{Contents: strings.Repeat("b", testMaxFileSize)}
	if _, err := blobserver.Receive(ctxbg, sto, big.BlobRef(), big.Reader()); err != nil {
		t.Fatalf("Receive when rollover fails: %v", err)
	}
	if s.writer != nil {
		t.Fatal("writer still set after failed rollover")
	}
	small := &test.Blob{Contents: "small"}
	if _, err := blobserver.Receive(ctxbg, sto, small.BlobRef(), small.Reader()); err == nil {
		t.Fatal("Receive succeeded with no pack file to write to")
	}

	if err := os.Chmod(s.root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := blobserver.Receive(ctxbg, sto, small.BlobRef(), small.Reader()); err != nil {
		t.Fatal(err)
	}
	for _, b := range []*test.Blob{big, small} {
		m, err := s.meta(b.BlobRef())
		if err != nil {
			t.Fatal(err)
		}
		if want := map[*test.Blob]int{big: 0, small: 1}[b]; m.file != want {
			t.Errorf("%v is in pack %d; want %d", b.BlobRef(), m.file, want)
		}
	}
}

type failingIndex struct {
	sorted.KeyValue
	setCount int
}

func (idx *failingIndex) Set(key string, value string) error {
	idx.setCount++
	if idx.setCount == 1 { // fail the first time
		return errDummy
	}
	return idx.KeyValue.Set(key, value)
}

func TestReadHeader(t *testing.T) {
	tests := []struct {
		in           string
		wantConsumed int
		wantDigest   string
		wantSize     uint32
		wantErr      bool
	}{
		{"[foo-123 234]", 13, "foo-123", 234, false},

		// Too short:
		{in: "", wantErr: true},
		{in: "[", wantErr: true},
		{in: "[]", wantErr: true},
		// Missing brackets:
		{in: "[foo-123 234", wantErr: true},
		{in: "foo-123 234]", wantErr: true},
		// non-number in size:
		{in: "[foo-123 234x]", wantErr: true},
		// No spce:
		{in: "[foo-abcd1234]", wantErr: true},
	}
	for _, tt := range tests {
		consumed, digest, size, err := readHeader(bufio.NewReader(strings.NewReader(tt.in)))
		if tt.wantErr {
			if err == nil {
				t.Errorf("readHeader(%q) = %d, %q, %v with nil error; but wanted an error",
					tt.in, consumed, digest, size)
			}
		} else if consumed != tt.wantConsumed ||
			string(digest) != tt.wantDigest ||
			size != tt.wantSize ||
			err != nil {
			t.Errorf("readHeader(%q) = %d, %q, %v, %v; want %d, %q, %v, nil",
				tt.in,
				consumed, digest, size, err,
				tt.wantConsumed, tt.wantDigest, tt.wantSize)
		}
	}
}

func TestClose(t *testing.T) {
	fds := func() (n int) {
		openFdsVar.Do(func(kv expvar.KeyValue) {
			if i, ok := kv.Value.(*expvar.Int); ok {
				inc, _ := strconv.Atoi(i.String())
				n += inc
			}
		})
		return
	}

	fd0 := fds()
	sto := newTempDiskpackedMemory(t)
	fd1 := fds()

	s := sto.(*storage)

	const blobSize = 5 << 10
	b := &test.Blob{Contents: strings.Repeat("a", blobSize)}
	br := b.BlobRef()

	fd2 := fds()
	_, err := blobserver.Receive(ctxbg, sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	fd3 := fds()

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	fd4 := fds()
	got := [...]int{fd1 - fd0, fd2 - fd1, fd3 - fd2, fd4 - fd3}
	want := [...]int{+2, 0, 0, -2}
	if got != want {
		t.Errorf("fd count over time = %v; want %v", got, want)
	}

}

func TestFetchReadStats(t *testing.T) {
	sto := newTempDiskpackedMemory(t)
	s := sto.(*storage)
	b := &test.Blob{Contents: "some blob contents"}
	if _, err := blobserver.Receive(ctxbg, sto, b.BlobRef(), b.Reader()); err != nil {
		t.Fatal(err)
	}
	rc, _, err := sto.Fetch(ctxbg, b.BlobRef())
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if _, err := io.Copy(io.Discard, rc); err != nil {
		t.Fatal(err)
	}
	want := strconv.Itoa(len(b.Contents))
	if got := readVar.Get(s.filename(0)).String(); got != want {
		t.Errorf("per-file read bytes = %s; want %s", got, want)
	}
	if got := readTotVar.Get(s.root).String(); got != want {
		t.Errorf("total read bytes = %s; want %s", got, want)
	}
}

// TestCloseWithActiveReader verifies that Close doesn't wait for
// readers still holding a pack file, and that the file is closed
// when the reader is.
func TestCloseWithActiveReader(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		sto, err := newStorage(dir, 1<<20, 1, jsonconfig.Obj{"type": "memory"})
		if err != nil {
			t.Fatal(err)
		}
		b := &test.Blob{Contents: "some blob contents"}
		if _, err := blobserver.Receive(ctxbg, sto, b.BlobRef(), b.Reader()); err != nil {
			t.Fatal(err)
		}
		rc, _, err := sto.Fetch(ctxbg, b.BlobRef())
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(rc, buf); err != nil {
			t.Fatal(err)
		}

		// With an active reader, this deadlocks the bubble if Close waits.
		if err := sto.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		rest, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("reading after Close: %v", err)
		}
		if got := string(buf) + string(rest); got != b.Contents {
			t.Errorf("read %q; want %q", got, b.Contents)
		}
		if err := rc.Close(); err != nil {
			t.Fatalf("reader Close: %v", err)
		}
		if got := openFdsVar.Get(dir).String(); got != "0" {
			t.Errorf("open fds after reader Close = %s; want 0", got)
		}
	})
}

func TestBadDir(t *testing.T) {
	s, err := newStorage("hopefully this is a not existing directory", 1<<20, 1, jsonconfig.Obj{"type": "memory"})
	if err == nil {
		s.Close()
		t.Errorf("expected error for non-existing directory")
	}
}

func TestWriteError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping symlink test on Windows")
	}
	dir, err := os.MkdirTemp("", "diskpacked-test")
	if err != nil {
		t.Fatal(err)
	}
	if !env.IsDebug() {
		defer os.RemoveAll(dir)
	}
	t.Logf("diskpacked test dir is %q", dir)
	fn := filepath.Join(dir, "pack-00000.blobs")
	if err := os.Symlink("/non existing file", fn); err != nil {
		t.Fatal(err)
	}
	s, err := newStorage(dir, 1, 1, jsonconfig.Obj{"type": "memory"})
	if err == nil {
		s.Close()
		t.Fatal("expected error for non-existing directory")
	}
}

func TestConcurrentFetch(t *testing.T) {
	sto := newTempDiskpacked(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	const blobSize = testMaxFileSize + 1
	brs := make(map[string]blob.Ref, 2)
	for i := range testFdCacheLimit + 1 {
		k := string([]byte{'a' + byte(i)})
		b := &test.Blob{Contents: strings.Repeat(k, blobSize)}
		br := b.BlobRef()
		brs[k] = br

		_, err := blobserver.Receive(ctxbg, sto, br, b.Reader())
		if err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for range 2 * len(brs) {
		wg.Go(func() {
			keys := slices.Collect(maps.Keys(brs))
			rand.Shuffle(len(brs), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
			for _, k := range keys {
				br := brs[k]
				rc, size, err := sto.Fetch(ctx, br)
				if err != nil {
					t.Error(err)
					return
				}
				t.Logf("size: %d", size)
				if got, readErr := io.Copy(io.Discard, rc); readErr != nil {
					t.Errorf("read: %+v", readErr)
				} else if got != int64(size) {
					t.Errorf("got %d, wanted %d bytes", got, size)
				}
				if err := rc.Close(); err != nil {
					t.Errorf("Close: %v", err)
				}
			}
		})
	}
	wg.Wait()
}
