/*
Copyright 2011 Google Inc.

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

package schema

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/test"
)

var testFetcher = &test.Fetcher{}

var blobA = &test.Blob{"AAAAAaaaaa"}
var blobB = &test.Blob{"BBBBBbbbbb"}
var blobC = &test.Blob{"CCCCCccccc"}

func init() {
	testFetcher.AddBlob(blobA)
	testFetcher.AddBlob(blobB)
	testFetcher.AddBlob(blobC)
}

type readTest struct {
	parts    []*BytesPart
	skip     uint64
	expected string
}

func part(blob *test.Blob, offset, size uint64) *BytesPart {
	return &BytesPart{BlobRef: blob.BlobRef(), Size: size, Offset: offset}
}

// filePart returns a BytesPart that references a file JSON schema
// blob made of the provided content parts.
func filePart(cps []*BytesPart, skip uint64) *BytesPart {
	m := newBytes()
	fileSize := int64(0)
	cpl := []BytesPart{}
	for _, cp := range cps {
		fileSize += int64(cp.Size)
		cpl = append(cpl, *cp)
	}
	err := m.PopulateParts(fileSize, cpl)
	if err != nil {
		panic(err)
	}
	json, err := m.JSON()
	if err != nil {
		panic(err)
	}
	tb := &test.Blob{json}
	testFetcher.AddBlob(tb)
	return &BytesPart{BytesRef: tb.BlobRef(), Size: uint64(fileSize) - skip, Offset: skip}
}

func all(blob *test.Blob) *BytesPart {
	return part(blob, 0, uint64(blob.Size()))
}

func zero(size uint64) *BytesPart {
	return &BytesPart{Size: size}
}

func parts(parts ...*BytesPart) []*BytesPart {
	return parts
}

func sizeSum(parts []*BytesPart) (s uint64) {
	for _, p := range parts {
		s += uint64(p.Size)
	}
	return
}

var readTests = []readTest{
	{parts(all(blobA)), 0, "AAAAAaaaaa"},
	{parts(all(blobA)), 2, "AAAaaaaa"},
	{parts(part(blobA, 0, 5)), 0, "AAAAA"},
	{parts(part(blobA, 2, 8)), 0, "AAAaaaaa"},
	{parts(part(blobA, 2, 8)), 1, "AAaaaaa"},
	{parts(part(blobA, 4, 6)), 0, "Aaaaaa"},
	{parts(all(blobA), all(blobB)), 0, "AAAAAaaaaaBBBBBbbbbb"},
	{parts(all(blobA), all(blobB)), 1, "AAAAaaaaaBBBBBbbbbb"},
	{parts(all(blobA), all(blobB)), 10, "BBBBBbbbbb"},
	{parts(all(blobA), all(blobB)), 11, "BBBBbbbbb"},
	{parts(all(blobA), all(blobB)), 100, ""},
	{parts(all(blobA), all(blobB), all(blobC)), 0, "AAAAAaaaaaBBBBBbbbbbCCCCCccccc"},
	{parts(all(blobA), all(blobB), all(blobC)), 20, "CCCCCccccc"},
	{parts(all(blobA), all(blobB), all(blobC)), 22, "CCCccccc"},
	{parts(part(blobA, 5, 5), part(blobB, 0, 5), part(blobC, 4, 2)), 1, "aaaaBBBBBCc"},
	{parts(all(blobA), zero(2), all(blobB)), 5, "aaaaa\x00\x00BBBBBbbbbb"},
	{parts(all(blobB), part(blobC, 4, 2)), 0, "BBBBBbbbbbCc"},
	{parts(
		all(blobA),
		filePart(parts(all(blobB), part(blobC, 4, 2)), 0),
		part(blobA, 5, 5)),
		1,
		"AAAAaaaaa" + "BBBBBbbbbb" + "Cc" + "aaaaa"},
	{parts(
		all(blobA),
		filePart(parts(all(blobB), part(blobC, 4, 2)), 4),
		part(blobA, 5, 5)),
		1,
		"AAAAaaaaa" + "Bbbbbb" + "Cc" + "aaaaa"},
}

func skipBytes(fr *FileReader, skipBytes uint64) uint64 {
	oldOff, err := fr.Seek(0, os.SEEK_CUR)
	if err != nil {
		panic("Failed to seek")
	}
	remain := fr.size - oldOff
	if int64(skipBytes) > remain {
		skipBytes = uint64(remain)
	}
	newOff, err := fr.Seek(int64(skipBytes), os.SEEK_CUR)
	if err != nil {
		panic("Failed to seek")
	}
	skipped := newOff - oldOff
	if skipped < 0 {
		panic("")
	}
	return uint64(skipped)
}

func TestReader(t *testing.T) {
	for idx, rt := range readTests {
		ss := new(superset)
		ss.Type = "file"
		ss.Version = 1
		ss.Parts = rt.parts
		fr, err := ss.NewFileReader(testFetcher)
		if err != nil {
			t.Errorf("read error on test %d: %v", idx, err)
			continue
		}
		skipBytes(fr, rt.skip)
		all, err := ioutil.ReadAll(fr)
		if err != nil {
			t.Errorf("read error on test %d: %v", idx, err)
			continue
		}
		if g, e := string(all), rt.expected; e != g {
			t.Errorf("test %d\nwant %q\n got %q", idx, e, g)
		}
	}
}

func TestReaderSeekStress(t *testing.T) {
	const fileSize = 750<<10 + 123
	bigFile := make([]byte, fileSize)
	rnd := rand.New(rand.NewSource(1))
	for i := range bigFile {
		bigFile[i] = byte(rnd.Intn(256))
	}

	sto := new(test.Fetcher) // in-memory blob storage
	fileMap := NewFileMap("testfile")
	fileref, err := WriteFileMap(sto, fileMap, bytes.NewReader(bigFile))
	if err != nil {
		t.Fatalf("WriteFileMap: %v", err)
	}
	c, ok := sto.BlobContents(fileref)
	if !ok {
		t.Fatal("expected file contents to be present")
	}
	const debug = false
	if debug {
		t.Logf("Fileref %s: %s", fileref, c)
	}

	// Test a bunch of reads at different offsets, making sure we always
	// get the same results.
	skipBy := int64(999)
	if testing.Short() {
		skipBy += 10 << 10
	}
	for off := int64(0); off < fileSize; off += skipBy {
		fr, err := NewFileReader(sto, fileref)
		if err != nil {
			t.Fatal(err)
		}

		skipBytes(fr, uint64(off))
		got, err := ioutil.ReadAll(fr)
		if err != nil {
			t.Fatal(err)
		}
		want := bigFile[off:]
		if !bytes.Equal(got, want) {
			t.Errorf("Incorrect read at offset %d:\n  got: %s\n want: %s", off, summary(got), summary(want))
			off := 0
			for len(got) > 0 && len(want) > 0 && got[0] == want[0] {
				off++
				got = got[1:]
				want = want[1:]
			}
			t.Errorf("  differences start at offset %d:\n    got: %s\n   want: %s\n", off, summary(got), summary(want))
			break
		}
		fr.Close()
	}
}

/*

1KB ReadAt calls before:
fileread_test.go:253: Blob Size: 4194304 raw, 4201523 with meta (1.00172x)
fileread_test.go:283: Blobs fetched: 4160 (63.03x)
fileread_test.go:284: Bytes fetched: 361174780 (85.96x)

2KB ReadAt calls before:
fileread_test.go:253: Blob Size: 4194304 raw, 4201523 with meta (1.00172x)
fileread_test.go:283: Blobs fetched: 2112 (32.00x)
fileread_test.go:284: Bytes fetched: 182535389 (43.45x)

After fix:
fileread_test.go:253: Blob Size: 4194304 raw, 4201523 with meta (1.00172x)
fileread_test.go:283: Blobs fetched: 66 (1.00x)
fileread_test.go:284: Bytes fetched: 4201523 (1.00x)
*/
func TestReaderEfficiency(t *testing.T) {
	const fileSize = 4 << 20
	bigFile := make([]byte, fileSize)
	rnd := rand.New(rand.NewSource(1))
	for i := range bigFile {
		bigFile[i] = byte(rnd.Intn(256))
	}

	sto := new(test.Fetcher) // in-memory blob storage
	fileMap := NewFileMap("testfile")
	fileref, err := WriteFileMap(sto, fileMap, bytes.NewReader(bigFile))
	if err != nil {
		t.Fatalf("WriteFileMap: %v", err)
	}

	fr, err := NewFileReader(sto, fileref)
	if err != nil {
		t.Fatal(err)
	}

	numBlobs := sto.NumBlobs()
	t.Logf("Num blobs = %d", numBlobs)
	sumSize := sto.SumBlobSize()
	t.Logf("Blob Size: %d raw, %d with meta (%.05fx)", fileSize, sumSize, float64(sumSize)/float64(fileSize))

	const readSize = 2 << 10
	buf := make([]byte, readSize)
	for off := int64(0); off < fileSize; off += readSize {
		n, err := fr.ReadAt(buf, off)
		if err != nil {
			t.Fatalf("ReadAt at offset %d: %v", off, err)
		}
		if n != readSize {
			t.Fatalf("Read %d bytes at offset %d; want %d", n, off, readSize)
		}
		got, want := buf, bigFile[off:off+readSize]
		if !bytes.Equal(buf, want) {
			t.Errorf("Incorrect read at offset %d:\n  got: %s\n want: %s", off, summary(got), summary(want))
			off := 0
			for len(got) > 0 && len(want) > 0 && got[0] == want[0] {
				off++
				got = got[1:]
				want = want[1:]
			}
			t.Errorf("  differences start at offset %d:\n    got: %s\n   want: %s\n", off, summary(got), summary(want))
			break
		}
	}
	fr.Close()
	blobsFetched, bytesFetched := sto.Stats()
	if blobsFetched != int64(numBlobs) {
		t.Errorf("Fetched %d blobs; want %d", blobsFetched, numBlobs)
	}
	if bytesFetched != sumSize {
		t.Errorf("Fetched %d bytes; want %d", bytesFetched, sumSize)
	}
}

