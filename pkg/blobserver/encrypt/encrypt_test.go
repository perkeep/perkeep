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

package encrypt

/*
Dev notes:

$ devcam put --path=/enc/ blob dev-camput
sha1-282c0feceeb5cdf4c5086c191b15356fadfb2392
$ devcam get --path=/enc/ sha1-282c0feceeb5cdf4c5086c191b15356fadfb2392
$ find /tmp/camliroot-$USER/port3179/encblob/
$ ./dev-camtool sync --src=http://localhost:3179/enc/ --dest=stdout
*/

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"filippo.io/age"
	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"
)

var ctxbg = context.Background()

var testIdentity, _ = age.GenerateX25519Identity()

type testStorage struct {
	sto   *storage
	blobs *test.Fetcher
	meta  *test.Fetcher
}

// fetchOrErrorString fetches br from sto and returns its body as a string.
// If an error occurs the stringified error is returned, prefixed by "Error: ".
func (ts *testStorage) fetchOrErrorString(br blob.Ref) string {
	rc, _, err := ts.sto.Fetch(ctxbg, br)
	var slurp []byte
	if err == nil {
		defer rc.Close()
		slurp, err = io.ReadAll(rc)
	}
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return string(slurp)
}

func newTestStorage() *testStorage {
	sto := &storage{
		index:     sorted.NewMemoryKeyValue(),
		smallMeta: &metaBlobHeap{},
		identity:  testIdentity,
	}
	ts := &testStorage{
		sto:   sto,
		blobs: new(test.Fetcher),
		meta:  new(test.Fetcher),
	}
	sto.blobs = ts.blobs
	sto.meta = ts.meta
	return ts
}

func TestStorage(t *testing.T) {
	storagetest.TestOpt(t, storagetest.Opts{
		New: func(t *testing.T) blobserver.Storage {
			return newTestStorage().sto
		},
	})
}

func TestEncrypt(t *testing.T) {
	ts := newTestStorage()

	const blobData = "foofoofoo"
	tb := &test.Blob{Contents: blobData}
	tb.MustUpload(t, ts.sto)
	if got := ts.fetchOrErrorString(tb.BlobRef()); got != blobData {
		t.Errorf("Fetching plaintext blobref %v = %v; want %q", tb.BlobRef(), got, blobData)
	}

	// Make sure the plaintext doesn't show up anywhere.
	for _, bs := range []*test.Fetcher{ts.meta, ts.blobs} {
		c := make(chan blob.SizedRef)
		go bs.EnumerateBlobs(context.TODO(), c, "", 0)
		for sb := range c {
			data, ok := bs.BlobContents(sb.Ref)
			if !ok {
				panic("where did it go?")
			}
			if strings.Contains(data, blobData) {
				t.Error("plaintext found in storage")
			}
		}
	}

	const blobData2 = "bar"
	tb2 := &test.Blob{Contents: blobData2}
	tb2.MustUpload(t, ts.sto)
	if got := ts.fetchOrErrorString(tb2.BlobRef()); got != blobData2 {
		t.Errorf("Fetching plaintext blobref %v = %v; want %q", tb2.BlobRef(), got, blobData2)
	}

	missingError := "Error: file does not exist"
	tb3 := &test.Blob{Contents: "xxx"}
	if got := ts.fetchOrErrorString(tb3.BlobRef()); got != missingError {
		t.Errorf("Fetching missing blobref %v; want %q", got, missingError)
	}

	ctx := context.Background()
	got, err := blobserver.StatBlobs(ctx, ts.sto, []blob.Ref{tb3.BlobRef(), tb.BlobRef(), tb2.BlobRef()})
	if err != nil {
		t.Fatalf("StatBlobs: %v", err)
	}
	if sr := got[tb.BlobRef()]; sr != tb.SizedRef() {
		t.Errorf("tb stat = %v; want %v", sr, tb.SizedRef())
	}
	if sr := got[tb2.BlobRef()]; sr != tb2.SizedRef() {
		t.Errorf("tb2 stat = %v; want %v", sr, tb2.SizedRef())
	}
	if sr, ok := got[tb3.BlobRef()]; ok {
		t.Errorf("unexpected stat response for tb3: %v", sr)
	}
	if len(got) != 2 {
		t.Errorf("unexpected blobs in stat response")
	}

	c := make(chan blob.SizedRef)
	go func() {
		if err := ts.sto.EnumerateBlobs(context.TODO(), c, "", 0); err != nil {
			t.Error(err)
		}
	}()
	if sr := <-c; sr != tb2.SizedRef() {
		t.Errorf("%s != %s", sr, tb2.SizedRef())
	}
	if sr := <-c; sr != tb.SizedRef() {
		t.Errorf("%s != %s", sr, tb.SizedRef())
	}
	if _, ok := <-c; ok {
		t.Error("did not close the channel")
	}
}

