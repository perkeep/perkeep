// Copyright 2014 The kv Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kv

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"testing"

	"camlistore.org/third_party/github.com/cznic/fileutil"
	"camlistore.org/third_party/github.com/cznic/mathutil"
)

const sz0 = 144 // size of an empty KV DB

var (
	oDB   = flag.String("db", "", "DB to use in BenchmarkEnumerateDB")
	oKeep = flag.Bool("keep", false, "do not delete test DB (some tests)")
)

func dbg(s string, va ...interface{}) {
	if s == "" {
		s = strings.Repeat("%v ", len(va))
	}
	_, fn, fl, _ := runtime.Caller(1)
	fmt.Printf("%s:%d: ", path.Base(fn), fl)
	fmt.Printf(s, va...)
	fmt.Println()
}

func opts() *Options {
	return &Options{
		noClone:             true,
		VerifyDbBeforeOpen:  true,
		VerifyDbAfterOpen:   true,
		VerifyDbBeforeClose: true,
		VerifyDbAfterClose:  true,
	}
}

func temp() (dir, name string) {
	dir, err := ioutil.TempDir("", "test-kv-")
	if err != nil {
		panic(err)
	}

	return dir, filepath.Join(dir, "test.tmp")
}

func TestCreate(t *testing.T) {
	o := opts()

	dir, name := temp()
	defer os.RemoveAll(dir)

	db, err := Create(name, o)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()

	if _, err = Create(name, opts()); err == nil {
		t.Error("unexpected success")
		return
	}

	if _, err = Open(name, opts()); err == nil {
		t.Error("unexpected success")
		return
	}
}

func TestCreateMem(t *testing.T) {
	db, err := CreateMem(opts())
	if err != nil {
		t.Fatal(err)
	}

	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateTemp(t *testing.T) {
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
}

func cp(dest, src string) {
	b, err := ioutil.ReadFile(src)
	if err != nil {
		panic(err)
	}

	f, err := os.Create(dest)
	if err != nil {
		panic(err)
	}

	n, err := f.Write(b)
	if n != len(b) {
		panic(n)
	}

	if err != nil {
		panic(err)
	}
}

func TestOpen(t *testing.T) {
	dir, _ := temp()
	defer os.RemoveAll(dir)

	name := filepath.Join(dir, "open.db")
	cp(name, "_testdata/open.db")
	cp(filepath.Join(dir, ".2196ad2c3cbc669595720f0cfb6f0dd888bc64bc"), "_testdata/.2196ad2c3cbc669595720f0cfb6f0dd888bc64bc")
	db, err := Open(name, opts())
	if err != nil {
		t.Fatal(err)
	}

	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestClose(t *testing.T) {
	o := opts()

	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		db.Close()
	}()

	go db.Close()
	if err := db.Close(); err != nil {
		t.Error(err)
		return
	}

	if err := db.Close(); err != nil {
		t.Error(err)
		return
	}
}

func TestName(t *testing.T) {
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	if n := db.Name(); n == "" ||
		!strings.Contains(n, dir) ||
		!strings.HasPrefix(filepath.Base(n), "temp") ||
		!strings.HasSuffix(filepath.Base(n), ".db") ||
		path.Base(n) == "temp.db" {
		t.Error(n)
	}
}

func TestSize(t *testing.T) {
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	sz, err := db.Size()
	if err != nil {
		t.Error(err)
		return
	}

	if sz != sz0 {
		t.Error(sz, sz0)
	}
}

func TestVerify(t *testing.T) {
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	t.Log(db.Name(), o._WAL)
	if err := db.Verify(nil, nil); err != nil {
		t.Error(err)
	}
}

//DONE xacts test
// ---- tested in lldb extensively

func n2b(n int) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(n))
	return b[:]
}

func b2n(b []byte) int {
	if len(b) != 8 {
		return mathutil.MinInt
	}

	return int(binary.BigEndian.Uint64(b))
}

func fc() *mathutil.FC32 {
	r, err := mathutil.NewFC32(0, math.MaxInt32, false)
	if err != nil {
		panic(err)
	}

	return r
}

