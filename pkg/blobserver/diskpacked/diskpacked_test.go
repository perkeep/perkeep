/*
Copyright 2013 The Camlistore Authors

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
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/test"
)

func newTempDiskpacked(t *testing.T) (sto blobserver.Storage, cleanup func()) {
	dir, err := ioutil.TempDir("", "diskpacked-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("diskpacked test dir is %q", dir)
	s, err := newStorage(dir, 1<<20)
	if err != nil {
		t.Fatalf("newStorage: %v", err)
	}
	return s, func() {
		s.Close()
		os.RemoveAll(dir)
	}
}

func TestDiskpacked(t *testing.T) {
	storagetest.Test(t, newTempDiskpacked)
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

	_, err := blobserver.Receive(sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	if size(0) < blobSize {
		t.Fatalf("size = %d; want at least %d", size(0), blobSize)
	}
	sto.(*storage).nextPack()

	_, err = blobserver.Receive(sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	sizePostDup := size(1)
	if sizePostDup >= blobSize {
		t.Fatalf("size(pack1) = %d; appeared to double-write.", sizePostDup)
	}

	os.Remove(sto.(*storage).filename(0))
	_, err = blobserver.Receive(sto, br, b.Reader())
	if err != nil {
		t.Fatal(err)
	}
	sizePostDelete := size(1)
	if sizePostDelete < blobSize {
		t.Fatalf("after packfile delete + reupload, not big enough. want size of a blob")
	}
}