func TestEncryptStress(t *testing.T) {
	const (
		workers  = 20
		numBlobs = 1000
	)

	ts := newTestStorage()

	var wg sync.WaitGroup
	defer wg.Wait()

	blobs := make(chan string, workers)
	defer close(blobs)

	for range workers {
		wg.Go(func() {
			for blob := range blobs {
				tb := &test.Blob{Contents: blob}
				tb.MustUpload(t, ts.sto)
				if got := ts.fetchOrErrorString(tb.BlobRef()); got != blob {
					t.Errorf("Fetching plaintext blobref %v = %v; want %q", tb.BlobRef(), got, blob)
				}
			}
		})
	}

	for i := range numBlobs {
		blobs <- fmt.Sprintf("%d", i)
	}
}

func TestLoadMeta(t *testing.T) {
	ts := newTestStorage()
	const blobData = "foo"
	tb := &test.Blob{Contents: blobData}
	tb.MustUpload(t, ts.sto)
	const blobData2 = "bar"
	tb2 := &test.Blob{Contents: blobData2}
	tb2.MustUpload(t, ts.sto)
	meta, blobs := ts.meta, ts.blobs

	ts = newTestStorage()
	ts.meta, ts.blobs = meta, blobs
	ts.sto.meta, ts.sto.blobs = meta, blobs
	if err := ts.sto.readAllMetaBlobs(); err != nil {
		t.Fatal(err)
	}
	if got := ts.fetchOrErrorString(tb.BlobRef()); got != blobData {
		t.Errorf("Fetching plaintext blobref %v = %v; want %q", tb.BlobRef(), got, blobData)
	}
	if got := ts.fetchOrErrorString(tb2.BlobRef()); got != blobData2 {
		t.Errorf("Fetching plaintext blobref %v = %v; want %q", tb2.BlobRef(), got, blobData2)
	}
}

func TestNewFromConfig(t *testing.T) {
	ld := test.NewLoader()

	// Using key file
	tmpKeyFile, _ := os.CreateTemp(t.TempDir(), "camlitest")
	defer os.Remove((tmpKeyFile.Name()))
	defer tmpKeyFile.Close()
	tmpKeyFile.WriteString(testIdentity.String())

	if _, err := newFromConfig(ld, jsonconfig.Obj{
		"I_AGREE": "that encryption support hasn't been peer-reviewed, isn't finished, and its format might change.",
		"keyFile": tmpKeyFile.Name(),
		"blobs":   "/good-blobs/",
		"meta":    "/good-meta/",
		"metaIndex": map[string]any{
			"type": "memory",
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Using public key file
	if runtime.GOOS != "windows" {
		os.Chmod(tmpKeyFile.Name(), 0644)
		if _, err := newFromConfig(ld, jsonconfig.Obj{
			"I_AGREE": "that encryption support hasn't been peer-reviewed, isn't finished, and its format might change.",
			"keyFile": tmpKeyFile.Name(),
			"blobs":   "/good-blobs/",
			"meta":    "/good-meta/",
			"metaIndex": map[string]any{
				"type": "memory",
			},
		}); err == nil || !strings.Contains(err.Error(), "Key file permissions are too permissive") {
			t.Fatal(err)
		}
	}

}