func TestDelete(t *testing.T) {
	const (
		n = 500
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	dbname := db.Name()
	defer db.Close()

	rng := fc()
	var keys []int
	for i := 0; i < n*m; i++ {
		k, v := rng.Next(), rng.Next()
		keys = append(keys, k)
		if err := db.Set(n2b(k), n2b(v)); err != nil {
			t.Error(err)
			return
		}
	}

	c := make(chan int)
	var wg sync.WaitGroup
	x := 0
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			<-c
			for _, k := range keys[start : start+n] {
				if err := db.Delete(n2b(k)); err != nil {
					t.Error(err)
					return
				}
			}
		}(x)
		x += n
	}
	close(c)
	wg.Wait()

	if err := db.Close(); err != nil {
		t.Error(err)
		return
	}

	fi, err := os.Stat(dbname)
	if err != nil {
		t.Error(err)
		return
	}

	if sz := fi.Size(); sz != sz0 {
		t.Error(sz, sz0)
	}
}

func BenchmarkDelete16(b *testing.B) {
	const (
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	rng := fc()
	var keys []int
	for i := 0; i < b.N; i++ {
		k, v := rng.Next(), rng.Next()
		keys = append(keys, k)
		if err := db.Set(n2b(k), n2b(v)); err != nil {
			b.Error(err)
			return
		}
	}

	c := make(chan int)
	var wg sync.WaitGroup
	x := 0
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			<-c
			for _, k := range keys[start : start+b.N/m] {
				db.Delete(n2b(k))
			}
		}(x)
		x += b.N / m
	}
	b.ResetTimer()
	close(c)
	wg.Wait()
	b.StopTimer()
}

func TestExtract(t *testing.T) {
	const (
		n = 500
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	dbname := db.Name()
	defer db.Close()

	rng := fc()
	var keys, vals []int
	for i := 0; i < n*m; i++ {
		k, v := rng.Next(), rng.Next()
		keys = append(keys, k)
		vals = append(vals, v)
		if err := db.Set(n2b(k), n2b(v)); err != nil {
			t.Error(err)
			return
		}
	}

	c := make(chan int)
	var wg sync.WaitGroup
	x := 0
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			<-c
			for i, k := range keys[start : start+n] {
				v, err := db.Extract(nil, n2b(k))
				if err != nil {
					t.Error(err)
					return
				}

				if g, e := len(v), 8; g != e {
					t.Error(err)
					return
				}

				if g, e := b2n(v), vals[start+i]; g != e {
					t.Errorf("index %#x, key %#x, got %#x, want %#x", i, k, g, e)
					return
				}
			}
		}(x)
		x += n
	}
	close(c)
	wg.Wait()

	if err := db.Close(); err != nil {
		t.Error(err)
		return
	}

	fi, err := os.Stat(dbname)
	if err != nil {
		t.Error(err)
		return
	}

	if sz := fi.Size(); sz != sz0 {
		t.Error(sz, sz0)
	}
}

func BenchmarkExtract16(b *testing.B) {
	const (
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	rng := fc()
	var keys, vals []int
	for i := 0; i < b.N; i++ {
		k, v := rng.Next(), rng.Next()
		keys = append(keys, k)
		vals = append(vals, v)
		if err := db.Set(n2b(k), n2b(v)); err != nil {
			b.Error(err)
			return
		}
	}

	c := make(chan int)
	var wg sync.WaitGroup
	x := 0
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			buf := make([]byte, 8)
			<-c
			for _, k := range keys[start : start+b.N/m] {
				db.Extract(buf, n2b(k))
			}
		}(x)
		x += b.N / m
	}
	b.ResetTimer()
	close(c)
	wg.Wait()
	b.StopTimer()
}

