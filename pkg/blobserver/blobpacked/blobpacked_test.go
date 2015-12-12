/*
Copyright 2014 The Camlistore Authors

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

package blobpacked

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/constants"
	"camlistore.org/pkg/schema"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/test"
	"camlistore.org/third_party/go/pkg/archive/zip"
	"golang.org/x/net/context"

	"go4.org/syncutil"
)

const debug = false

var brokenTests = flag.Bool("broken", false, "also test known-broken tests")

func TestStorage(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
		s := &storage{
			small: new(test.Fetcher),
			large: new(test.Fetcher),
			meta:  sorted.NewMemoryKeyValue(),
			log:   test.NewLogger(t, "blobpacked: "),
		}
		s.init()
		return s, func() {}
	})
}

func TestStorageNoSmallSubfetch(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
		s := &storage{
			// We need to hide SubFetcher, to test *storage's SubFetch, as it delegates
			// to the underlying SubFetcher, if small implements that interface.
			small: hideSubFetcher(new(test.Fetcher)),
			large: new(test.Fetcher),
			meta:  sorted.NewMemoryKeyValue(),
			log:   test.NewLogger(t, "blobpacked: "),
		}
		s.init()
		return s, func() {}
	})
}

func hideSubFetcher(sto blobserver.Storage) blobserver.Storage {
	if _, ok := sto.(blob.SubFetcher); ok {
		return struct{ blobserver.Storage }{sto}
	}
	return sto
}

func TestParseMetaRow(t *testing.T) {
	cases := []struct {
		in   string
		want meta
		err  bool
	}{
		{in: "123 sx", err: true},
		{in: "-123 s", err: true},
		{in: "", err: true},
		{in: "1 ", err: true},
		{in: " ", err: true},
		{in: "123 x", err: true},
		{in: "123 l", err: true},
		{in: "123 sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15", err: true},
		{in: "123 notaref 12", err: true},
		{in: "123 sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15 42 extra", err: true},
		{in: "123 sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15 42 ", err: true},
		{in: "123 sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15 42", want: meta{
			exists:   true,
			size:     123,
			largeRef: blob.MustParse("sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15"),
			largeOff: 42,
		}},
	}
	for _, tt := range cases {
		got, err := parseMetaRow([]byte(tt.in))
		if (err != nil) != tt.err {
			t.Errorf("For %q error = %v; want-err? = %v", tt.in, err, tt.err)
			continue
		}
		if tt.err {
			continue
		}
		if got != tt.want {
			t.Errorf("For %q, parseMetaRow = %+v; want %+v", tt.in, got, tt.want)
		}
	}
}

func wantNumLargeBlobs(want int) func(*packTest) {
	return func(pt *packTest) { pt.wantLargeBlobs = want }
}

func wantNumSmallBlobs(want int) func(*packTest) {
	return func(pt *packTest) { pt.wantSmallBlobs = want }
}

func okayWithoutMeta(refStr string) func(*packTest) {
	return func(pt *packTest) {
		if pt.okayNoMeta == nil {
			pt.okayNoMeta = map[blob.Ref]bool{}
		}
		pt.okayNoMeta[blob.MustParse(refStr)] = true
	}
}

func randBytes(n int) []byte {
	r := rand.New(rand.NewSource(42))
	s := make([]byte, n)
	for i := range s {
		s[i] = byte(r.Int63())
	}
	return s
}

func TestPackNormal(t *testing.T) {
	const fileSize = 5 << 20
	const fileName = "foo.dat"
	fileContents := randBytes(fileSize)

	hash := blob.NewHash()
	hash.Write(fileContents)
	wholeRef := blob.RefFromHash(hash)

	pt := testPack(t,
		func(sto blobserver.Storage) error {
			_, err := schema.WriteFileFromReader(sto, fileName, bytes.NewReader(fileContents))
			return err
		},
		wantNumLargeBlobs(1),
		wantNumSmallBlobs(0),
	)
	// And verify we can read it back out.
	pt.testOpenWholeRef(t, wholeRef, fileSize)
}

func TestPackNoDelete(t *testing.T) {
	const fileSize = 1 << 20
	const fileName = "foo.dat"
	fileContents := randBytes(fileSize)
	testPack(t,
		func(sto blobserver.Storage) error {
			_, err := schema.WriteFileFromReader(sto, fileName, bytes.NewReader(fileContents))
			return err
		},
		func(pt *packTest) { pt.sto.skipDelete = true },
		wantNumLargeBlobs(1),
		wantNumSmallBlobs(15), // empirically
	)
}

func TestPackLarge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	const fileSize = 17 << 20 // more than 16 MB, so more than one zip
	const fileName = "foo.dat"
	fileContents := randBytes(fileSize)

	hash := blob.NewHash()
	hash.Write(fileContents)
	wholeRef := blob.RefFromHash(hash)

	pt := testPack(t,
		func(sto blobserver.Storage) error {
			_, err := schema.WriteFileFromReader(sto, fileName, bytes.NewReader(fileContents))
			return err
		},
		wantNumLargeBlobs(2),
		wantNumSmallBlobs(0),
	)

	// Verify we wrote the correct "w:*" meta rows.
	got := map[string]string{}
	want := map[string]string{
		"w:" + wholeRef.String():        "17825792 2",
		"w:" + wholeRef.String() + ":0": "sha1-9b4a3d114c059988075c87293c86ee7cbc6f4af5 37 0 16709479",
		"w:" + wholeRef.String() + ":1": "sha1-fe6326ac6b389ffe302623e4a501bfc8c6272e8e 37 16709479 1116313",
	}
	if err := sorted.Foreach(pt.sto.meta, func(key, value string) error {
		if strings.HasPrefix(key, "b:") {
			return nil
		}
		got[key] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("'w:*' meta rows = %v; want %v", got, want)
	}

	// And verify we can read it back out.
	pt.testOpenWholeRef(t, wholeRef, fileSize)
}

func (pt *packTest) testOpenWholeRef(t *testing.T, wholeRef blob.Ref, wantSize int64) {
	rc, gotSize, err := pt.sto.OpenWholeRef(wholeRef, 0)
	if err != nil {
		t.Errorf("OpenWholeRef = %v", err)
		return
	}
	defer rc.Close()
	if gotSize != wantSize {
		t.Errorf("OpenWholeRef size = %v; want %v", gotSize, wantSize)
		return
	}
	h := blob.NewHash()
	n, err := io.Copy(h, rc)
	if err != nil {
		t.Errorf("OpenWholeRef read error: %v", err)
		return
	}
	if n != wantSize {
		t.Errorf("OpenWholeRef read %v bytes; want %v", n, wantSize)
		return
	}
	gotRef := blob.RefFromHash(h)
	if gotRef != wholeRef {
		t.Errorf("OpenWholeRef read contents = %v; want %v", gotRef, wholeRef)
	}
}

func TestPackTwoIdenticalfiles(t *testing.T) {
	const fileSize = 1 << 20
	fileContents := randBytes(fileSize)
	testPack(t,
		func(sto blobserver.Storage) (err error) {
			if _, err = schema.WriteFileFromReader(sto, "a.txt", bytes.NewReader(fileContents)); err != nil {
				return
			}
			if _, err = schema.WriteFileFromReader(sto, "b.txt", bytes.NewReader(fileContents)); err != nil {
				return
			}
			return
		},
		func(pt *packTest) { pt.sto.packGate = syncutil.NewGate(1) }, // one pack at a time
		wantNumLargeBlobs(1),
		wantNumSmallBlobs(1), // just the "b.txt" file schema blob
		okayWithoutMeta("sha1-cb4399f6b3b31ace417e1ec9326f9818bb3f8387"),
	)
}

// packTest is the state kept while running func testPack.
type packTest struct {
	sto                   *storage
	logical, small, large *test.Fetcher

	wantLargeBlobs interface{} // nil means disabled, else int
	wantSmallBlobs interface{} // nil means disabled, else int

	okayNoMeta map[blob.Ref]bool
}

func testPack(t *testing.T,
	write func(sto blobserver.Storage) error,
	checks ...func(*packTest),
) *packTest {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	logical := new(test.Fetcher)
	small, large := new(test.Fetcher), new(test.Fetcher)
	pt := &packTest{
		logical: logical,
		small:   small,
		large:   large,
	}
	// Figure out the logical baseline blobs we'll later expect in the packed storage.
	if err := write(logical); err != nil {
		t.Fatal(err)
	}
	t.Logf("items in logical storage: %d", logical.NumBlobs())

	pt.sto = &storage{
		small: small,
		large: large,
		meta:  sorted.NewMemoryKeyValue(),
		log:   test.NewLogger(t, "blobpacked: "),
	}
	pt.sto.init()

	for _, setOpt := range checks {
		setOpt(pt)
	}

	if err := write(pt.sto); err != nil {
		t.Fatal(err)
	}

	t.Logf("items in small: %v", small.NumBlobs())
	t.Logf("items in large: %v", large.NumBlobs())

	if want, ok := pt.wantLargeBlobs.(int); ok && want != large.NumBlobs() {
		t.Fatalf("num large blobs = %d; want %d", large.NumBlobs(), want)
	}
	if want, ok := pt.wantSmallBlobs.(int); ok && want != small.NumBlobs() {
		t.Fatalf("num small blobs = %d; want %d", small.NumBlobs(), want)
	}

	var zipRefs []blob.Ref
	var zipSeen = map[blob.Ref]bool{}
	blobserver.EnumerateAll(ctx, large, func(sb blob.SizedRef) error {
		zipRefs = append(zipRefs, sb.Ref)
		zipSeen[sb.Ref] = true
		return nil
	})
	if len(zipRefs) != large.NumBlobs() {
		t.Fatalf("Enumerated only %d zip files; expected %d", len(zipRefs), large.NumBlobs())
	}

	bytesOfZip := map[blob.Ref][]byte{}
	for _, zipRef := range zipRefs {
		rc, _, err := large.Fetch(zipRef)
		if err != nil {
			t.Fatal(err)
		}
		zipBytes, err := ioutil.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("Error slurping %s: %v", zipRef, err)
		}
		if len(zipBytes) > constants.MaxBlobSize {
			t.Fatalf("zip is too large: %d > max %d", len(zipBytes), constants.MaxBlobSize)
		}
		bytesOfZip[zipRef] = zipBytes
		zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
		if err != nil {
			t.Fatalf("Error reading resulting zip file: %v", err)
		}
		if len(zr.File) == 0 {
			t.Fatal("zip is empty")
		}
		nameSeen := map[string]bool{}
		for i, zf := range zr.File {
			if nameSeen[zf.Name] {
				t.Errorf("duplicate name %q seen", zf.Name)
			}
			nameSeen[zf.Name] = true
			t.Logf("zip[%d] size %d, %v", i, zf.UncompressedSize64, zf.Name)
		}
		mfr, err := zr.File[len(zr.File)-1].Open()
		if err != nil {
			t.Fatalf("Error opening manifest JSON: %v", err)
		}
		maniJSON, err := ioutil.ReadAll(mfr)
		if err != nil {
			t.Fatalf("Error reading manifest JSON: %v", err)
		}
		var mf Manifest
		if err := json.Unmarshal(maniJSON, &mf); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}

		// Verify each chunk described in the manifest:
		baseOffset, err := zr.File[0].DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		for _, bo := range mf.DataBlobs {
			h := bo.Ref.Hash()
			h.Write(zipBytes[baseOffset+bo.Offset : baseOffset+bo.Offset+int64(bo.Size)])
			if !bo.Ref.HashMatches(h) {
				t.Errorf("blob %+v didn't describe the actual data in the zip", bo)
			}
		}
		if debug {
			t.Logf("Manifest: %s", maniJSON)
		}
	}

	// Verify that each chunk in the logical mapping is in the meta.
	logBlobs := 0
	if err := blobserver.EnumerateAll(ctx, logical, func(sb blob.SizedRef) error {
		logBlobs++
		v, err := pt.sto.meta.Get(blobMetaPrefix + sb.Ref.String())
		if err == sorted.ErrNotFound && pt.okayNoMeta[sb.Ref] {
			return nil
		}
		if err != nil {
			return fmt.Errorf("error looking up logical blob %v in meta: %v", sb.Ref, err)
		}
		m, err := parseMetaRow([]byte(v))
		if err != nil {
			return fmt.Errorf("error parsing logical blob %v meta %q: %v", sb.Ref, v, err)
		}
		if !m.exists || m.size != sb.Size || !zipSeen[m.largeRef] {
			return fmt.Errorf("logical blob %v = %+v; want in zip", sb.Ref, m)
		}
		h := sb.Ref.Hash()
		h.Write(bytesOfZip[m.largeRef][m.largeOff : m.largeOff+sb.Size])
		if !sb.Ref.HashMatches(h) {
			t.Errorf("blob %v not found matching in zip", sb.Ref)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if logBlobs != logical.NumBlobs() {
		t.Error("enumerate over logical blobs didn't work?")
	}

	// TODO, more tests:
	// -- like TestPackTwoIdenticalfiles, but instead of testing
	// no dup for 100% identical file bytes, test that uploading a
	// 49% identical one does not denormalize and repack.
	// -- test StreamBlobs in all its various flavours, and recovering from stream blobs.
	// -- overflowing the 16MB chunk size with huge initial chunks
	return pt
}

// see if storage proxies through to small for Fetch, Stat, and Enumerate.
func TestSmallFallback(t *testing.T) {
	small := new(test.Fetcher)
	s := &storage{
		small: small,
		large: new(test.Fetcher),
		meta:  sorted.NewMemoryKeyValue(),
		log:   test.NewLogger(t, "blobpacked: "),
	}
	s.init()
	b1 := &test.Blob{"foo"}
	b1.MustUpload(t, small)
	wantSB := b1.SizedRef()

	// Fetch
	rc, _, err := s.Fetch(b1.BlobRef())
	if err != nil {
		t.Errorf("failed to Get blob: %v", err)
	} else {
		rc.Close()
	}

	// Stat.
	sb, err := blobserver.StatBlob(s, b1.BlobRef())
	if err != nil {
		t.Errorf("failed to Stat blob: %v", err)
	} else if sb != wantSB {
		t.Errorf("Stat = %v; want %v", sb, wantSB)
	}

	// Enumerate
	saw := false
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	if err := blobserver.EnumerateAll(ctx, s, func(sb blob.SizedRef) error {
		if sb != wantSB {
			return fmt.Errorf("saw blob %v; want %v", sb, wantSB)
		}
		saw = true
		return nil
	}); err != nil {
		t.Errorf("EnuerateAll: %v", err)
	}
	if !saw {
		t.Error("didn't see blob in Enumerate")
	}
}

func TestZ_LeakCheck(t *testing.T) {
	if testing.Short() {
		return
	}
	time.Sleep(50 * time.Millisecond) // let goroutines schedule & die off
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	n := bytes.Count(buf, []byte("[chan receive]:"))
	if n > 1 {
		t.Errorf("%d goroutines in chan receive: %s", n, buf)
	}
}

func TestForeachZipBlob(t *testing.T) {
	const fileSize = 2 << 20
	const fileName = "foo.dat"
	fileContents := randBytes(fileSize)

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	pt := testPack(t,
		func(sto blobserver.Storage) error {
			_, err := schema.WriteFileFromReader(sto, fileName, bytes.NewReader(fileContents))
			return err
		},
		wantNumLargeBlobs(1),
		wantNumSmallBlobs(0),
	)

	zipBlob, err := singleBlob(pt.large)
	if err != nil {
		t.Fatal(err)
	}
	zipBytes := slurpBlob(t, pt.large, zipBlob.Ref)
	zipSize := len(zipBytes)

	all := map[blob.Ref]blob.SizedRef{}
	if err := blobserver.EnumerateAll(ctx, pt.logical, func(sb blob.SizedRef) error {
		all[sb.Ref] = sb
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	foreachSaw := 0
	blobSizeSum := 0
	if err := pt.sto.foreachZipBlob(zipBlob.Ref, func(bap BlobAndPos) error {
		foreachSaw++
		blobSizeSum += int(bap.Size)
		want, ok := all[bap.Ref]
		if !ok {
			t.Errorf("unwanted blob ref returned from foreachZipBlob: %v", bap.Ref)
			return nil
		}
		delete(all, bap.Ref)
		if want.Size != bap.Size {
			t.Errorf("for %v, foreachZipBlob size = %d; want %d", bap.Ref, bap.Size, want.Size)
			return nil
		}

		// Verify the offset.
		h := bap.Ref.Hash()
		h.Write(zipBytes[bap.Offset : bap.Offset+int64(bap.Size)])
		if !bap.Ref.HashMatches(h) {
			return fmt.Errorf("foreachZipBlob returned blob %v at offset %d that failed validation", bap.Ref, bap.Offset)
		}

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	t.Logf("foreachZipBlob enumerated %d blobs", foreachSaw)
	if len(all) > 0 {
		t.Errorf("foreachZipBlob forgot to enumerate %d blobs: %v", len(all), all)
	}
	// Calculate per-blobref zip overhead (zip file headers/TOC/manifest file, etc)
	zipOverhead := zipSize - blobSizeSum
	t.Logf("zip fixed overhead = %d bytes, for %d blobs (%d bytes each)", zipOverhead, foreachSaw, zipOverhead/foreachSaw)
}

// singleBlob assumes that sto contains a single blob and returns it.
// If there are more or fewer than one blob, it's an error.
func singleBlob(sto blobserver.BlobEnumerator) (ret blob.SizedRef, err error) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	n := 0
	if err = blobserver.EnumerateAll(ctx, sto, func(sb blob.SizedRef) error {
		ret = sb
		n++
		return nil
	}); err != nil {
		return blob.SizedRef{}, err
	}
	if n != 1 {
		return blob.SizedRef{}, fmt.Errorf("saw %d blobs; want 1", n)
	}
	return
}

func TestRemoveBlobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	// The basic small cases are handled via storagetest in TestStorage,
	// so this only tests removing packed blobs.

	small := new(test.Fetcher)
	large := new(test.Fetcher)
	sto := &storage{
		small: small,
		large: large,
		meta:  sorted.NewMemoryKeyValue(),
		log:   test.NewLogger(t, "blobpacked: "),
	}
	sto.init()

	const fileSize = 1 << 20
	fileContents := randBytes(fileSize)
	if _, err := schema.WriteFileFromReader(sto, "foo.dat", bytes.NewReader(fileContents)); err != nil {
		t.Fatal(err)
	}
	if small.NumBlobs() != 0 || large.NumBlobs() == 0 {
		t.Fatalf("small, large counts == %d, %d; want 0, non-zero", small.NumBlobs(), large.NumBlobs())
	}
	var all []blob.SizedRef
	if err := blobserver.EnumerateAll(ctx, sto, func(sb blob.SizedRef) error {
		all = append(all, sb)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Find the zip
	zipBlob, err := singleBlob(sto.large)
	if err != nil {
		t.Fatalf("failed to find packed zip: %v", err)
	}

	// The zip file is in use, so verify we can't delete it.
	if err := sto.deleteZipPack(zipBlob.Ref); err == nil {
		t.Fatalf("zip pack blob deleted but it should not have been allowed")
	}

	// Delete everything
	for len(all) > 0 {
		del := all[0].Ref
		all = all[1:]
		if err := sto.RemoveBlobs([]blob.Ref{del}); err != nil {
			t.Fatalf("RemoveBlobs: %v", err)
		}
		if err := storagetest.CheckEnumerate(sto, all); err != nil {
			t.Fatalf("After deleting %v, %v", del, err)
		}
	}

	dRows := func() (n int) {
		if err := sorted.ForeachInRange(sto.meta, "d:", "", func(key, value string) error {
			if strings.HasPrefix(key, "d:") {
				n++
			}
			return nil
		}); err != nil {
			t.Fatalf("meta iteration error: %v", err)
		}
		return
	}

	if n := dRows(); n == 0 {
		t.Fatalf("expected a 'd:' row after deletes")
	}

	// TODO: test the background pack-deleter loop? figure out its design first.
	if err := sto.deleteZipPack(zipBlob.Ref); err != nil {
		t.Errorf("error deleting zip %v: %v", zipBlob.Ref, err)
	}
	if n := dRows(); n != 0 {
		t.Errorf("expected the 'd:' row to be deleted")
	}
}

func setIntTemporarily(i *int, tempVal int) (restore func()) {
	old := *i
	*i = tempVal
	return func() { *i = old }
}

func TestPackerBoundarySplits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow test")
	}
	// Test a file of three chunk sizes, totalling near the 16 MB
	// boundary:
	//    - 1st chunk is 6 MB. ("blobA")
	//    - 2nd chunk is 6 MB. ("blobB")
	//    - 3rd chunk ("blobC") is binary-searched (up to 4MB) to find
	//      which size causes the packer to write two zip files.

	// During the test we set zip overhead boundaries to 0, to
	// force the test to into its pathological misprediction code paths,
	// where it needs to back up and rewrite the zip with one part less.
	// That's why the test starts with two zip files: so there's at
	// least one that can be removed to make room.
	defer setIntTemporarily(&zipPerEntryOverhead, 0)()

	const sizeAB = 12 << 20
	const maxBlobSize = 16 << 20
	bytesAB := randBytes(sizeAB)
	blobA := &test.Blob{string(bytesAB[:sizeAB/2])}
	blobB := &test.Blob{string(bytesAB[sizeAB/2:])}
	refA := blobA.BlobRef()
	refB := blobB.BlobRef()
	bytesCFull := randBytes(maxBlobSize - sizeAB) // will be sliced down

	// Mechanism to verify we hit the back-up code path:
	var (
		mu                    sync.Mutex
		sawTruncate           blob.Ref
		stoppedBeforeOverflow bool
	)
	testHookSawTruncate = func(after blob.Ref) {
		if after != refB {
			t.Errorf("unexpected truncate point %v", after)
		}
		mu.Lock()
		defer mu.Unlock()
		sawTruncate = after
	}
	testHookStopBeforeOverflowing = func() {
		mu.Lock()
		defer mu.Unlock()
		stoppedBeforeOverflow = true
	}
	defer func() {
		testHookSawTruncate = nil
		testHookStopBeforeOverflowing = nil
	}()

	generatesTwoZips := func(sizeC int) (ret bool) {
		large := new(test.Fetcher)
		s := &storage{
			small: new(test.Fetcher),
			large: large,
			meta:  sorted.NewMemoryKeyValue(),
			log: test.NewLogger(t, "blobpacked: ",
				// Ignore these phrases:
				"Packing file ",
				"Packed file ",
			),
		}
		s.init()

		// Upload first two chunks
		blobA.MustUpload(t, s)
		blobB.MustUpload(t, s)

		// Upload second chunk
		bytesC := bytesCFull[:sizeC]
		h := blob.NewHash()
		h.Write(bytesC)
		refC := blob.RefFromHash(h)
		_, err := s.ReceiveBlob(refC, bytes.NewReader(bytesC))
		if err != nil {
			t.Fatal(err)
		}

		// Upload the file schema blob.
		m := schema.NewFileMap("foo.dat")
		m.PopulateParts(sizeAB+int64(sizeC), []schema.BytesPart{
			schema.BytesPart{
				Size:    sizeAB / 2,
				BlobRef: refA,
			},
			schema.BytesPart{
				Size:    sizeAB / 2,
				BlobRef: refB,
			},
			schema.BytesPart{
				Size:    uint64(sizeC),
				BlobRef: refC,
			},
		})
		fjson, err := m.JSON()
		if err != nil {
			t.Fatalf("schema filemap JSON: %v", err)
		}
		fb := &test.Blob{Contents: fjson}
		fb.MustUpload(t, s)
		num := large.NumBlobs()
		if num < 1 || num > 2 {
			t.Fatalf("for size %d, num packed zip blobs = %d; want 1 or 2", sizeC, num)
		}
		return num == 2
	}
	maxC := maxBlobSize - sizeAB
	smallestC := sort.Search(maxC, generatesTwoZips)
	if smallestC == maxC {
		t.Fatalf("never found a point at which we generated 2 zip files")
	}
	t.Logf("After 12 MB of data (in 2 chunks), the smallest blob that generates two zip files is %d bytes (%.03f MB)", smallestC, float64(smallestC)/(1<<20))
	t.Logf("Zip overhead (for this two chunk file) = %d bytes", maxBlobSize-1-smallestC-sizeAB)

	mu.Lock()
	if sawTruncate != refB {
		t.Errorf("truncate after = %v; want %v", sawTruncate, refB)
	}
	if !stoppedBeforeOverflow {
		t.Error("never hit the code path where it calculates that another data chunk would push it over the 16MB boundary")
	}
}

func slurpBlob(t *testing.T, sto blob.Fetcher, br blob.Ref) []byte {
	rc, _, err := sto.Fetch(br)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	slurp, err := ioutil.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return slurp
}
