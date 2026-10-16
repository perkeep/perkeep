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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/localdisk"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/constants"
	"perkeep.org/pkg/health"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/test"

	"go4.org/jsonconfig"
	"go4.org/syncutil"
)

const debug = false

var ctxbg = context.Background()

func TestStorage(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		s := &storage{
			small: new(test.Fetcher),
			large: new(test.Fetcher),
			meta:  sorted.NewMemoryKeyValue(),
			log:   test.NewLogger(t, "blobpacked: "),
		}
		s.init()
		return s
	})
}

func TestStorageNoSmallSubfetch(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		s := &storage{
			// We need to hide SubFetcher, to test *storage's SubFetch, as it delegates
			// to the underlying SubFetcher, if small implements that interface.
			small: hideSubFetcher(new(test.Fetcher)),
			large: new(test.Fetcher),
			meta:  sorted.NewMemoryKeyValue(),
			log:   test.NewLogger(t, "blobpacked: "),
		}
		s.init()
		return s
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

func TestParseMetaRowSizeOnly(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want uint32
		err  bool
	}{
		{in: "", err: true},
		{in: " ", err: true},
		{in: "1", err: true},
		{in: "1 ", err: true},
		{in: "-1 x", err: true},
		{in: "+1 x", err: true},
		{in: "x rest", err: true},
		{in: "1_000 rest", err: true},
		{in: "4294967296 rest", err: true},
		{in: "0 rest"},
		{in: "123 rest", want: 123},
		{in: "000123 rest", want: 123},
		{in: "4294967295 rest", want: 4294967295},
	} {
		got, err := parseMetaRowSizeOnly(tt.in)
		if (err != nil) != tt.err || got != tt.want {
			t.Errorf("parseMetaRowSizeOnly(%q) = %d, %v; want %d, error=%v", tt.in, got, err, tt.want, tt.err)
		}
	}
}

func TestParseMetaRowSizeOnlyAllocs(t *testing.T) {
	const row = "123 sha1-f1d2d2f924e986ac86fdf7b36c94bcdf32beec15 42"
	var size uint32
	var err error
	allocs := testing.AllocsPerRun(1000, func() {
		size, err = parseMetaRowSizeOnly(row)
	})
	if size != 123 || err != nil {
		t.Fatalf("parseMetaRowSizeOnly = %d, %v; want 123, nil", size, err)
	}
	if allocs != 0 {
		t.Fatalf("allocations per parse = %v; want 0", allocs)
	}
}

func wantNumLargeBlobs(want int) func(*packTest) {
	return func(pt *packTest) { pt.wantLargeBlobs = want }
}

func wantNumSmallBlobs(want int) func(*packTest) {
	return func(pt *packTest) { pt.wantSmallBlobs = want }
}