func TestFirst(t *testing.T) {
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	k, v, err := db.First()
	if err != nil {
		t.Error(err)
		return
	}

	if k != nil {
		t.Error(k)
		return
	}

	if v != nil {
		t.Error(v)
		return
	}

	if err := db.Set(n2b(10), n2b(100)); err != nil {
		t.Error(err)
		return
	}

	k, v, err = db.First()
	if err != nil {
		t.Error(err)
		return
	}

	if len(k) != 8 {
		t.Error(k)
		return
	}

	if g, e := b2n(k), 10; g != e {
		t.Error(g, e)
		return
	}

	if len(v) != 8 {
		t.Error(v)
		return
	}

	if g, e := b2n(v), 100; g != e {
		t.Error(g, e)
		return
	}

	if err := db.Set(n2b(20), n2b(200)); err != nil {
		t.Error(err)
		return
	}

	k, v, err = db.First()
	if err != nil {
		t.Error(err)
		return
	}

	if len(k) != 8 {
		t.Error(k)
		return
	}

	if g, e := b2n(k), 10; g != e {
		t.Error(g, e)
		return
	}

	if len(v) != 8 {
		t.Error(v)
		return
	}

	if g, e := b2n(v), 100; g != e {
		t.Error(g, e)
		return
	}

	if err := db.Set(n2b(5), n2b(50)); err != nil {
		t.Error(err)
		return
	}

	k, v, err = db.First()
	if err != nil {
		t.Error(err)
		return
	}

	if len(k) != 8 {
		t.Error(k)
		return
	}

	if g, e := b2n(k), 5; g != e {
		t.Error(g, e)
		return
	}

	if len(v) != 8 {
		t.Error(v)
		return
	}

	if g, e := b2n(v), 50; g != e {
		t.Error(g, e)
		return
	}

}

func BenchmarkFirst16(b *testing.B) {
	const n = 5000
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		b.Fatal(err)
	}

	defer func() {
		db.Close()
		os.Remove(o._WAL)
	}()

	rng := fc()
	for i := 0; i < n; i++ {
		if err := db.Set(n2b(rng.Next()), n2b(rng.Next())); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.First()
	}
	b.StopTimer()
}

func TestGet(t *testing.T) {
	const (
		n = 800
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		db.Close()
		os.Remove(o._WAL)
	}()

	rng := fc()
	var keys, vals []int
	for i := 0; i < n*m; i++ {
		k, v := rng.Next(), rng.Next()
		keys = append(keys, k)
		vals = append(vals, v)
		if err := db.Set(n2b(k), n2b(v)); err != nil {
			t.Error(err)
			return
		}
	}

	c := make(chan int)
	var wg sync.WaitGroup
	x := 0
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			buf := make([]byte, 8)
			<-c
			for i, k := range keys[start : start+n] {
				v, err := db.Get(buf, n2b(k))
				if err != nil {
					t.Error(err)
					return
				}

				if g, e := len(v), 8; g != e {
					t.Error(err)
					return
				}

				if g, e := b2n(v), vals[start+i]; g != e {
					t.Errorf("index %#x, key %#x, got %#x, want %#x", i, k, g, e)
					return
				}
			}
		}(x)
		x += n
	}
	close(c)
	wg.Wait()
}

func BenchmarkGet16(b *testing.B) {
	const (
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	rng := fc()
	var keys, vals []int
	for i := 0; i < b.N; i++ {
		k, v := rng.Next(), rng.Next()
		keys = append(keys, k)
		vals = append(vals, v)
		if err := db.Set(n2b(k), n2b(v)); err != nil {
			b.Error(err)
			return
		}
	}

	c := make(chan int)
	var wg sync.WaitGroup
	x := 0
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			buf := make([]byte, 8)
			<-c
			for _, k := range keys[start : start+b.N/m] {
				db.Get(buf, n2b(k))
			}
		}(x)
		x += b.N / m
	}
	b.ResetTimer()
	close(c)
	wg.Wait()
	b.StopTimer()
}

