/*
Copyright 2026 The Perkeep Authors

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

package firestore

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/sorted/kvtest"
)

// newTestKV returns a KeyValue on a fresh, randomly named collection of
// the project named by PERKEEP_TEST_FIRESTORE_PROJECT, which is wiped when
// the test ends. It skips the test when the variable is unset.
func newTestKV(t *testing.T) sorted.KeyValue {
	t.Helper()
	project := os.Getenv("PERKEEP_TEST_FIRESTORE_PROJECT")
	if project == "" {
		t.Skip("PERKEEP_TEST_FIRESTORE_PROJECT not set")
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		t.Fatal(err)
	}
	collection := "pk_test_" + hex.EncodeToString(rnd[:])
	kv, err := sorted.NewKeyValue(jsonconfig.Obj{
		"type":       "firestore",
		"project":    project,
		"collection": collection,
	})
	if err != nil {
		t.Fatalf("firestore.NewKeyValue = %v", err)
	}
	t.Cleanup(func() {
		if err := kv.(sorted.Wiper).Wipe(); err != nil {
			t.Errorf("wiping test collection %s: %v", collection, err)
		}
		kv.Close()
	})
	return kv
}

func TestFirestoreKV(t *testing.T) {
	kvtest.TestSorted(t, newTestKV(t))
}

func TestFirestoreBatchAndWipe(t *testing.T) {
	kv := newTestKV(t)

	// The last mutation of a key in a batch wins.
	b := kv.BeginBatch()
	b.Set("k1", "v1")
	b.Set("k2", "v2")
	b.Delete("k2")
	b.Set("k3", "old")
	b.Set("k3", "new")
	if err := kv.CommitBatch(b); err != nil {
		t.Fatalf("CommitBatch: %v", err)
	}
	want := map[string]string{"k1": "v1", "k3": "new"}
	got := map[string]string{}
	it := kv.Find("", "")
	for it.Next() {
		got[it.Key()] = it.Value()
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("after batch, got %v; want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("after batch, got %v; want %v", got, want)
		}
	}

	// Keys that can't be document IDs are rejected, or not found.
	for _, key := range []string{"", "a/b", ".", "..", "__id__"} {
		if _, err := kv.Get(key); err != sorted.ErrNotFound {
			t.Errorf("Get(%q) = %v; want ErrNotFound", key, err)
		}
		if err := kv.Delete(key); err != nil {
			t.Errorf("Delete(%q) = %v; want nil", key, err)
		}
	}
	if err := kv.Set("a/b", "v"); err == nil {
		t.Error("Set(\"a/b\") succeeded; want an error")
	}

	if err := kv.(sorted.Wiper).Wipe(); err != nil {
		t.Fatalf("Wipe: %v", err)
	}
	it = kv.Find("", "")
	if it.Next() {
		t.Errorf("after Wipe, Find still returns %q", it.Key())
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestValidKey(t *testing.T) {
	for _, tt := range []struct {
		key  string
		want bool
	}{
		{"", false},
		{".", false},
		{"..", false},
		{"...", true},
		{"a/b", false},
		{"__name__", false},
		{"____", false},
		{"__", true},
		{"___", true},
		{"__a", true},
		{"a__", true},
		{"have:sha224-00", true},
		{"recpn|7EFA|rt7976-94-71T86:61:49Z|sha224-2c", true},
		{"I do not exist", true},
	} {
		if got := validKey(tt.key); got != tt.want {
			t.Errorf("validKey(%q) = %v; want %v", tt.key, got, tt.want)
		}
	}
}
