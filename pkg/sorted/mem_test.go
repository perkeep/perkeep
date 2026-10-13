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

package sorted_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvtest"
)

func TestMemoryKV(t *testing.T) {
	kv := sorted.NewMemoryKeyValue()
	kvtest.TestSorted(t, kv)
}

// TODO(mpl): move this test into kvtest. But that might require
// kvtest taking a "func () sorted.KeyValue) constructor param,
// so kvtest can create several and close in different ways.
func TestMemoryKV_DoubleClose(t *testing.T) {
	kv := sorted.NewMemoryKeyValue()

	it := kv.Find("", "")
	it.Close()
	it.Close()

	kv.Close()
	kv.Close()
}

func memKeys(t *testing.T, it sorted.Iterator) []string {
	t.Helper()
	var keys []string
	for it.Next() {
		keys = append(keys, it.Key()+"="+it.Value())
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	return keys
}

// TestMemoryKVFindSnapshot tests that an iterator sees the store as of the
// Find call, unaffected by later writes.
func TestMemoryKVFindSnapshot(t *testing.T) {
	kv := sorted.NewMemoryKeyValue()
	for _, k := range []string{"a", "b", "c", "d"} {
		if err := kv.Set(k, "1"); err != nil {
			t.Fatal(err)
		}
	}
	it := kv.Find("b", "")
	if !it.Next() || it.Key() != "b" {
		t.Fatalf("first key = %q; want b", it.Key())
	}
	// Change the store mid-iteration: delete the next key, overwrite one
	// ahead, and add a new one.
	if err := kv.Delete("c"); err != nil {
		t.Fatal(err)
	}
	if err := kv.Set("d", "2"); err != nil {
		t.Fatal(err)
	}
	if err := kv.Set("bb", "new"); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(memKeys(t, it), " "), "c=1 d=1"; got != want {
		t.Errorf("rest of snapshot iteration = %q; want %q", got, want)
	}
	if got, want := strings.Join(memKeys(t, kv.Find("", "")), " "), "a=1 b=1 bb=new d=2"; got != want {
		t.Errorf("new iteration = %q; want %q", got, want)
	}
}

func TestMemoryKVFindRange(t *testing.T) {
	kv := sorted.NewMemoryKeyValue()
	for _, k := range []string{"a", "ab", "b", "ba", "c"} {
		if err := kv.Set(k, "v"); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		start, end string
		want       string
	}{
		{"", "", "a ab b ba c"},
		{"ab", "", "ab b ba c"},
		{"aa", "b", "ab"},
		{"b", "c", "b ba"},
		{"b", "b", ""},
		{"z", "", ""},
		{"", "a", ""},
	} {
		var keys []string
		it := kv.Find(tc.start, tc.end)
		for it.Next() {
			keys = append(keys, it.Key())
		}
		it.Close()
		if got := strings.Join(keys, " "); got != tc.want {
			t.Errorf("Find(%q, %q) = %q; want %q", tc.start, tc.end, got, tc.want)
		}
	}
}

// TestMemoryKVConcurrent exercises concurrent reads, writes, batches, and
// iteration, for the race detector.
func TestMemoryKVConcurrent(t *testing.T) {
	kv := sorted.NewMemoryKeyValue()
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Go(func() {
			for i := range 200 {
				k := fmt.Sprintf("k%d-%03d", g, i)
				if err := kv.Set(k, "v"); err != nil {
					t.Error(err)
					return
				}
				b := kv.BeginBatch()
				b.Set(k+"-batch", "v")
				b.Delete(k)
				if err := kv.CommitBatch(b); err != nil {
					t.Error(err)
					return
				}
				if _, err := kv.Get(k + "-batch"); err != nil {
					t.Error(err)
					return
				}
				it := kv.Find(fmt.Sprintf("k%d-", g), "")
				for n := 0; n < 5 && it.Next(); n++ {
				}
				it.Close()
			}
		})
	}
	wg.Wait()
	n := len(memKeys(t, kv.Find("", "")))
	if n != 4*200 {
		t.Errorf("got %d keys; want %d", n, 4*200)
	}
}
