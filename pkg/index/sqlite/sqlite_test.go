/*
Copyright 2012 The Perkeep Authors.

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

package sqlite_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvtest"
	sqlitekv "perkeep.org/pkg/sorted/sqlite"
	"perkeep.org/pkg/test"

	"go4.org/jsonconfig"
	_ "modernc.org/sqlite"
)

func newSorted(t *testing.T) (kv sorted.KeyValue, clean func()) {
	f, err := os.CreateTemp("", "sqlite-test")
	if err != nil {
		t.Fatal(err)
	}

	kv, err = sorted.NewKeyValue(jsonconfig.Obj{
		"type": "sqlite",
		"file": f.Name(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return kv, func() {
		kv.Close()
		os.Remove(f.Name())
	}
}

func TestSorted_SQLite(t *testing.T) {
	kv, clean := newSorted(t)
	defer clean()
	kvtest.TestSorted(t, kv)
}

type tester struct{}

func (tester) test(t *testing.T, tfn func(*testing.T, func() *index.Index)) {
	var mu sync.Mutex // guards cleanups
	var cleanups []func()
	defer func() {
		mu.Lock() // never unlocked
		for _, fn := range cleanups {
			fn()
		}
	}()
	makeIndex := func() *index.Index {
		s, cleanup := newSorted(t)
		mu.Lock()
		cleanups = append(cleanups, cleanup)
		mu.Unlock()
		return indextest.MustNew(t, s)
	}
	tfn(t, makeIndex)
}

func TestIndex_SQLite(t *testing.T) {
	tester{}.test(t, indextest.Index)
}

func TestPathsOfSignerTarget_SQLite(t *testing.T) {
	tester{}.test(t, indextest.PathsOfSignerTarget)
}

func TestFiles_SQLite(t *testing.T) {
	tester{}.test(t, indextest.Files)
}

func TestEdgesTo_SQLite(t *testing.T) {
	tester{}.test(t, indextest.EdgesTo)
}

func TestDelete_SQLite(t *testing.T) {
	tester{}.test(t, indextest.Delete)
}

func TestReindex_SQLite(t *testing.T) {
	test.TLog(t)
	tester{}.test(t, indextest.Reindex)
}

func TestReindexManyDeletes_SQLite(t *testing.T) {
	test.TLog(t)
	tester{}.test(t, indextest.ReindexManyDeletes)
}

// Force delete-before-target enumeration with one primary worker, while keeping
// the rebuild small enough that all logical writes share an uncommitted group.
// The delete's retry must see its target's provisional meta row, then all rows
// and the rebuilt deletion cache must survive reopening the real SQLite store.
func TestReindexSQLiteDeleteBeforeTargetReopen(t *testing.T) {
	test.TLog(t)
	t.Setenv("CAMLI_REINDEX_START", "")
	t.Setenv("CAMLI_REDO_INDEX_ON_RECEIVE", "false")
	oldProcs := index.ReindexMaxProcs()
	index.SetReindexMaxProcs(1)
	defer index.SetReindexMaxProcs(oldProcs)

	path := filepath.Join(t.TempDir(), "reindex.sqlite")
	kv, err := sqlitekv.NewStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	idx := indextest.MustNew(t, kv)
	t.Cleanup(func() {
		if err := idx.Close(); err != nil {
			t.Error(err)
		}
	})
	id := indextest.NewIndexDeps(idx)
	id.Fataler = t
	var target, deletion *test.Blob
	for n := range 100 {
		target = id.Sign(schema.NewPlannedPermanode(fmt.Sprintf("sqlite-reindex-target-%d", n)))
		del := schema.NewDeleteClaim(target.BlobRef())
		del.SetClaimDate(test.ClockOrigin)
		deletion = id.Sign(del)
		if deletion.BlobRef().Less(target.BlobRef()) {
			break
		}
	}
	if !deletion.BlobRef().Less(target.BlobRef()) {
		t.Fatal("could not construct a delete sorting before its target")
	}
	id.Upload(target)
	id.SetAttribute(target.BlobRef(), "tag", "reindex-reopen")
	id.Upload(deletion)
	if !idx.IsDeleted(target.BlobRef()) {
		t.Fatal("target should be deleted before rebuilding")
	}
	want := reindexSQLiteRows(t, idx.Storage())
	for attempt := range 2 {
		if err := idx.Reindex(); err != nil {
			t.Fatalf("Reindex attempt %d: %v", attempt, err)
		}
		if !idx.IsDeleted(target.BlobRef()) {
			t.Fatalf("target not deleted after Reindex attempt %d", attempt)
		}
		if got := reindexSQLiteRows(t, idx.Storage()); !reflect.DeepEqual(got, want) {
			t.Fatalf("Reindex attempt %d changed index rows:\n got: %v\nwant: %v", attempt, got, want)
		}
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := sqlitekv.NewStorage(path)
	if err != nil {
		t.Fatalf("ordinary reopen after completed rebuild: %v", err)
	}
	idx = indextest.MustNew(t, reopened)
	if !idx.IsDeleted(target.BlobRef()) {
		t.Error("reopened index lost target deletion")
	}
	if got := reindexSQLiteRows(t, idx.Storage()); !reflect.DeepEqual(got, want) {
		t.Errorf("reopened index rows differ:\n got: %v\nwant: %v", got, want)
	}
	for _, b := range []*test.Blob{target, deletion} {
		value, err := idx.Storage().Get("have:" + b.BlobRef().String())
		if err != nil || !strings.HasSuffix(value, "|indexed") {
			t.Errorf("reopened have:%v = %q, %v; want indexed", b.BlobRef(), value, err)
		}
	}
	if _, err := idx.Storage().Get("missing|" + deletion.BlobRef().String() + "|" + target.BlobRef().String()); !errors.Is(err, sorted.ErrNotFound) {
		t.Errorf("reopened delete dependency row: %v; want ErrNotFound", err)
	}
}

func reindexSQLiteRows(t *testing.T, kv sorted.KeyValue) map[string]string {
	t.Helper()
	rows := make(map[string]string)
	it := kv.Find("", "")
	for it.Next() {
		if strings.HasPrefix(it.Key(), "missing|") {
			t.Errorf("unexpected unresolved dependency: %q", it.Key())
		}
		rows[it.Key()] = it.Value()
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestConcurrency(t *testing.T) {
	if testing.Short() {
		t.Logf("skipping for short mode")
		return
	}
	s, clean := newSorted(t)
	defer clean()
	const n = 100
	ch := make(chan error)
	for i := range n {
		i := i
		go func() {
			bm := s.BeginBatch()
			bm.Set("keyA-"+fmt.Sprint(i), fmt.Sprintf("valA=%d", i))
			bm.Set("keyB-"+fmt.Sprint(i), fmt.Sprintf("valB=%d", i))
			ch <- s.CommitBatch(bm)
		}()
	}
	for i := range n {
		if err := <-ch; err != nil {
			t.Errorf("%d: %v", i, err)
		}
	}
}

func numFDs(t *testing.T) int {
	lsofPath, err := exec.LookPath("lsof")
	if err != nil {
		t.Skipf("No lsof available; skipping test")
	}
	out, err := exec.Command(lsofPath, "-n", "-p", fmt.Sprint(os.Getpid())).Output()
	if err != nil {
		t.Skipf("Error running lsof; skipping test: %s", err)
	}
	return bytes.Count(out, []byte("\n")) - 1 // hacky
}

func TestFDLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode.")
	}
	fd0 := numFDs(t)
	t.Logf("fd0 = %d", fd0)

	s, clean := newSorted(t)
	defer clean()

	bm := s.BeginBatch()
	const numRows = 150 // 3x the batchSize of 50 in sqlindex.go; to guarantee we do multiple batches
	for i := range numRows {
		bm.Set(fmt.Sprintf("key:%05d", i), fmt.Sprint(i))
	}
	if err := s.CommitBatch(bm); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		it := s.Find("key:", "key~")
		n := 0
		for it.Next() {
			n++
		}
		if n != numRows {
			t.Errorf("iterated over %d rows; want %d", n, numRows)
		}
		it.Close()
		t.Logf("fd after iteration %d = %d", i, numFDs(t))
	}
}

func TestMultiClaim_SQLite(t *testing.T) {
	test.TLog(t)
	tester{}.test(t, indextest.MultiClaim)
}