func TestInc(t *testing.T) {
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		db.Close()
	}()

	v, err := db.Inc(nil, 1)
	if err != nil {
		t.Error(err)
		return
	}

	if g, e := v, int64(1); g != e {
		t.Error(g, e)
		return
	}

	v, err = db.Inc(nil, 2)
	if err != nil {
		t.Error(err)
		return
	}

	if g, e := v, int64(3); g != e {
		t.Error(g, e)
		return
	}

	if err := db.Set(nil, nil); err != nil {
		t.Error(err)
		return
	}

	v, err = db.Inc(nil, 4)
	if err != nil {
		t.Error(err)
		return
	}

	if g, e := v, int64(4); g != e {
		t.Error(g, e)
		return
	}

	if err := db.Set(nil, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}); err != nil {
		t.Error(err)
		return
	}

	v, err = db.Inc(nil, 5)
	if err != nil {
		t.Error(err)
		return
	}

	if g, e := v, int64(5); g != e {
		t.Error(g, e)
		return
	}

}

func TestInc2(t *testing.T) {
	const (
		n = 10000
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		db.Close()
	}()

	c := make(chan int)
	var wg sync.WaitGroup
	sum := 0
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(n, delta int) {
			defer wg.Done()
			<-c
			for i := 0; i < n; i++ {
				if _, err := db.Inc(nil, int64(delta)); err != nil {
					t.Error(err)
					return
				}
			}
		}(n, i)
		sum += n * i
	}
	close(c)
	wg.Wait()
	v, err := db.Get(nil, nil)
	if err != nil {
		t.Error(err)
		return
	}

	if n := len(v); n != 8 {
		t.Error(n, 8)
		return
	}

	if g, e := b2n(v), sum; g != e {
		t.Errorf("%#x %#x", g, e)
	}
}

func BenchmarkInc(b *testing.B) {
	const (
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	c := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(n, delta int) {
			defer wg.Done()
			<-c
			for i := 0; i < b.N/m; i++ {
				db.Inc(nil, int64(delta))
			}
		}(3*i, 5*i)
	}
	b.ResetTimer()
	close(c)
	wg.Wait()
	b.StopTimer()
}

func TestLast(t *testing.T) {
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		db.Close()
	}()

	k, v, err := db.Last()
	if err != nil {
		t.Error(err)
		return
	}

	if k != nil {
		t.Error(k)
		return
	}

	if v != nil {
		t.Error(v)
		return
	}

	if err := db.Set(n2b(10), n2b(100)); err != nil {
		t.Error(err)
		return
	}

	k, v, err = db.Last()
	if err != nil {
		t.Error(err)
		return
	}

	if len(k) != 8 {
		t.Error(k)
		return
	}

	if g, e := b2n(k), 10; g != e {
		t.Error(g, e)
		return
	}

	if len(v) != 8 {
		t.Error(v)
		return
	}

	if g, e := b2n(v), 100; g != e {
		t.Error(g, e)
		return
	}

	if err := db.Set(n2b(5), n2b(50)); err != nil {
		t.Error(err)
		return
	}

	k, v, err = db.Last()
	if err != nil {
		t.Error(err)
		return
	}

	if len(k) != 8 {
		t.Error(k)
		return
	}

	if g, e := b2n(k), 10; g != e {
		t.Error(g, e)
		return
	}

	if len(v) != 8 {
		t.Error(v)
		return
	}

	if g, e := b2n(v), 100; g != e {
		t.Error(g, e)
		return
	}

	if err := db.Set(n2b(20), n2b(200)); err != nil {
		t.Error(err)
		return
	}

	k, v, err = db.Last()
	if err != nil {
		t.Error(err)
		return
	}

	if len(k) != 8 {
		t.Error(k)
		return
	}

	if g, e := b2n(k), 20; g != e {
		t.Error(g, e)
		return
	}

	if len(v) != 8 {
		t.Error(v)
		return
	}

	if g, e := b2n(v), 200; g != e {
		t.Error(g, e)
		return
	}

}

func BenchmarkLast16(b *testing.B) {
	const n = 5000
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	db, err := CreateTemp("_testdata", "temp", ".db", o)
	if err != nil {
		b.Fatal(err)
	}

	dbname := db.Name()
	defer func(n string) {
		db.Close()
		os.Remove(n)
		os.Remove(o._WAL)
	}(dbname)

	rng := fc()
	for i := 0; i < n; i++ {
		if err := db.Set(n2b(rng.Next()), n2b(rng.Next())); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.Last()
	}
	b.StopTimer()
}

