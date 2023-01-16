/*
Copyright 2014 The Perkeep Authors

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
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/constants"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"

	"go4.org/syncutil"
)

const debug = false

var ctxbg = context.Background()

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

func randBytesSrc(n int, src int64) []byte {
	r := rand.New(rand.NewSource(src))
	s := make([]byte, n)
	for i := range s {
		s[i] = byte(r.Int63())
	}
	return s
}

func randBytes(n int) []byte {
	return randBytesSrc(n, 42)
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
			_, err := schema.WriteFileFromReader(ctxbg, sto, fileName, bytes.NewReader(fileContents))
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
			_, err := schema.WriteFileFromReader(ctxbg, sto, fileName, bytes.NewReader(fileContents))
			return err
		},
		func(pt *packTest) { pt.sto.skipDelete = true },
		wantNumLargeBlobs(1),
		wantNumSmallBlobs(14), // empirically
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
			_, err := schema.WriteFileFromReader(ctxbg, sto, fileName, bytes.NewReader(fileContents))
			return err
		},
		wantNumLargeBlobs(2),
		wantNumSmallBlobs(0),
	)

	// Gather the "w:*" meta rows we wrote.
	got := map[string]string{}
	if err := sorted.Foreach(pt.sto.meta, func(key, value string) error {
		if strings.HasPrefix(key, "b:") {
			return nil
		}
		got[key] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Verify the two zips are correctly described.

	// There should be one row to say that we have two zip, and
	// that the overall file is 17MB:
	keyBase := "w:" + wholeRef.String()
	if g, w := got[keyBase], "17825792 2"; g != w {
		t.Fatalf("meta row for key %q = %q; want %q", keyBase, g, w)
	}

	// ... (and a little helper) ...
	parseMeta := func(n int) (zipOff, dataOff, dataLen int64) {
		key := keyBase + ":" + strconv.Itoa(n)
		v := got[key]
		f := strings.Fields(v)
		if len(f) != 4 {
			t.Fatalf("meta for key %q = %q; expected 4 space-separated fields", key, v)
		}
		i64 := func(n int) int64 {
			i, err := strconv.ParseInt(f[n], 10, 64)
			if err != nil {
				t.Fatalf("error parsing int64 %q in field index %d of meta key %q (value %q): %v", f[n], n, key, v, err)
			}
			return i
		}
		zipOff, dataOff, dataLen = i64(1), i64(2), i64(3)
		return
	}

	// And then verify if we have the two "w:<wholeref>:0" and
	// "w:<wholeref>:1" rows and that they're consistent.
	z0, d0, l0 := parseMeta(0)
	z1, d1, l1 := parseMeta(1)
	if z0 != z1 {
		t.Errorf("expected zip offset in zip0 and zip1 to match. got %d and %d", z0, z0)
	}
	if d0 != 0 {
		t.Errorf("zip0's data offset = %d; want 0", d0)
	}
	if d1 != l0 {
		t.Errorf("zip1 data offset %d != zip0 data length %d", d1, l0)
	}
	if d1+l1 != fileSize {
		t.Errorf("zip1's offset %d + length %d = %d; want %d (fileSize)", d1, l1, d1+l1, fileSize)
	}

	// And verify we can read it back out.
	pt.testOpenWholeRef(t, wholeRef, fileSize)
}

func countSortedRows(t *testing.T, meta sorted.KeyValue) int {
	rows := 0
	if err := sorted.Foreach(meta, func(key, value string) error {
		rows++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestParseZipMetaRow(t *testing.T) {
	tests := []struct {
		zm       zipMetaInfo
		wholeRef blob.Ref
		offset   uint64
	}{
		{
			zm: zipMetaInfo{
				zipSize:   16738962,
				wholeSize: 139639864,
				dataSize:  16659276,
				wholeRef:  blob.MustParse("sha224-d003d3cf9784df4efe617ba319c5028fe93e5e9188cc448bf6d655b4"),
			},
			offset: 0,
		},
		{
			zm: zipMetaInfo{
				zipSize:   16739170,
				wholeSize: 139639864,
				dataSize:  16670204,
				wholeRef:  blob.MustParse("sha224-d003d3cf9784df4efe617ba319c5028fe93e5e9188cc448bf6d655b4"),
			},
			offset: 16659276,
		},
		{
			zm: zipMetaInfo{
				zipSize:   16744577,
				wholeSize: 139639864,
				dataSize:  16668625,
				wholeRef:  blob.MustParse("sha224-d003d3cf9784df4efe617ba319c5028fe93e5e9188cc448bf6d655b4"),
			},
			offset: 33329480,
		},
		{
			zm: zipMetaInfo{
				zipSize:   16628223,
				wholeSize: 139639864,
				dataSize:  16555478,
				wholeRef:  blob.MustParse("sha224-d003d3cf9784df4efe617ba319c5028fe93e5e9188cc448bf6d655b4"),
			},
			offset: 49998105,
		},
		{
			zm: zipMetaInfo{
				zipSize:   16735901,
				wholeSize: 139639864,
				dataSize:  16661990,
				wholeRef:  blob.MustParse("sha224-d003d3cf9784df4efe617ba319c5028fe93e5e9188cc448bf6d655b4"),
			},
			offset: 66553583,
		},
		{
			zm: zipMetaInfo{
				zipSize:   16628162,
				wholeSize: 139639864,
				dataSize:  16555638,
				wholeRef:  blob.MustParse("sha224-d003d3cf9784df4efe617ba319c5028fe93e5e9188cc448bf6d655b4"),
			},
			offset: 83215573,
		},
		{
			zm: zipMetaInfo{
				zipSize:   16638400,
				wholeSize: 139639864,
				dataSize:  16569680,
				wholeRef:  blob.MustParse("sha224-d003d3cf9784df4efe617ba319c5028fe93e5e9188cc448bf6d655b4"),
			},
			offset: 99771211,
		},
		{
			zm: zipMetaInfo{
				zipSize:   16731570,
				wholeSize: 139639864,
				dataSize:  16665343,
				wholeRef:  blob.MustParse("sha224-d003d3cf9784df4efe617ba319c5028fe93e5e9188cc448bf6d655b4"),
			},
			offset: 116340891,
		},
		{
			zm: zipMetaInfo{
				zipSize:   6656201,
				wholeSize: 139639864,
				dataSize:  6633630,
				wholeRef:  blob.MustParse("sha224-d003d3cf9784df4efe617ba319c5028fe93e5e9188cc448bf6d655b4"),
			},
			offset: 133006234,
		},
	}
	for k, tt := range tests {
		rv := tt.zm.rowValue(tt.offset)
		got, err := parseZipMetaRow([]byte(rv))
		if err != nil {
			t.Fatal(err)
		}
		if tt.zm != got {
			t.Errorf("for zip %d;\n got: %#v\n want: %#v\n", k, got, tt.zm)
		}
	}
}

func TestReindex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	type file struct {
		size     int64
		name     string
		contents []byte
	}
	files := []file{
		{17 << 20, "foo.dat", randBytesSrc(17<<20, 42)},
		{10 << 20, "bar.dat", randBytesSrc(10<<20, 43)},
		{5 << 20, "baz.dat", randBytesSrc(5<<20, 44)},
	}

	pt := testPack(t,
		func(sto blobserver.Storage) error {
			for _, f := range files {
				if _, err := schema.WriteFileFromReader(ctxbg, sto, f.name, bytes.NewReader(f.contents)); err != nil {
					return err
				}
			}
			return nil
		},
		wantNumLargeBlobs(4),
		wantNumSmallBlobs(0),
	)

	// backup the meta that is supposed to be lost/erased.
	// pt.sto.reindex allocates a new pt.sto.meta, so meta != pt.sto.meta after it is called.
	meta := pt.sto.meta

	// and build new meta index
	if err := pt.sto.reindex(context.TODO(), func() (sorted.KeyValue, error) {
		return sorted.NewMemoryKeyValue(), nil
	}); err != nil {
		t.Fatal(err)
	}

	validBlobKey := func(key, value string) error {
		if !strings.HasPrefix(key, "b:") {
			return errors.New("not a blob meta key")
		}
		wantRef, ok := blob.Parse(key[2:])
		if !ok {
			return errors.New("bogus blobref in key")
		}
		m, err := parseMetaRow([]byte(value))
		if err != nil {
			return err
		}
		rc, err := pt.large.SubFetch(ctxbg, m.largeRef, int64(m.largeOff), int64(m.size))
		if err != nil {
			return err
		}
		defer rc.Close()
		h := wantRef.Hash()
		n, err := io.Copy(h, rc)
		if err != nil {
			return err
		}

		if !wantRef.HashMatches(h) {
			return errors.New("content doesn't match")
		}
		if n != int64(m.size) {
			return errors.New("size doesn't match")
		}
		return nil
	}

	// check that new meta is identical to "lost" one
	newRows := 0
	if err := sorted.Foreach(pt.sto.meta, func(key, newValue string) error {
		oldValue, err := meta.Get(key)
		if err != nil {
			t.Fatalf("Could not get value for %v in old meta: %v", key, err)
		}
		newRows++
		// Exact match is fine.
		if oldValue == newValue {
			return nil
		}
		// If it differs, it should at least be correct. (blob metadata
		// can now point to different packed zips, depending on sorting)
		err = validBlobKey(key, newValue)
		if err == nil {
			return nil
		}
		t.Errorf("Reindexing error: for key %v: %v\n got: %q\nwant: %q", key, err, newValue, oldValue)
		return nil // keep enumerating, regardless of errors
	}); err != nil {
		t.Fatal(err)
	}

	// make sure they have the same number of entries too, to be sure that the reindexing
	// did not miss entries that the old meta had.
	oldRows := countSortedRows(t, meta)
	if oldRows != newRows {
		t.Fatalf("index number of entries mismatch: got %d entries in new index, wanted %d (as in index before reindexing)", newRows, oldRows)
	}

	// And verify we can read one of the files back out.
	hash := blob.NewHash()
	hash.Write(files[0].contents)
	pt.testOpenWholeRef(t, blob.RefFromHash(hash), files[0].size)

	// Specifically check the z: rows.
	zrows := []string{
		"z:sha224-32505a4f8af2a4f86dda5680caa759ed3c229a3d484ed37b85707a53 | 10521605 sha224-336161c04a4e2ea1ac15dd2fe81820b8a5254c7030d840b61a5deb85 10485760 0 10485760",
		"z:sha224-c778d4f89f70ec7068230fe07c172e3107fbad4d9d1664c9cc83fd67 | 5263814 sha224-440ee7ebd58d2adcf48089dfc5ba1ea00d7fb1c887b52874b1fb44a4 5242880 0 5242880",
		"z:sha224-d9063688aa83bbc6ec9747222d0499196785f662d0f02ac91e852fa1 | 1121155 sha224-f039af7fe0b27aee76e301a9b3fc92ebbd2ef64b4a4abf6115356441 17825792 16709479 1116313",
		"z:sha224-fc99c9411ac39dcdba687a24fef294ba1c4ac731df4ce090a2afea0f | 16771653 sha224-f039af7fe0b27aee76e301a9b3fc92ebbd2ef64b4a4abf6115356441 17825792 0 16709479",
	}
	it := pt.sto.meta.Find(zipMetaPrefix, zipMetaPrefixLimit)
	i := 0
	for it.Next() {
		got := it.Key() + " | " + it.Value()
		if zrows[i] != got {
			t.Errorf("for row %d;\n got: %v\n want: %v\n", i, got, zrows[i])
		}
		i++
	}
	it.Close()

	recoMode, err := pt.sto.checkLargeIntegrity()
	if err != nil {
		t.Fatal(err)
	}
	if recoMode != NoRecovery {
		t.Fatalf("recovery mode after integrity check: %v", recoMode)
	}
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
			if _, err = schema.WriteFileFromReader(ctxbg, sto, "a.txt", bytes.NewReader(fileContents)); err != nil {
				return
			}
			if _, err = schema.WriteFileFromReader(ctxbg, sto, "b.txt", bytes.NewReader(fileContents)); err != nil {
				return
			}
			return
		},
		func(pt *packTest) { pt.sto.packGate = syncutil.NewGate(1) }, // one pack at a time
		wantNumLargeBlobs(1),
		wantNumSmallBlobs(1), // just the "b.txt" file schema blob
		okayWithoutMeta("sha224-8cafef47a63c2bb4f5dd6edc656c606cab803460360989c38ee2dc57"),
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
		rc, _, err := large.Fetch(ctxbg, zipRef)
		if err != nil {
			t.Fatal(err)
		}
		zipBytes, err := io.ReadAll(rc)
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
		maniJSON, err := io.ReadAll(mfr)
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
	b1 := &test.Blob{Contents: "foo"}
	b1.MustUpload(t, small)
	wantSB := b1.SizedRef()

	// Fetch
	rc, _, err := s.Fetch(ctxbg, b1.BlobRef())
	if err != nil {
		t.Errorf("failed to Get blob: %v", err)
	} else {
		rc.Close()
	}

	// Stat.
	sb, err := blobserver.StatBlob(ctxbg, s, b1.BlobRef())
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
			_, err := schema.WriteFileFromReader(ctxbg, sto, fileName, bytes.NewReader(fileContents))
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
	if err := pt.sto.foreachZipBlob(ctxbg, zipBlob.Ref, func(bap BlobAndPos) error {
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
	if _, err := schema.WriteFileFromReader(ctxbg, sto, "foo.dat", bytes.NewReader(fileContents)); err != nil {
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
	if err := sto.deleteZipPack(ctxbg, zipBlob.Ref); err == nil {
		t.Fatalf("zip pack blob deleted but it should not have been allowed")
	}

	// Delete everything
	for len(all) > 0 {
		del := all[0].Ref
		all = all[1:]
		if err := sto.RemoveBlobs(ctx, []blob.Ref{del}); err != nil {
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
	if err := sto.deleteZipPack(ctxbg, zipBlob.Ref); err != nil {
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
	blobA := &test.Blob{Contents: string(bytesAB[:sizeAB/2])}
	blobB := &test.Blob{Contents: string(bytesAB[sizeAB/2:])}
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
		_, err := s.ReceiveBlob(ctxbg, refC, bytes.NewReader(bytesC))
		if err != nil {
			t.Fatal(err)
		}

		// Upload the file schema blob.
		m := schema.NewFileMap("foo.dat")
		m.PopulateParts(sizeAB+int64(sizeC), []schema.BytesPart{
			{
				Size:    sizeAB / 2,
				BlobRef: refA,
			},
			{
				Size:    sizeAB / 2,
				BlobRef: refB,
			},
			{
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
	rc, _, err := sto.Fetch(ctxbg, br)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	slurp, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return slurp
}
