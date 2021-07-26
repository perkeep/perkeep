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
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/env"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

var ctxbg = context.Background()

func newTempDiskpacked(t *testing.T) (sto blobserver.Storage, cleanup func()) {
	return newTempDiskpackedWithIndex(t, jsonconfig.Obj{})
}

func newTempDiskpackedMemory(t *testing.T) (sto blobserver.Storage, cleanup func()) {
	return newTempDiskpackedWithIndex(t, jsonconfig.Obj{
		"type": "memory",
	})
}

func newTempDiskpackedWithIndex(t *testing.T, indexConf jsonconfig.Obj) (sto blobserver.Storage, cleanup func()) {
	restoreLogging := test.TLog(t)
	dir, err := ioutil.TempDir("", "diskpacked-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("diskpacked test dir is %q", dir)
	s, err := newStorage(dir, 1<<20, indexConf)
	if err != nil {
		t.Fatalf("newStorage: %v", err)
	}
	return s, func() {
		s.Close()
		if env.IsDebug() {
			t.Logf("CAMLI_DEBUG set, skipping cleanup of dir %q", dir)
		} else {
			os.RemoveAll(dir)
		}
		restoreLogging()
	}
}

func TestDiskpacked(t *testing.T) {
	storagetest.Test(t, newTempDiskpacked)
}

func TestDiskpackedAltIndex(t *testing.T) {
	storagetest.Test(t, newTempDiskpackedMemory)
}

func TestDoubleReceive(t *testing.T) {
	sto, cleanup := newTempDiskpacked(t)
	defer cleanup()

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
	sto, cleanup := newTempDiskpacked(t)
	defer cleanup()

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
	sto, cleanup := newTempDiskpacked(t)
	defer cleanup()

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
	sto, cleanup := newTempDiskpackedMemory(t)
	defer cleanup()
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

func TestBadDir(t *testing.T) {
	s, err := newStorage("hopefully this is a not existing directory", 1<<20, jsonconfig.Obj{"type": "memory"})
	if err == nil {
		s.Close()
		t.Errorf("expected error for non-existing directory")
	}
}

func TestWriteError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping symlink test on Windows")
	}
	dir, err := ioutil.TempDir("", "diskpacked-test")
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
	s, err := newStorage(dir, 1, jsonconfig.Obj{"type": "memory"})
	if err == nil {
		s.Close()
		t.Fatal("expected error for non-existing directory")
	}
}
