// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/binary"
	"io/ioutil"
	"os"
	"runtime"
	"testing"

	"camlistore.org/third_party/github.com/cznic/bufs"
	"camlistore.org/third_party/github.com/cznic/exp/lldb"
	"camlistore.org/third_party/github.com/cznic/zappy"
)

func Test(t *testing.T) {

	if n := len(value100); n != 100 {
		t.Fatal(n)
	}

	c, err := zappy.Encode(nil, value100)
	if err != nil {
		t.Fatal(err)
	}

	if n := len(c); n != 50 {
		t.Fatal(n)
	}
}

func TestProf(t *testing.T) {
	dbname := os.Args[0] + ".db"
	f, err := os.OpenFile(dbname, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0666)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()

	filer := lldb.NewSimpleFileFiler(f) // file
	//filer := lldb.NewMemFiler()         // mem
	a, err := lldb.NewAllocator(filer, &lldb.Options{})
	if err != nil {
		t.Error(err)
		return
	}

	a.Compress = true
	b, _, err := lldb.CreateBTree(a, nil)
	if err != nil {
		t.Error(err)
		return
	}

	var key [16]byte
	for i := uint32(0); int(i) < 1e6; i++ {
		binary.BigEndian.PutUint32(key[:], i)
		if err = b.Set(key[:], value100); err != nil {
			t.Error(err)
			return
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	bufsU, bufsT, bytesU, bytesT, h, m := a.CacheStats()
	const p = 100.0
	t.Logf(
		"cache: buffers %d/%d(%.1f%%), bytes %d/%d(%.1f%%), hits %d(%.1f%%), misses %d(%.1f%%)",
		bufsU, bufsT, p*float64(bufsU)/float64(bufsT),
		bytesU, bytesT, p*float64(bytesU)/float64(bytesT),
		h, p*float64(h)/float64(h+m),
		m, p*float64(m)/float64(h+m),
	)
	nn, bts := bufs.GCache.Stats()
	t.Logf("bufs.GCache.Stats() {%d, %d}", nn, bts)
	t.Logf("%+v\n", ms)
}

func BenchmarkMem(b *testing.B) {
	f, err := ioutil.TempFile("", "")
	if err != nil {
		b.Fatal(err)
	}

	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()

	filer := lldb.NewSimpleFileFiler(f)
	a, err := lldb.NewAllocator(filer, &lldb.Options{})
	if err != nil {
		b.Error(err)
		return
	}

	a.Compress = true

	t, _, err := lldb.CreateBTree(a, nil)
	if err != nil {
		b.Error(err)
		return
	}

	b.ResetTimer()
	var key [16]byte
	for i := uint32(0); int(i) < b.N; i++ {
		binary.BigEndian.PutUint32(key[:], i)
		if err = t.Set(key[:], value100); err != nil {
			b.Error(err)
			return
		}
	}

	if err := filer.Sync(); err != nil {
		b.Error(err)
		return
	}
}