func TestReaderForeachChunk(t *testing.T) {
	fileSize := 4 << 20
	if testing.Short() {
		fileSize = 1 << 20
	}
	bigFile := make([]byte, fileSize)
	rnd := rand.New(rand.NewSource(1))
	for i := range bigFile {
		bigFile[i] = byte(rnd.Intn(256))
	}
	sto := new(test.Fetcher) // in-memory blob storage
	fileMap := NewFileMap("testfile")
	fileref, err := WriteFileMap(sto, fileMap, bytes.NewReader(bigFile))
	if err != nil {
		t.Fatalf("WriteFileMap: %v", err)
	}

	fr, err := NewFileReader(sto, fileref)
	if err != nil {
		t.Fatal(err)
	}

	var back bytes.Buffer
	var totSize uint64
	err = fr.ForeachChunk(func(sref []blob.Ref, p BytesPart) error {
		if len(sref) < 1 {
			t.Fatal("expected at least one schemaPath blob")
		}
		for i, br := range sref {
			if !br.Valid() {
				t.Fatalf("invalid schema blob in path index %d", i)
			}
		}
		if p.BytesRef.Valid() {
			t.Fatal("should never see a valid BytesRef")
		}
		if !p.BlobRef.Valid() {
			t.Fatal("saw part with invalid blobref")
		}
		rc, size, err := sto.Fetch(p.BlobRef)
		if err != nil {
			return fmt.Errorf("Error fetching blobref of chunk %+v: %v", p, err)
		}
		defer rc.Close()
		totSize += p.Size
		if uint64(size) != p.Size {
			return fmt.Errorf("fetched size %d doesn't match expected for chunk %+v", size, p)
		}
		n, err := io.Copy(&back, rc)
		if err != nil {
			return err
		}
		if n != int64(size) {
			return fmt.Errorf("Copied unexpected %d bytes of chunk %+v", n, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ForeachChunk = %v", err)
	}
	if back.Len() != fileSize {
		t.Fatalf("Read file is %d bytes; want %d", back.Len(), fileSize)
	}
	if totSize != uint64(fileSize) {
		t.Errorf("sum of parts = %d; want %d", totSize, fileSize)
	}
	if !bytes.Equal(back.Bytes(), bigFile) {
		t.Errorf("file read mismatch")
	}
}

func TestForeachChunkAllSchemaBlobs(t *testing.T) {
	sto := new(test.Fetcher) // in-memory blob storage
	foo := &test.Blob{"foo"}
	bar := &test.Blob{"bar"}
	sto.AddBlob(foo)
	sto.AddBlob(bar)

	// Make a "bytes" schema blob referencing the "foo" and "bar" chunks.
	// Verify it works.
	bytesBlob := &test.Blob{`{"camliVersion": 1,
"camliType": "bytes",
"parts": [
   {"blobRef": "` + foo.BlobRef().String() + `", "size": 3},
   {"blobRef": "` + bar.BlobRef().String() + `", "size": 3}
]}`}
	sto.AddBlob(bytesBlob)

	var fr *FileReader
	mustRead := func(name string, br blob.Ref, want string) {
		var err error
		fr, err = NewFileReader(sto, br)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		all, err := ioutil.ReadAll(fr)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(all) != want {
			t.Errorf("%s: read contents %q; want %q", name, all, want)
		}
	}
	mustRead("bytesBlob", bytesBlob.BlobRef(), "foobar")

	// Now make another bytes schema blob embedding the previous one.
	bytesBlob2 := &test.Blob{`{"camliVersion": 1,
"camliType": "bytes",
"parts": [
   {"bytesRef": "` + bytesBlob.BlobRef().String() + `", "size": 6}
]}`}
	sto.AddBlob(bytesBlob2)
	mustRead("bytesBlob2", bytesBlob2.BlobRef(), "foobar")

	sawSchema := map[blob.Ref]bool{}
	sawData := map[blob.Ref]bool{}
	if err := fr.ForeachChunk(func(path []blob.Ref, p BytesPart) error {
		for _, sref := range path {
			sawSchema[sref] = true
		}
		sawData[p.BlobRef] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name string
		tb   *test.Blob
		m    map[blob.Ref]bool
	}{
		{"bytesBlob", bytesBlob, sawSchema},
		{"bytesBlob2", bytesBlob2, sawSchema},
		{"foo", foo, sawData},
		{"bar", bar, sawData},
	}
	for _, tt := range want {
		if b := tt.tb.BlobRef(); !tt.m[b] {
			t.Errorf("didn't see %s (%s)", tt.name, b)
		}
	}
}

type summary []byte

func (s summary) String() string {
	const prefix = 10
	plen := prefix
	if len(s) < plen {
		plen = len(s)
	}
	return fmt.Sprintf("%d bytes, starting with %q", len(s), []byte(s[:plen]))
}