// withMaxZipBlobSize sets the packer's maximum zip size, so tests can
// span several zips with less data.
func withMaxZipBlobSize(n int) func(*packTest) {
	return func(pt *packTest) { pt.sto.forceMaxZipBlobSize = n }
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

// TestPackReceiveBlobs checks that a file is packed when its schema blob
// and all its chunks are received with one ReceiveBlobs call, with the
// schema blob first.
func TestPackReceiveBlobs(t *testing.T) {
	const fileSize = 5 << 20
	fileContents := randBytes(fileSize)
	hash := blob.NewHash()
	hash.Write(fileContents)
	wholeRef := blob.RefFromHash(hash)

	pt := testPack(t,
		func(sto blobserver.Storage) error {
			scratch := new(test.Fetcher)
			fileRef, err := schema.WriteFileFromReader(ctxbg, scratch, "foo.dat", bytes.NewReader(fileContents))
			if err != nil {
				return err
			}
			var blobs []*blob.Blob
			err = blobserver.EnumerateAll(ctxbg, scratch, func(sb blob.SizedRef) error {
				b, err := blob.FromFetcher(ctxbg, scratch, sb.Ref)
				if err != nil {
					return err
				}
				if sb.Ref == fileRef {
					blobs = append([]*blob.Blob{b}, blobs...)
				} else {
					blobs = append(blobs, b)
				}
				return nil
			})
			if err != nil {
				return err
			}
			return blobserver.ReceiveBlobs(ctxbg, sto, blobs)
		},
		wantNumLargeBlobs(1),
		wantNumSmallBlobs(0),
	)
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

// corruptingStorage is a blobserver.Storage that flips a bit in the
// contents of one blob when fetching it, after the first goodFetches
// fetches of it.
type corruptingStorage struct {
	blobserver.Storage
	bad         blob.Ref
	goodFetches int

	mu      sync.Mutex
	fetches int
}

func (s *corruptingStorage) Fetch(ctx context.Context, br blob.Ref) (io.ReadCloser, uint32, error) {
	rc, size, err := s.Storage.Fetch(ctx, br)
	if err != nil || br != s.bad {
		return rc, size, err
	}
	s.mu.Lock()
	s.fetches++
	good := s.fetches <= s.goodFetches
	s.mu.Unlock()
	if good {
		return rc, size, nil
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, 0, err
	}
	b[len(b)/2] ^= 1
	return io.NopCloser(bytes.NewReader(b)), size, nil
}

func TestPackCorruptChunk(t *testing.T) {
	for _, goodFetches := range []int{0, 1} {
		t.Run(fmt.Sprintf("goodFetches=%d", goodFetches), func(t *testing.T) {
			small, large := new(test.Fetcher), new(test.Fetcher)
			fileRef, err := schema.WriteFileFromReader(ctxbg, small, "foo.dat", bytes.NewReader(randBytes(1<<20)))
			if err != nil {
				t.Fatal(err)
			}
			fr, err := schema.NewFileReader(ctxbg, small, fileRef)
			if err != nil {
				t.Fatal(err)
			}
			var chunks []blob.Ref
			if err := fr.ForeachChunk(ctxbg, func(_ []blob.Ref, p schema.BytesPart) error {
				chunks = append(chunks, p.BlobRef)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(chunks) < 2 {
				t.Fatalf("got %d chunks; want at least 2", len(chunks))
			}
			bad := chunks[len(chunks)/2]

			s := &storage{
				small: &corruptingStorage{Storage: small, bad: bad, goodFetches: goodFetches},
				large: large,
				meta:  sorted.NewMemoryKeyValue(),
				log:   test.NewLogger(t, "blobpacked: "),
			}
			s.init()
			numSmall := small.NumBlobs()

			err = s.packFile(ctxbg, fileRef)
			if err == nil || !strings.Contains(err.Error(), bad.String()) {
				t.Fatalf("packFile error = %v; want error mentioning %v", err, bad)
			}
			if n := large.NumBlobs(); n != 0 {
				t.Errorf("large has %d blobs; want 0", n)
			}
			if n := small.NumBlobs(); n != numSmall {
				t.Errorf("small has %d blobs; want all %d still there", n, numSmall)
			}
			if n := countSortedRows(t, s.meta); n != 0 {
				t.Errorf("meta has %d rows; want 0", n)
			}
		})
	}
}

// TestPackPartialChunk tests that a file whose part uses only a prefix of
// its chunk isn't packed, since its contents aren't just its chunks.
func TestPackPartialChunk(t *testing.T) {
	small, large := new(test.Fetcher), new(test.Fetcher)
	chunk := &test.Blob{Contents: string(randBytes(1 << 10))}
	chunk.MustUpload(t, small)
	const partSize = 100
	bb := schema.NewFileMap("foo.dat")
	if err := bb.PopulateParts(partSize, []schema.BytesPart{{Size: partSize, BlobRef: chunk.BlobRef()}}); err != nil {
		t.Fatal(err)
	}
	fileJSON, err := bb.JSON()
	if err != nil {
		t.Fatal(err)
	}
	fileBlob := &test.Blob{Contents: fileJSON}
	fileBlob.MustUpload(t, small)

	s := &storage{
		small: small,
		large: large,
		meta:  sorted.NewMemoryKeyValue(),
		log:   test.NewLogger(t, "blobpacked: "),
	}
	s.init()
	err = s.packFile(ctxbg, fileBlob.BlobRef())
	if want := fmt.Sprintf("chunk %v is %d bytes; schema says %d", chunk.BlobRef(), chunk.Size(), partSize); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("packFile error = %v; want error containing %q", err, want)
	}
	if n := large.NumBlobs(); n != 0 {
		t.Errorf("large has %d blobs; want 0", n)
	}
	if n := countSortedRows(t, s.meta); n != 0 {
		t.Errorf("meta has %d rows; want 0", n)
	}
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
	// With 2 MB zips, foo.dat takes two zips, and the others one each.
	const maxZip = 2 << 20
	files := []file{
		{17 << 17, "foo.dat", randBytesSrc(17<<17, 42)},
		{10 << 17, "bar.dat", randBytesSrc(10<<17, 43)},
		{5 << 17, "baz.dat", randBytesSrc(5<<17, 44)},
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
		withMaxZipBlobSize(maxZip),
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
	//
	// The zip blobrefs and sizes aren't hard-coded, as they depend on
	// the output of compress/flate (used for the manifest), which can
	// change between Go releases. Instead, verify that each row's zip
	// size matches the actual zip blob, and compare the remaining
	// (whole file) fields against known values.
	wantWholeParts := []string{
		"sha224-12b707b3f56235cb22cab3422b138e918fe9f8a6dbbf2234c6a9d829 1310720 0 1310720",
		"sha224-1633c04802e06cee89c1f955c9e3e17cdb3ca9d8d54ffd20994815af 2228224 0 2089447",
		"sha224-1633c04802e06cee89c1f955c9e3e17cdb3ca9d8d54ffd20994815af 2228224 2089447 138777",
		"sha224-ed566fbee5425359f759de618fabc072c35071a13c84b99929318cc4 655360 0 655360",
	}
	var gotWholeParts []string
	it := pt.sto.meta.Find(zipMetaPrefix, zipMetaPrefixLimit)
	for it.Next() {
		zipRef, ok := blob.Parse(strings.TrimPrefix(it.Key(), zipMetaPrefix))
		if !ok {
			t.Errorf("bogus z: row key %q", it.Key())
			continue
		}
		zipSize, wholePart, ok := strings.Cut(it.Value(), " ")
		if !ok {
			t.Errorf("bogus z: row value %q", it.Value())
			continue
		}
		sb, err := blobserver.StatBlob(ctxbg, pt.large, zipRef)
		if err != nil {
			t.Errorf("stat of zip %v: %v", zipRef, err)
		} else if fmt.Sprint(sb.Size) != zipSize {
			t.Errorf("z: row for %v has size %s; actual zip size is %d", zipRef, zipSize, sb.Size)
		}
		gotWholeParts = append(gotWholeParts, wholePart)
	}
	it.Close()
	sort.Strings(gotWholeParts)
	if !slices.Equal(gotWholeParts, wantWholeParts) {
		t.Errorf("z: rows whole file parts mismatch;\n got: %q\nwant: %q", gotWholeParts, wantWholeParts)
	}

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

	wantLargeBlobs any // nil means disabled, else int
	wantSmallBlobs any // nil means disabled, else int

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
		if errors.Is(err, sorted.ErrNotFound) && pt.okayNoMeta[sb.Ref] {
			return nil
		}
		if err != nil {
			return fmt.Errorf("error looking up logical blob %v in meta: %w", sb.Ref, err)
		}
		m, err := parseMetaRow([]byte(v))
		if err != nil {
			return fmt.Errorf("error parsing logical blob %v meta %q: %w", sb.Ref, v, err)
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
	ctx := t.Context()
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
	buf := make([]byte, 1<<20)
	var n int
	// Give goroutines that are finishing up up to 5 seconds to exit.
	for range 500 {
		buf = buf[:runtime.Stack(buf[:cap(buf)], true)]
		if n = bytes.Count(buf, []byte("[chan receive]:")); n <= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n > 1 {
		t.Errorf("%d goroutines in chan receive: %s", n, buf)
	}
}

func TestForeachZipBlob(t *testing.T) {
	const fileSize = 2 << 20
	const fileName = "foo.dat"
	fileContents := randBytes(fileSize)

	ctx := t.Context()

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
	ctx := t.Context()

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
	// Test a file of three chunk sizes, totalling near a 2 MB maximum
	// zip size (scaled down from the default 16 MB, as the binary
	// search below packs the file ~20 times):
	//    - 1st chunk is 768 KB. ("blobA")
	//    - 2nd chunk is 768 KB. ("blobB")
	//    - 3rd chunk ("blobC") is binary-searched (up to 512 KB) to find
	//      which size causes the packer to write two zip files.

	// During the test we set zip overhead boundaries to 0, to
	// force the test to into its pathological misprediction code paths,
	// where it needs to back up and rewrite the zip with one part less.
	// That's why the test starts with two zip files: so there's at
	// least one that can be removed to make room.
	defer setIntTemporarily(&zipPerEntryOverhead, 0)()

	const sizeAB = 3 << 19
	const maxBlobSize = 2 << 20
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
			forceMaxZipBlobSize: maxBlobSize,
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
	t.Logf("After 1.5 MB of data (in 2 chunks), the smallest blob that generates two zip files is %d bytes (%.03f MB)", smallestC, float64(smallestC)/(1<<20))
	t.Logf("Zip overhead (for this two chunk file) = %d bytes", maxBlobSize-1-smallestC-sizeAB)

	mu.Lock()
	if sawTruncate != refB {
		t.Errorf("truncate after = %v; want %v", sawTruncate, refB)
	}
	if !stoppedBeforeOverflow {
		t.Error("never hit the code path where it calculates that another data chunk would push it over the max zip size")
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

func TestCheckLargeIntegrity(t *testing.T) {
	newLargeFuncs := map[string]func(t *testing.T) subFetcherStorage{
		"fetcher": func(t *testing.T) subFetcherStorage { return new(test.Fetcher) },
		// localdisk implements blobserver.RefLister.
		"localdisk": func(t *testing.T) subFetcherStorage {
			sto, err := localdisk.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return sto
		},
	}
	wholeRef := blob.RefFromString("whole")
	tests := []struct {
		name    string
		inLarge []string          // contents of zips in large storage
		rows    map[string]string // z: rows to add, by zip contents; "" means a valid row
		// packed, if non-empty, is the contents of a zip that's packed
		// while the check pauses before looking again at mismatches:
		// written to large, and given a valid z: row.
		packed   string
		wantMode RecoveryMode
		wantErr  string
	}{
		{
			name:     "ok",
			inLarge:  []string{"a", "b", "c"},
			rows:     map[string]string{"a": "", "b": "", "c": ""},
			wantMode: NoRecovery,
		},
		{
			name:     "empty",
			wantMode: NoRecovery,
		},
		{
			name:     "missing_from_meta",
			inLarge:  []string{"a", "b", "c"},
			rows:     map[string]string{"b": ""},
			wantMode: FastRecovery,
			wantErr:  "2 large blobs missing from index",
		},
		{
			name:     "missing_from_large",
			inLarge:  []string{"a"},
			rows:     map[string]string{"a": "", "b": "", "c": ""},
			wantMode: FullRecovery,
			wantErr:  "2 large blobs in index but not actually in storage",
		},
		{
			// The zip was in large, but its z: row wasn't yet
			// written when meta was read.
			name:     "zip_packed_during_check",
			inLarge:  []string{"a", "b"},
			rows:     map[string]string{"a": ""},
			packed:   "b",
			wantMode: NoRecovery,
		},
		{
			// The z: row was written after large was listed.
			name:     "row_packed_during_check",
			inLarge:  []string{"a"},
			rows:     map[string]string{"a": "", "b": ""},
			packed:   "b",
			wantMode: NoRecovery,
		},
		{
			name:     "bad_row",
			inLarge:  []string{"a"},
			rows:     map[string]string{"a": "garbage"},
			wantMode: FullRecovery,
			wantErr:  "error parsing row from meta",
		},
	}
	for largeName, newLarge := range newLargeFuncs {
		for _, tt := range tests {
			t.Run(largeName+"/"+tt.name, func(t *testing.T) {
				large := newLarge(t)
				for _, s := range tt.inLarge {
					(&test.Blob{Contents: s}).MustUpload(t, large)
				}
				meta := sorted.NewMemoryKeyValue()
				setRow := func(s, row string) {
					t.Helper()
					zip := &test.Blob{Contents: s}
					if row == "" {
						row = fmt.Sprintf("%d %v 100 0 %d", zip.Size(), wholeRef, zip.Size())
					}
					if err := meta.Set(zipMetaPrefix+zip.BlobRef().String(), row); err != nil {
						t.Fatal(err)
					}
				}
				for s, row := range tt.rows {
					setRow(s, row)
				}
				oldWait := largeIntegrityRecheckWait
				t.Cleanup(func() { largeIntegrityRecheckWait = oldWait })
				largeIntegrityRecheckWait = func() {
					if tt.packed != "" {
						(&test.Blob{Contents: tt.packed}).MustUpload(t, large)
						setRow(tt.packed, "")
					}
				}
				s := &storage{large: large, meta: meta, log: test.NewLogger(t, "blobpacked: ")}
				mode, err := s.checkLargeIntegrity()
				if mode != tt.wantMode {
					t.Errorf("mode = %v; want %v", mode, tt.wantMode)
				}
				switch {
				case tt.wantErr == "" && err != nil:
					t.Errorf("unexpected error: %v", err)
				case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
					t.Errorf("error = %v; want one containing %q", err, tt.wantErr)
				}
			})
		}
	}
}

// Tests that reindex recovers the zips of a file whose middle zip is lost,
// finding their offsets in the file from the schema blobs they contain.
func TestReindexMissingPart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
	tests := []struct {
		name     string
		contents []byte
		// exactOffsets is whether the offsets of the zips after the
		// lost one are unambiguous. Files that repeat their chunks,
		// like all zeros, can match in more than one place.
		exactOffsets bool
	}{
		{"random", randBytesSrc(7<<20, 45), true},
		{"zeros", make([]byte, 7<<20), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			large := new(test.Fetcher)
			s := &storage{
				small:               new(test.Fetcher),
				large:               large,
				meta:                sorted.NewMemoryKeyValue(),
				log:                 test.NewLogger(t, "blobpacked: ", "Packing file ", "Packed file "),
				forceMaxZipBlobSize: 2 << 20,
			}
			s.init()
			if _, err := schema.WriteFileFromReader(ctxbg, s, "foo.dat", bytes.NewReader(tt.contents)); err != nil {
				t.Fatal(err)
			}
			if n := large.NumBlobs(); n < 3 {
				t.Fatalf("packed into %d zips; want at least 3", n)
			}
			wholeRef := blob.RefFromBytes(tt.contents)
			oldMeta := s.meta

			v, err := oldMeta.Get(fmt.Sprintf("%s%v:1", wholeMetaPrefix, wholeRef))
			if err != nil {
				t.Fatal(err)
			}
			lostRef, ok := blob.Parse(strings.Fields(v)[0])
			if !ok {
				t.Fatalf("bogus w: row %q", v)
			}
			if err := large.RemoveBlobs(ctxbg, []blob.Ref{lostRef}); err != nil {
				t.Fatal(err)
			}

			if err := s.reindex(ctxbg, func() (sorted.KeyValue, error) {
				return sorted.NewMemoryKeyValue(), nil
			}); err != nil {
				t.Fatal(err)
			}

			if _, err := s.meta.Get(wholeMetaPrefix + wholeRef.String()); !errors.Is(err, sorted.ErrNotFound) {
				t.Errorf("incomplete file has a w: row; err = %v", err)
			}
			if err := blobserver.EnumerateAll(ctxbg, large, func(sb blob.SizedRef) error {
				key := zipMetaPrefix + sb.Ref.String()
				got, err := s.meta.Get(key)
				if err != nil {
					return fmt.Errorf("%s: %w", key, err)
				}
				want, err := oldMeta.Get(key)
				if err != nil {
					return fmt.Errorf("%s: %w", key, err)
				}
				if tt.exactOffsets {
					if got != want {
						t.Errorf("row %s = %q; want %q", key, got, want)
					}
					return nil
				}
				// Only the offset in the whole file may differ, and
				// the data must still be within the file.
				gotf, wantf := strings.Fields(got), strings.Fields(want)
				if len(gotf) != 5 || !slices.Equal(gotf[:3], wantf[:3]) || gotf[4] != wantf[4] {
					t.Errorf("row %s = %q; want like %q", key, got, want)
					return nil
				}
				offset, _ := strconv.ParseInt(gotf[3], 10, 64)
				dataSize, _ := strconv.ParseInt(gotf[4], 10, 64)
				if offset+dataSize > int64(len(tt.contents)) {
					t.Errorf("row %s = %q: data goes past the end of the file", key, got)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			// Each blob that was in a zip that wasn't lost can still be read.
			if err := sorted.ForeachInRange(oldMeta, blobMetaPrefix, blobMetaPrefixLimit, func(key, value string) error {
				m, err := parseMetaRow([]byte(value))
				if err != nil {
					return err
				}
				if m.largeRef == lostRef {
					return nil
				}
				br, _ := blob.Parse(strings.TrimPrefix(key, blobMetaPrefix))
				b, err := blob.FromFetcher(ctxbg, s, br)
				if err != nil {
					return fmt.Errorf("fetching %v: %w", br, err)
				}
				return b.ValidContents(ctxbg)
			}); err != nil {
				t.Fatal(err)
			}

			if mode, err := s.checkLargeIntegrity(); err != nil || mode != NoRecovery {
				t.Errorf("checkLargeIntegrity = %v, %v; want %v, nil", mode, err, NoRecovery)
			}
		})
	}
}

// rezip returns a copy of the zip zipBytes in which edit has changed the
// contents of each file, or dropped it if edit returns false.
func rezip(t *testing.T, zipBytes []byte, edit func(name string, contents []byte) ([]byte, bool)) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		contents, keep := edit(f.Name, contents)
		if !keep {
			continue
		}
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: f.Method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// failingLarge is large storage whose reads of one blob fail.
type failingLarge struct {
	*test.Fetcher
	bad blob.Ref
}

var errFailingLarge = errors.New("failingLarge: read failed")

func (s failingLarge) SubFetch(ctx context.Context, br blob.Ref, offset, length int64) (io.ReadCloser, error) {
	if br == s.bad {
		return nil, errFailingLarge
	}
	return s.Fetcher.SubFetch(ctx, br, offset, length)
}

// Tests that reindex skips zips whose contents are corrupt, recording them
// so that checkLargeIntegrity doesn't count them as missing from the
// index, but stops on errors reading a zip.
func TestReindexCorruptZip(t *testing.T) {
	large := new(test.Fetcher)
	s := &storage{
		small:               new(test.Fetcher),
		large:               large,
		meta:                sorted.NewMemoryKeyValue(),
		log:                 test.NewLogger(t, "blobpacked: ", "Packing file ", "Packed file "),
		forceMaxZipBlobSize: 2 << 20,
	}
	s.init()

	// A file in one zip, which stays good.
	goodContents := randBytesSrc(1<<20, 49)
	if _, err := schema.WriteFileFromReader(ctxbg, s, "good.dat", bytes.NewReader(goodContents)); err != nil {
		t.Fatal(err)
	}
	// A file in several zips.
	multiContents := randBytesSrc(5<<20, 50)
	if _, err := schema.WriteFileFromReader(ctxbg, s, "multi.dat", bytes.NewReader(multiContents)); err != nil {
		t.Fatal(err)
	}
	multiRef := blob.RefFromBytes(multiContents)
	partZip := func(i int) (blob.Ref, []byte) {
		v, err := s.meta.Get(fmt.Sprintf("%s%v:%d", wholeMetaPrefix, multiRef, i))
		if err != nil {
			t.Fatal(err)
		}
		zipRef, _ := blob.Parse(strings.Fields(v)[0])
		return zipRef, slurpBlob(t, large, zipRef)
	}
	part0Ref, part0 := partZip(0)
	part1Ref, _ := partZip(1)
	part2Ref, part2 := partZip(2)

	add := func(contents []byte) blob.Ref {
		b := &test.Blob{Contents: string(contents)}
		large.AddBlob(b)
		return b.BlobRef()
	}
	wantCorrupt := []blob.Ref{
		add([]byte("not a zip")),
		add(part0[:len(part0)/2]),
		add(rezip(t, part0, func(name string, contents []byte) ([]byte, bool) {
			return contents, name != zipManifestPath
		})),
		add(rezip(t, part0, func(name string, contents []byte) ([]byte, bool) {
			if name == zipManifestPath {
				return []byte("{"), true
			}
			return contents, true
		})),
		add(rezip(t, part0, func(name string, contents []byte) ([]byte, bool) {
			if name != zipManifestPath {
				return contents, true
			}
			var mf Manifest
			if err := json.Unmarshal(contents, &mf); err != nil {
				t.Fatal(err)
			}
			mf.DataBlobs[0].Offset = 1 << 30
			contents, err := json.Marshal(mf)
			if err != nil {
				t.Fatal(err)
			}
			return contents, true
		})),
	}
	// Lose the second zip of the multi-zip file, and the schema blobs of
	// the third, so its place in the file can't be found.
	if err := large.RemoveBlobs(ctxbg, []blob.Ref{part1Ref, part2Ref}); err != nil {
		t.Fatal(err)
	}
	wantCorrupt = append(wantCorrupt, add(rezip(t, part2, func(name string, contents []byte) ([]byte, bool) {
		return contents, name == zipManifestPath || !strings.HasPrefix(name, "camlistore/")
	})))

	if err := s.reindex(ctxbg, func() (sorted.KeyValue, error) {
		return sorted.NewMemoryKeyValue(), nil
	}); err != nil {
		t.Fatal(err)
	}

	var gotCorrupt []blob.Ref
	if err := sorted.ForeachInRange(s.meta, corruptMetaPrefix, corruptMetaPrefixLimit, func(key, _ string) error {
		zipRef, _ := blob.Parse(strings.TrimPrefix(key, corruptMetaPrefix))
		gotCorrupt = append(gotCorrupt, zipRef)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sortRefs := func(refs []blob.Ref) {
		sort.Slice(refs, func(i, j int) bool { return refs[i].Less(refs[j]) })
	}
	sortRefs(gotCorrupt)
	sortRefs(wantCorrupt)
	if !slices.Equal(gotCorrupt, wantCorrupt) {
		t.Errorf("c: rows for %v; want %v", gotCorrupt, wantCorrupt)
	}

	rc, _, err := s.OpenWholeRef(blob.RefFromBytes(goodContents), 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || !bytes.Equal(got, goodContents) {
		t.Errorf("reading good file after reindex: %d bytes, err %v", len(got), err)
	}
	if _, err := s.meta.Get(zipMetaPrefix + part0Ref.String()); err != nil {
		t.Errorf("multi-zip file's first zip: %v", err)
	}

	if mode, err := s.checkLargeIntegrity(); err != nil || mode != NoRecovery {
		t.Errorf("checkLargeIntegrity = %v, %v; want %v, nil", mode, err, NoRecovery)
	}

	// A zip that can't be read, rather than one that's corrupt, stops reindex.
	s.large = failingLarge{large, part0Ref}
	if err := s.reindex(ctxbg, func() (sorted.KeyValue, error) {
		return sorted.NewMemoryKeyValue(), nil
	}); !errors.Is(err, errFailingLarge) {
		t.Errorf("reindex with a failing read = %v; want %v", err, errFailingLarge)
	}
}

// Tests that when the packer underestimates the size of a two-chunk zip,
// it writes the first chunk in one zip and the second in another, even
// when both chunks are the same blob.
func TestPackerTruncateTwoChunks(t *testing.T) {
	// Zero per-entry overhead forces the underestimate. The fixed overhead
	// is left alone, so the packer's estimate is never zero.
	defer setIntTemporarily(&zipPerEntryOverhead, 0)()

	const chunkSize = 600 << 10
	tests := []struct {
		name   string
		chunk0 []byte
		chunk1 []byte // or nil for the same as chunk0
	}{
		{"distinct", randBytesSrc(chunkSize, 46), randBytesSrc(chunkSize, 47)},
		{"same", randBytesSrc(chunkSize, 48), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.chunk1 == nil {
				tt.chunk1 = tt.chunk0
			}
			b0 := &test.Blob{Contents: string(tt.chunk0)}
			b1 := &test.Blob{Contents: string(tt.chunk1)}
			whole := append(slices.Clone(tt.chunk0), tt.chunk1...)
			wholeRef := blob.RefFromBytes(whole)

			var truncs []blob.Ref
			testHookSawTruncate = func(after blob.Ref) {
				truncs = append(truncs, after)
				if len(truncs) > 1 {
					t.Fatalf("truncated twice packing one file, after %v", truncs)
				}
			}
			defer func() { testHookSawTruncate = nil }()

			// pack packs the file with zips of at most maxZip bytes.
			pack := func(maxZip int) (s *storage, small, large *test.Fetcher) {
				truncs = nil
				small, large = new(test.Fetcher), new(test.Fetcher)
				s = &storage{
					small:               small,
					large:               large,
					meta:                sorted.NewMemoryKeyValue(),
					log:                 test.NewLogger(t, "blobpacked: ", "Packing file ", "Packed file "),
					forceMaxZipBlobSize: maxZip,
				}
				s.init()
				b0.MustUpload(t, s)
				b1.MustUpload(t, s)
				m := schema.NewFileMap("foo.dat")
				m.PopulateParts(int64(len(whole)), []schema.BytesPart{
					{Size: uint64(len(tt.chunk0)), BlobRef: b0.BlobRef()},
					{Size: uint64(len(tt.chunk1)), BlobRef: b1.BlobRef()},
				})
				fjson, err := m.JSON()
				if err != nil {
					t.Fatal(err)
				}
				(&test.Blob{Contents: fjson}).MustUpload(t, s)
				return s, small, large
			}

			// Find the smallest zip size limit that fits the whole file
			// in one zip. One byte less, the packer still estimates that
			// both chunks fit, and has to back up.
			lo := chunkSize + 4<<10 // room for one chunk and the overhead
			n := sort.Search(chunkSize+8<<10, func(i int) bool {
				_, _, large := pack(lo + i)
				return large.NumBlobs() == 1
			})
			oneZipMax := lo + n
			if _, _, large := pack(oneZipMax); large.NumBlobs() != 1 {
				t.Fatalf("never packed into one zip")
			}

			s, small, large := pack(oneZipMax - 1)
			if !slices.Equal(truncs, []blob.Ref{b0.BlobRef()}) {
				t.Errorf("truncated after %v; want once, after %v", truncs, b0.BlobRef())
			}
			if got := large.NumBlobs(); got != 2 {
				t.Fatalf("packed into %d zips; want 2", got)
			}
			if got := small.NumBlobs(); got != 0 {
				t.Errorf("%d blobs left in small; want 0", got)
			}
			rc, size, err := s.OpenWholeRef(wholeRef, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			got, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}
			if size != int64(len(whole)) || !bytes.Equal(got, whole) {
				t.Errorf("OpenWholeRef read %d bytes (size %d) that don't match the file", len(got), size)
			}
		})
	}
}

// TestNewFromConfigHealth checks that newFromConfig reports the check of
// large storage against meta to the health monitor: a mismatch makes the
// server read-only, unless keepGoing is set.
func TestNewFromConfigHealth(t *testing.T) {
	tests := []struct {
		name         string
		keepGoing    bool
		largeHasZip  bool // with no meta rows, which is a mismatch
		wantState    health.State
		wantReadOnly bool
	}{
		{name: "ok", wantState: health.StateOK},
		{name: "zips_without_meta", largeHasZip: true, wantState: health.StateFailed, wantReadOnly: true},
		{name: "zips_without_meta_keepgoing", keepGoing: true, largeHasZip: true, wantState: health.StateFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mon := health.New()
				ld := &test.Loader{HealthMonitor: mon}
				large := new(test.Fetcher)
				if tt.largeHasZip {
					(&test.Blob{Contents: "a zip"}).MustUpload(t, large)
				}
				ld.SetStorage("/small/", new(test.Fetcher))
				ld.SetStorage("/large/", large)
				sto, err := newFromConfig(ld, jsonconfig.Obj{
					"smallBlobs": "/small/",
					"largeBlobs": "/large/",
					"metaIndex":  map[string]any{"type": "memory"},
					"keepGoing":  tt.keepGoing,
				})
				if err != nil {
					t.Fatal(err)
				}
				defer sto.(*storage).Close()
				synctest.Wait() // for the check in the background

				st := mon.Status()
				if len(st.Checks) != 1 || st.Checks[0].ID != "storage.blobpacked.large-integrity[lies]" {
					t.Fatalf("checks = %+v; want one, storage.blobpacked.large-integrity[lies]", st.Checks)
				}
				if c := st.Checks[0]; c.State != tt.wantState {
					t.Errorf("state = %v (%q); want %v", c.State, c.Message, tt.wantState)
				}
				if st.ReadOnly != tt.wantReadOnly {
					t.Errorf("read-only = %v; want %v", st.ReadOnly, tt.wantReadOnly)
				}
			})
		})
	}
}