func TestPut(t *testing.T) {
	const (
		n = 800
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)

	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	rng := fc()
	var keys, vals []int
	for i := 0; i < n*m; i++ {
		k, v := rng.Next(), rng.Next()
		keys = append(keys, k)
		vals = append(vals, v)
	}

	c := make(chan int)
	var wg sync.WaitGroup
	x := 0
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			buf := make([]byte, 8)
			<-c
			for i, k := range keys[start : start+n] {
				if _, _, err := db.Put(buf, n2b(k), func(key, old []byte) (new []byte, write bool, err error) {
					return n2b(vals[start+i]), true, nil
				}); err != nil {
					t.Error(err)
					return
				}
			}
		}(x)
		x += n
	}
	close(c)
	wg.Wait()
	buf := make([]byte, 8)
	for i, k := range keys {
		v, err := db.Get(buf, n2b(k))
		if err != nil {
			t.Error(err)
			return
		}

		if g, e := len(v), 8; g != e {
			t.Error(g, e)
		}

		if g, e := b2n(v), vals[i]; g != e {
			t.Error(g, e)
			return
		}
	}
}

func BenchmarkPut16(b *testing.B) {
	const (
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	rng := fc()
	var keys, vals []int
	for i := 0; i < b.N; i++ {
		k, v := rng.Next(), rng.Next()
		keys = append(keys, k)
		vals = append(vals, v)
	}

	c := make(chan int)
	var wg sync.WaitGroup
	x := 0
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			buf := make([]byte, 8)
			<-c
			for _, k := range keys[start : start+b.N/m] {
				db.Put(buf, n2b(k), func(key, old []byte) (new []byte, write bool, err error) {
					return buf, true, nil
				})
			}
		}(x)
		x += b.N / m
	}
	b.ResetTimer()
	close(c)
	wg.Wait()
	b.StopTimer()
}

func TestSet(t *testing.T) {
	const (
		n = 800
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	rng := fc()
	var keys, vals []int
	for i := 0; i < n*m; i++ {
		k, v := rng.Next(), rng.Next()
		keys = append(keys, k)
		vals = append(vals, v)
	}

	c := make(chan int)
	var wg sync.WaitGroup
	x := 0
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			<-c
			for i, k := range keys[start : start+n] {
				if err := db.Set(n2b(k), n2b(vals[start+i])); err != nil {
					t.Error(err)
					return
				}
			}
		}(x)
		x += n
	}
	close(c)
	wg.Wait()
	buf := make([]byte, 8)
	for i, k := range keys {
		v, err := db.Get(buf, n2b(k))
		if err != nil {
			t.Error(err)
			return
		}

		if g, e := len(v), 8; g != e {
			t.Error(g, e)
		}

		if g, e := b2n(v), vals[i]; g != e {
			t.Error(g, e)
			return
		}
	}
}

func BenchmarkSet16(b *testing.B) {
	const (
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	rng := fc()
	var keys, vals []int
	for i := 0; i < b.N; i++ {
		k, v := rng.Next(), rng.Next()
		keys = append(keys, k)
		vals = append(vals, v)
	}

	c := make(chan int)
	var wg sync.WaitGroup
	x := 0
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			buf := make([]byte, 8)
			<-c
			for _, k := range keys[start : start+b.N/m] {
				db.Set(n2b(k), buf)
			}
		}(x)
		x += b.N / m
	}
	b.ResetTimer()
	close(c)
	wg.Wait()
	b.StopTimer()
}

