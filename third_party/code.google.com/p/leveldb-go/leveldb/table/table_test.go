// Copyright 2011 The LevelDB-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package table

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"camlistore.org/third_party/code.google.com/p/leveldb-go/leveldb/db"
)

type memFile []byte

func (f *memFile) Close() error {
	return nil
}

func (f *memFile) ReadAt(p []byte, off int64) (int, error) {
	return copy(p, (*f)[off:]), nil
}

func (f *memFile) Stat() (os.FileInfo, error) {
	return f, nil
}

func (f *memFile) Write(p []byte) (int, error) {
	*f = append(*f, p...)
	return len(p), nil
}

func (f *memFile) Size() int64 {
	return int64(len(*f))
}

func (f *memFile) Sys() interface{} {
	return nil
}

func (f *memFile) IsDir() bool {
	return false
}

func (f *memFile) ModTime() time.Time {
	return time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC)
}

func (f *memFile) Mode() os.FileMode {
	return os.FileMode(0755)
}

func (f *memFile) Name() string {
	return "testdata"
}

var wordCount = map[string]string{}

func init() {
	f, err := os.Open("../../testdata/h.txt")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		s, err := r.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		k := strings.TrimSpace(string(s[8:]))
		v := strings.TrimSpace(string(s[:8]))
		wordCount[k] = v
	}
	if len(wordCount) != 1710 {
		panic(fmt.Sprintf("h.txt entry count: got %d, want %d", len(wordCount), 1710))
	}
}

func check(f File) error {
	r := NewReader(f, &db.Options{
		VerifyChecksums: true,
	})
	// Check that each key/value pair in wordCount is also in the table.
	for k, v := range wordCount {
		// Check using Get.
		if v1, err := r.Get([]byte(k), nil); string(v1) != string(v) || err != nil {
			return fmt.Errorf("Get %q: got (%q, %v), want (%q, %v)", k, v1, err, v, error(nil))
		}

		// Check using Find.
		i := r.Find([]byte(k), nil)
		if !i.Next() || string(i.Key()) != k {
			return fmt.Errorf("Find %q: key was not in the table", k)
		}
		if string(i.Value()) != v {
			return fmt.Errorf("Find %q: got value %q, want %q", k, i.Value(), v)
		}
		if err := i.Close(); err != nil {
			return err
		}
	}

	// Check that nonsense words are not in the table.
	var nonsenseWords = []string{
		"",
		"\x00",
		"kwyjibo",
		"\xff",
	}
	for _, s := range nonsenseWords {
		// Check using Get.
		if _, err := r.Get([]byte(s), nil); err != db.ErrNotFound {
			return fmt.Errorf("Get %q: got %v, want ErrNotFound", s, err)
		}

		// Check using Find.
		i := r.Find([]byte(s), nil)
		if i.Next() && s == string(i.Key()) {
			return fmt.Errorf("Find %q: unexpectedly found key in the table", s)
		}
		if err := i.Close(); err != nil {
			return err
		}
	}

	// Check that the number of keys >= a given start key matches the expected number.
	var countTests = []struct {
		count int
		start string
	}{
		// cat h.txt | cut -c 9- | wc -l gives 1710.
		{1710, ""},
		// cat h.txt | cut -c 9- | grep -v "^[a-b]" | wc -l gives 1522.
		{1522, "c"},
		// cat h.txt | cut -c 9- | grep -v "^[a-j]" | wc -l gives 940.
		{940, "k"},
		// cat h.txt | cut -c 9- | grep -v "^[a-x]" | wc -l gives 12.
		{12, "y"},
		// cat h.txt | cut -c 9- | grep -v "^[a-z]" | wc -l gives 0.
		{0, "~"},
	}
	for _, ct := range countTests {
		n, i := 0, r.Find([]byte(ct.start), nil)
		for i.Next() {
			n++
		}
		if err := i.Close(); err != nil {
			return err
		}
		if n != ct.count {
			return fmt.Errorf("count %q: got %d, want %d", ct.start, n, ct.count)
		}
	}

	return r.Close()
}

func build(compression db.Compression) (*memFile, error) {
	// Create a sorted list of wordCount's keys.
	keys := make([]string, len(wordCount))
	i := 0
	for k := range wordCount {
		keys[i] = k
		i++
	}
	sort.Strings(keys)

	// Write the key/value pairs to a new table, in increasing key order.
	f := new(memFile)
	w := NewWriter(f, &db.Options{
		Compression: compression,
	})
	for _, k := range keys {
		v := wordCount[k]
		if err := w.Set([]byte(k), []byte(v), nil); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return f, nil
}

func TestReader(t *testing.T) {
	// Check that we can read a pre-made table.
	f, err := os.Open("../../testdata/h.sst")
	if err != nil {
		t.Fatal(err)
	}
	err = check(f)
	if err != nil {
		t.Fatal(err)
	}
}

func TestWriter(t *testing.T) {
	// Check that we can read a freshly made table.
	f, err := build(db.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	err = check(f)
	if err != nil {
		t.Fatal(err)
	}
}

func TestNoCompressionOutput(t *testing.T) {
	// Check that a freshly made NoCompression table is byte-for-byte equal
	// to a pre-made table.
	a, err := ioutil.ReadFile("../../testdata/h.no-compression.sst")
	if err != nil {
		t.Fatal(err)
	}
	b, err := build(db.NoCompression)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, []byte(*b)) {
		t.Fatal("built table does not match pre-made table")
	}
}

func TestBlockIter(t *testing.T) {
	// k is a block that maps three keys "apple", "apricot", "banana" to empty strings.
	k := block([]byte("\x00\x05\x00apple\x02\x05\x00ricot\x00\x06\x00banana\x00\x00\x00\x00\x01\x00\x00\x00"))
	var testcases = []struct {
		index int
		key   string
	}{
		{0, ""},
		{0, "a"},
		{0, "aaaaaaaaaaaaaaa"},
		{0, "app"},
		{0, "apple"},
		{1, "appliance"},
		{1, "apricos"},
		{1, "apricot"},
		{2, "azzzzzzzzzzzzzz"},
		{2, "b"},
		{2, "banan"},
		{2, "banana"},
		{3, "banana\x00"},
		{3, "c"},
	}
	for _, tc := range testcases {
		i, err := k.seek(db.DefaultComparer, []byte(tc.key))
		if err != nil {
			t.Fatal(err)
		}
		for j, kWant := range []string{"apple", "apricot", "banana"}[tc.index:] {
			if !i.Next() {
				t.Fatalf("key=%q, index=%d, j=%d: Next got false, want true", tc.key, tc.index, j)
			}
			if kGot := string(i.Key()); kGot != kWant {
				t.Fatalf("key=%q, index=%d, j=%d: got %q, want %q", tc.key, tc.index, j, kGot, kWant)
			}
		}
		if i.Next() {
			t.Fatalf("key=%q, index=%d: Next got true, want false", tc.key, tc.index)
		}
		if err := i.Close(); err != nil {
			t.Fatalf("key=%q, index=%d: got err=%v", tc.key, tc.index, err)
		}
	}
}
