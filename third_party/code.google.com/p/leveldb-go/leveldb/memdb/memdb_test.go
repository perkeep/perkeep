// Copyright 2011 The LevelDB-Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package memdb

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"camlistore.org/third_party/code.google.com/p/leveldb-go/leveldb/db"
)

// count returns the number of entries in a DB.
func count(d db.DB) (n int) {
	x := d.Find(nil, nil)
	for x.Next() {
		n++
	}
	if x.Close() != nil {
		return -1
	}
	return n
}

// compact compacts a MemDB.
func compact(m *MemDB) (*MemDB, error) {
	n, x := New(nil), m.Find(nil, nil)
	for x.Next() {
		if err := n.Set(x.Key(), x.Value(), nil); err != nil {
			return nil, err
		}
	}
	if err := x.Close(); err != nil {
		return nil, err
	}
	return n, nil
}

func TestBasic(t *testing.T) {
	// Check the empty DB.
	m := New(nil)
	if got, want := count(m), 0; got != want {
		t.Fatalf("0.count: got %v, want %v", got, want)
	}
	v, err := m.Get([]byte("cherry"), nil)
	if string(v) != "" || err != db.ErrNotFound {
		t.Fatalf("1.get: got (%q, %v), want (%q, %v)", v, err, "", db.ErrNotFound)
	}
	// Add some key/value pairs.
	m.Set([]byte("cherry"), []byte("red"), nil)
	m.Set([]byte("peach"), []byte("yellow"), nil)
	m.Set([]byte("grape"), []byte("red"), nil)
	m.Set([]byte("grape"), []byte("green"), nil)
	m.Set([]byte("plum"), []byte("purple"), nil)
	if got, want := count(m), 4; got != want {
		t.Fatalf("2.count: got %v, want %v", got, want)
	}
	// Delete a key twice.
	if got, want := m.Delete([]byte("grape"), nil), error(nil); got != want {
		t.Fatalf("3.delete: got %v, want %v", got, want)
	}
	if got, want := m.Delete([]byte("grape"), nil), db.ErrNotFound; got != want {
		t.Fatalf("4.delete: got %v, want %v", got, want)
	}
	if got, want := count(m), 3; got != want {
		t.Fatalf("5.count: got %v, want %v", got, want)
	}
	// Get keys that are and aren't in the DB.
	v, err = m.Get([]byte("plum"), nil)
	if string(v) != "purple" || err != nil {
		t.Fatalf("6.get: got (%q, %v), want (%q, %v)", v, err, "purple", error(nil))
	}
	v, err = m.Get([]byte("lychee"), nil)
	if string(v) != "" || err != db.ErrNotFound {
		t.Fatalf("7.get: got (%q, %v), want (%q, %v)", v, err, "", db.ErrNotFound)
	}
	// Check an iterator.
	s, x := "", m.Find([]byte("mango"), nil)
	for x.Next() {
		s += fmt.Sprintf("%s/%s.", x.Key(), x.Value())
	}
	if want := "peach/yellow.plum/purple."; s != want {
		t.Fatalf("8.iter: got %q, want %q", s, want)
	}
	if err = x.Close(); err != nil {
		t.Fatalf("9.close: %v", err)
	}
	// Check some more sets and deletes.
	if got, want := m.Delete([]byte("cherry"), nil), error(nil); got != want {
		t.Fatalf("10.delete: got %v, want %v", got, want)
	}
	if got, want := count(m), 2; got != want {
		t.Fatalf("11.count: got %v, want %v", got, want)
	}
	if err := m.Set([]byte("apricot"), []byte("orange"), nil); err != nil {
		t.Fatalf("12.set: %v", err)
	}
	if got, want := count(m), 3; got != want {
		t.Fatalf("13.count: got %v, want %v", got, want)
	}
	// Clean up.
	if err := m.Close(); err != nil {
		t.Fatalf("14.close: %v", err)
	}
}

func TestCount(t *testing.T) {
	m := New(nil)
	for i := 0; i < 200; i++ {
		if j := count(m); j != i {
			t.Fatalf("count: got %d, want %d", j, i)
		}
		m.Set([]byte{byte(i)}, nil, nil)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}

func Test1000Entries(t *testing.T) {
	// Initialize the DB.
	const N = 1000
	m0 := New(nil)
	for i := 0; i < N; i++ {
		k := []byte(strconv.Itoa(i))
		v := []byte(strings.Repeat("x", i))
		m0.Set(k, v, nil)
	}
	// Delete one third of the entries, update another third,
	// and leave the last third alone.
	for i := 0; i < N; i++ {
		switch i % 3 {
		case 0:
			k := []byte(strconv.Itoa(i))
			m0.Delete(k, nil)
		case 1:
			k := []byte(strconv.Itoa(i))
			v := []byte(strings.Repeat("y", i))
			m0.Set(k, v, nil)
		case 2:
			// No-op.
		}
	}
	// Check the DB count.
	if got, want := count(m0), 666; got != want {
		t.Fatalf("count: got %v, want %v", got, want)
	}
	// Check random-access lookup.
	r := rand.New(rand.NewSource(0))
	for i := 0; i < 3*N; i++ {
		j := r.Intn(N)
		k := []byte(strconv.Itoa(j))
		v, err := m0.Get(k, nil)
		var c uint8
		if len(v) != 0 {
			c = v[0]
		}
		switch j % 3 {
		case 0:
			if err != db.ErrNotFound {
				t.Fatalf("get: j=%d, got err=%v, want %v", j, err, db.ErrNotFound)
			}
		case 1:
			if len(v) != j || c != 'y' {
				t.Fatalf("get: j=%d, got len(v),c=%d,%c, want %d,%c", j, len(v), c, j, 'y')
			}
		case 2:
			if len(v) != j || c != 'x' {
				t.Fatalf("get: j=%d, got len(v),c=%d,%c, want %d,%c", j, len(v), c, j, 'x')
			}
		}
	}
	// Check that iterating through the middle of the DB looks OK.
	// Keys are in lexicographic order, not numerical order.
	// Multiples of 3 are not present.
	wants := []string{
		"499",
		"5",
		"50",
		"500",
		"502",
		"503",
		"505",
		"506",
		"508",
		"509",
		"511",
	}
	x := m0.Find([]byte(wants[0]), nil)
	for _, want := range wants {
		if !x.Next() {
			t.Fatalf("iter: next failed, want=%q", want)
		}
		if got := string(x.Key()); got != want {
			t.Fatalf("iter: got %q, want %q", got, want)
		}
	}
	if err := x.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Check that compaction reduces memory usage by at least one third.
	amu0 := m0.ApproximateMemoryUsage()
	if amu0 == 0 {
		t.Fatalf("compact: memory usage is zero")
	}
	m1, err := compact(m0)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	amu1 := m1.ApproximateMemoryUsage()
	if ratio := float64(amu1) / float64(amu0); ratio > 0.667 {
		t.Fatalf("compact: memory usage before=%d, after=%d, ratio=%f", amu0, amu1, ratio)
	}
	// Clean up.
	if err := m0.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