func TestSeekNext(t *testing.T) {
	// seeking within 3 keys: 10, 20, 30
	table := []struct {
		k    int
		hit  bool
		keys []int
	}{
		{5, false, []int{10, 20, 30}},
		{10, true, []int{10, 20, 30}},
		{15, false, []int{20, 30}},
		{20, true, []int{20, 30}},
		{25, false, []int{30}},
		{30, true, []int{30}},
		{35, false, []int{}},
	}

	for i, test := range table {
		up := test.keys
		db, err := CreateMem(opts())
		if err != nil {
			t.Fatal(i, err)
		}

		if err := db.Set(n2b(10), n2b(100)); err != nil {
			t.Fatal(i, err)
		}

		if err := db.Set(n2b(20), n2b(200)); err != nil {
			t.Fatal(i, err)
		}

		if err := db.Set(n2b(30), n2b(300)); err != nil {
			t.Fatal(i, err)
		}

		for brokenSerial := 0; brokenSerial < 16; brokenSerial++ {
			en, hit, err := db.Seek(n2b(test.k))
			if err != nil {
				t.Fatal(err)
			}

			if g, e := hit, test.hit; g != e {
				t.Fatal(i, g, e)
			}

			j := 0
			for {
				if brokenSerial&(1<<uint(j)) != 0 {
					if err := db.Set(n2b(20), n2b(200)); err != nil {
						t.Fatal(i, err)
					}
				}

				k, v, err := en.Next()
				if err != nil {
					if !fileutil.IsEOF(err) {
						t.Fatal(i, err)
					}

					break
				}

				if g, e := len(k), 8; g != e {
					t.Fatal(i, g, e)
				}

				if j >= len(up) {
					t.Fatal(i, j, brokenSerial)
				}

				if g, e := b2n(k), up[j]; g != e {
					t.Fatal(i, j, brokenSerial, g, e)
				}

				if g, e := len(v), 8; g != e {
					t.Fatal(i, g, e)
				}

				if g, e := b2n(v), 10*up[j]; g != e {
					t.Fatal(i, g, e)
				}

				j++

			}

			if g, e := j, len(up); g != e {
				t.Fatal(i, j, g, e)
			}
		}

	}
}

func TestSeekPrev(t *testing.T) {
	// seeking within 3 keys: 10, 20, 30
	table := []struct {
		k    int
		hit  bool
		keys []int
	}{
		{5, false, []int{10}},
		{10, true, []int{10}},
		{15, false, []int{20, 10}},
		{20, true, []int{20, 10}},
		{25, false, []int{30, 20, 10}},
		{30, true, []int{30, 20, 10}},
		{35, false, []int{}},
	}

	for i, test := range table {
		down := test.keys
		db, err := CreateMem(opts())
		if err != nil {
			t.Fatal(i, err)
		}

		if err := db.Set(n2b(10), n2b(100)); err != nil {
			t.Fatal(i, err)
		}

		if err := db.Set(n2b(20), n2b(200)); err != nil {
			t.Fatal(i, err)
		}

		if err := db.Set(n2b(30), n2b(300)); err != nil {
			t.Fatal(i, err)
		}

		for brokenSerial := 0; brokenSerial < 16; brokenSerial++ {
			en, hit, err := db.Seek(n2b(test.k))
			if err != nil {
				t.Fatal(err)
			}

			if g, e := hit, test.hit; g != e {
				t.Fatal(i, g, e)
			}

			j := 0
			for {
				if brokenSerial&(1<<uint(j)) != 0 {
					if err := db.Set(n2b(20), n2b(200)); err != nil {
						t.Fatal(i, err)
					}
				}

				k, v, err := en.Prev()
				if err != nil {
					if !fileutil.IsEOF(err) {
						t.Fatal(i, err)
					}

					break
				}

				if g, e := len(k), 8; g != e {
					t.Fatal(i, g, e)
				}

				if j >= len(down) {
					t.Fatal(i, j, brokenSerial)
				}

				if g, e := b2n(k), down[j]; g != e {
					t.Fatal(i, j, brokenSerial, g, e)
				}

				if g, e := len(v), 8; g != e {
					t.Fatal(i, g, e)
				}

				if g, e := b2n(v), 10*down[j]; g != e {
					t.Fatal(i, g, e)
				}

				j++

			}

			if g, e := j, len(down); g != e {
				t.Fatal(i, j, g, e)
			}
		}

	}
}

func BenchmarkSeek(b *testing.B) {
	const (
		m = 4
	)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	rng := fc()
	var keys, vals []int
	for i := 0; i < b.N; i++ {
		k, v := rng.Next(), rng.Next()
		keys = append(keys, k)
		vals = append(vals, v)
		if err := db.Set(n2b(k), n2b(v)); err != nil {
			b.Error(err)
			return
		}
	}

	c := make(chan int)
	var wg sync.WaitGroup
	x := 0
	for i := 0; i < m; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			<-c
			for _, k := range keys[start : start+b.N/m] {
				db.Seek(n2b(k))
			}
		}(x)
		x += b.N / m
	}
	b.ResetTimer()
	close(c)
	wg.Wait()
	b.StopTimer()
}

func BenchmarkNext1e3(b *testing.B) {
	const N = int(1e3)
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	o := opts()
	dir, _ := temp()
	defer os.RemoveAll(dir)

	db, err := CreateTemp(dir, "temp", ".db", o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	for i := 0; i < N; i++ {
		if err := db.Set(n2b(i), n2b(17*i)); err != nil {
			b.Error(err)
			return
		}
	}

	b.ResetTimer()
	b.StopTimer()
	var n int
	for i := 0; i < b.N; i++ {
		en, err := db.SeekFirst()
		if err != nil {
			b.Error(err)
			return
		}

		b.StartTimer()
		for n = 0; ; n++ {
			if _, _, err := en.Next(); err != nil {
				break
			}
		}
		b.StopTimer()
		if g, e := n, N; g != e {
			b.Error(g, e)
			return
		}
	}
	b.StopTimer()
}

func TestSeekFirst(t *testing.T) {
	db, err := CreateMem(opts())
	if err != nil {
		t.Fatal(err)
	}

	en, err := db.SeekFirst()
	if err == nil {
		t.Fatal(err)
	}

	if err := db.Set(n2b(100), n2b(1000)); err != nil {
		t.Fatal(err)
	}

	if en, err = db.SeekFirst(); err != nil {
		t.Fatal(err)
	}

	k, v, err := en.Next()
	if err != nil {
		t.Fatal(err)
	}

	if g, e := b2n(k), 100; g != e {
		t.Fatal(g, e)
	}

	if g, e := b2n(v), 1000; g != e {
		t.Fatal(g, e)
	}

	if err := db.Set(n2b(110), n2b(1100)); err != nil {
		t.Fatal(err)
	}

	if en, err = db.SeekFirst(); err != nil {
		t.Fatal(err)
	}

	if k, v, err = en.Next(); err != nil {
		t.Fatal(err)
	}

	if g, e := b2n(k), 100; g != e {
		t.Fatal(g, e)
	}

	if g, e := b2n(v), 1000; g != e {
		t.Fatal(g, e)
	}

	if err := db.Set(n2b(90), n2b(900)); err != nil {
		t.Fatal(err)
	}

	if en, err = db.SeekFirst(); err != nil {
		t.Fatal(err)
	}

	if k, v, err = en.Next(); err != nil {
		t.Fatal(err)
	}

	if g, e := b2n(k), 90; g != e {
		t.Fatal(g, e)
	}

	if g, e := b2n(v), 900; g != e {
		t.Fatal(g, e)
	}

}

func TestSeekLast(t *testing.T) {
	db, err := CreateMem(opts())
	if err != nil {
		t.Fatal(err)
	}

	en, err := db.SeekLast()
	if err == nil {
		t.Fatal(err)
	}

	if err := db.Set(n2b(100), n2b(1000)); err != nil {
		t.Fatal(err)
	}

	if en, err = db.SeekLast(); err != nil {
		t.Fatal(err)
	}

	k, v, err := en.Next()
	if err != nil {
		t.Fatal(err)
	}

	if g, e := b2n(k), 100; g != e {
		t.Fatal(g, e)
	}

	if g, e := b2n(v), 1000; g != e {
		t.Fatal(g, e)
	}

	if err := db.Set(n2b(90), n2b(900)); err != nil {
		t.Fatal(err)
	}

	if en, err = db.SeekLast(); err != nil {
		t.Fatal(err)
	}

	if k, v, err = en.Next(); err != nil {
		t.Fatal(err)
	}

	if g, e := b2n(k), 100; g != e {
		t.Fatal(g, e)
	}

	if g, e := b2n(v), 1000; g != e {
		t.Fatal(g, e)
	}

	if err := db.Set(n2b(110), n2b(1100)); err != nil {
		t.Fatal(err)
	}

	if en, err = db.SeekLast(); err != nil {
		t.Fatal(err)
	}

	if k, v, err = en.Next(); err != nil {
		t.Fatal(err)
	}

	if g, e := b2n(k), 110; g != e {
		t.Fatal(g, e)
	}

	if g, e := b2n(v), 1100; g != e {
		t.Fatal(g, e)
	}

}

func TestWALName(t *testing.T) {
	db, err := CreateTemp("", "kv-wal-name", ".test", opts())
	if err != nil {
		t.Fatal(err)
	}

	defer func(n, wn string) {
		if _, err := os.Stat(n); err != nil {
			t.Error(err)
		} else {
			if err := os.Remove(n); err != nil {
				t.Error(err)
			}
		}
		if _, err := os.Stat(wn); err != nil {
			t.Error(err)
		} else {
			if err := os.Remove(wn); err != nil {
				t.Error(err)
			}
		}
		t.Logf("%q\n%q", n, wn)

	}(db.Name(), db.WALName())

	if err := db.Close(); err != nil {
		t.Error(err)
		return
	}

	if n := db.WALName(); n != "" {
		t.Error(n)
	}
}

func TestCreateWithEmptyWAL(t *testing.T) {
	dir, err := ioutil.TempDir("", "kv-test-create")
	if err != nil {
		t.Fatal(err)
	}

	defer os.RemoveAll(dir)
	dbName := filepath.Join(dir, "test.db")
	var o Options
	walName := o.walName(dbName, "")
	wal, err := os.Create(walName)
	if err != nil {
		t.Error(err)
		return
	}

	wal.Close()
	defer os.Remove(walName)

	db, err := Create(dbName, &Options{})
	if err != nil {
		t.Error(err)
		return
	}

	if err = db.Set([]byte("foo"), []byte("bar")); err != nil {
		t.Error(err)
	}
	db.Close()
}

func TestCreateWithNonEmptyWAL(t *testing.T) {
	dir, err := ioutil.TempDir("", "kv-test-create")
	if err != nil {
		t.Fatal(err)
	}

	defer os.RemoveAll(dir)
	dbName := filepath.Join(dir, "test.db")
	var o Options
	walName := o.walName(dbName, "")
	wal, err := os.Create(walName)
	if err != nil {
		t.Error(err)
		return
	}

	if n, err := wal.Write([]byte{0}); n != 1 || err != nil {
		t.Error(n, err)
		return
	}

	wal.Close()
	defer os.Remove(walName)

	if _, err = Create(dbName, &Options{}); err == nil {
		t.Error("Unexpected success")
		return
	}
}

func BenchmarkEnumerateDB(b *testing.B) {
	g := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(g)
	var db *DB
	var err error
	switch nm := *oDB; {
	case nm != "":
		db, err = Open(nm, &Options{
			VerifyDbBeforeOpen:  true,
			VerifyDbAfterOpen:   true,
			VerifyDbBeforeClose: true,
			VerifyDbAfterClose:  true,
		})
		if err != nil {
			b.Fatal(err)
		}

		defer db.Close()
	default:
		db, err = CreateMem(&Options{})
		if err != nil {
			b.Fatal(err)
		}

		for i := 0; i < 1e3; i++ {
			if err := db.Set(n2b(i), n2b(i)); err != nil {
				b.Fatal(err)
			}
		}
	}

	var n int
	debug.FreeOSMemory()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n = 0
		en, err := db.SeekFirst()
		if err != nil {
			b.Fatal(err)
		}

		for {
			_, _, err := en.Next()
			if err != nil {
				if err == io.EOF {
					break
				}

				b.Fatal(err)
			}

			n++
		}
	}
	b.StopTimer()
}
