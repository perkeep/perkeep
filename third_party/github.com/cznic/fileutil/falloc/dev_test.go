// Copyright (c) 2011 CZ.NIC z.s.p.o. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// blame: jnml, labs.nic.cz

// Development utils/helpers

package falloc

import (
	"camlistore.org/third_party/github.com/cznic/fileutil/storage"
	"camlistore.org/third_party/github.com/cznic/mathutil"
	"os"
	"runtime"
	"testing"
	"time"
)

func nums(first, last, overhead int, t *testing.T) {
	avail := last - first + 1
	std, escapes, rem := 0, 0, 0
	i := first
	for avail > 0 {
		if (i+overhead)&0xF == 0xF {
			avail--
			escapes++
		}
		if avail == 0 {
			rem++
			break
		}

		avail--
		std++
		i++
	}
	if avail < 0 {
		t.Fatal(avail)
	}

	t.Logf(
		"%04x...%04x (%5d range), overhead %d, std %04x...%04x (%5d values), esc %04x...%04x (%4d values, %d unused)",
		first, last, last-first+1, overhead,
		first, first+std-1, std,
		first+std, last, escapes, rem,
	)
}

func qmap(x []int) (y []int) {
	m := map[int]bool{}
	for _, v := range x {
		m[v] = true
	}
	for k := range m {
		y = append(y, k)
	}
	return
}

func qmapmem(nFlag int) uint64 {
	m := map[int64]bool{}
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	mem := ms.HeapAlloc
	for i := 0; i < nFlag; i++ {
		m[int64(i)] = true
	}
	runtime.GC()
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc - mem
}

func TestDev0(t *testing.T) {
	if !*devFlag {
		t.Log("Not enabled")
		return
	}

	nums(1, 0xFB, 1, t)
	nums(1, 0xFFFF, 3, t)

	x := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	y := qmap(x)
	t.Logf("%v %v", x, y)
	x = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	y = qmap(x)
	t.Logf("%v %v", x, y)
	x = []int{5, 6, 7, 8, 9, 0, 1, 2, 3, 4}
	y = qmap(x)
	t.Logf("%v %v", x, y)

	nFlag := int(1e3)
	mem := qmapmem(nFlag)
	t.Logf("map[int64]bool, nFlag %d, mem %d, %.3g bytes/nFlag", nFlag, mem, float64(mem)/float64(nFlag))
	nFlag = int(1e4)
	mem = qmapmem(nFlag)
	t.Logf("map[int64]bool, nFlag %d, mem %d, %.3g bytes/nFlag", nFlag, mem, float64(mem)/float64(nFlag))
	nFlag = int(1e5)
	mem = qmapmem(nFlag)
	t.Logf("map[int64]bool, nFlag %d, mem %d, %.3g bytes/nFlag", nFlag, mem, float64(mem)/float64(nFlag))
	nFlag = int(1e6)
	mem = qmapmem(nFlag)
	t.Logf("map[int64]bool, nFlag %d, mem %d, %.3g bytes/nFlag", nFlag, mem, float64(mem)/float64(nFlag))
}

func testBenchAlloc(t *testing.T, n, block int) (dt int64) {
	f, err := fcreate(*fnFlag)
	if err != nil {
		panic(err)
	}

	defer func() {
		f.Accessor().Sync()
		probed(t, f)
		ec := f.Close()
		er := os.Remove(*fnFlag)
		if ec != nil {
			t.Fatal(ec)
		}

		if er != nil {
			t.Fatal(er)
		}
	}()

	b := make([]byte, block)
	perc := 1
	if n >= 1000 {
		perc = n / 1000
	}
	dt = time.Now().UnixNano()
	if probe, ok := f.Accessor().(*storage.Probe); ok {
		probe.Reset()
	}
	for i := 0; i < n; i++ {
		if i != 0 && i%perc == 0 {
			ut := float64((time.Now().UnixNano() - dt) / 1e9)
			q := float64(i) / float64(n)
			rt := ut/q - ut
			print("w ", (1000*uint64(i))/uint64(n), " eta ", int(rt/60), ":", int(rt)%60, "              \r")
		}
		if _, err := f.Alloc(b); err != nil {
			panic(err)
		}
	}
	return time.Now().UnixNano() - dt
}

func TestDevBenchAlloc(t *testing.T) {
	if !*devFlag {
		t.Log("Not enabled")
		return
	}

	defer func() {
		if e := recover(); e != nil {
			t.Fatal(e)
		}
	}()

	n, block := *nFlag, int(*blockFlag)
	dt := testBenchAlloc(t, n, block)
	ft := float64(dt) / 1e9
	nb := int64(n) * int64(block)
	t.Logf(
		"%10d blocks x %8d bytes == %8.3fMB, %8.3fs, %8.3fMB/s, %12.3f blocks/s",
		n, block, float64(nb)/(1<<20), ft, float64(nb)/(1<<20)/ft, float64(n)/ft,
	)
}

func testBenchRead(t *testing.T, n, block int) (dt int64) {
	f, err := fcreate(*fnFlag)
	if err != nil {
		t.Fatal(err)
	}

	perc := 1
	if n >= 1000 {
		perc = n / 1000
	}
	h := make([]Handle, n)
	b := make([]byte, block)
	dt = time.Now().UnixNano()
	for i := 0; i < n; i++ {
		if i != 0 && i%perc == 0 {
			ut := float64((time.Now().UnixNano() - dt) / 1e9)
			q := float64(i) / float64(n)
			rt := ut/q - ut
			print("w ", (1000*uint64(i))/uint64(n), " eta ", int(rt/60), ":", int(rt)%60, "              \r")
		}
		if h[i], err = f.Alloc(b); err != nil {
			t.Fatal(err)
		}
	}

	println()
	if probe, ok := f.Accessor().(*storage.Probe); ok {
		probe.Reset()
	}
	dt = time.Now().UnixNano()
	for i := 0; i < n; i++ {
		if i != 0 && i%perc == 0 {
			ut := float64((time.Now().UnixNano() - dt) / 1e9)
			q := float64(i) / float64(n)
			rt := ut/q - ut
			print("r ", (1000*uint64(i))/uint64(n), " eta ", int(rt/60), ":", int(rt)%60, "              \r")
		}
		if _, err := f.Read(h[i]); err != nil {
			t.Fatal(err)
		}
	}
	dt = time.Now().UnixNano() - dt

	f.Accessor().Sync()
	probed(t, f)
	ec := f.Close()
	er := os.Remove(*fnFlag)
	if ec != nil {
		t.Fatal(ec)
	}

	if er != nil {
		t.Fatal(er)
	}

	return
}

func TestDevBenchRead(t *testing.T) {
	if !*devFlag {
		t.Log("Not enabled")
		return
	}

	defer func() {
		if e := recover(); e != nil {
			t.Fatal(e)
		}
	}()

	n, block := *nFlag, int(*blockFlag)
	dt := testBenchRead(t, n, block)
	ft := float64(dt) / 1e9
	nb := int64(n) * int64(block)
	t.Logf(
		"%10d blocks x %8d bytes == %8.3fMB, %8.3fs, %8.3fMB/s, %12.3f blocks/s",
		n, block, float64(nb)/(1<<20), ft, float64(nb)/(1<<20)/ft, float64(n)/ft,
	)
}

func testBenchReadRnd(t *testing.T, n, block int) (dt int64) {
	println(252, n, block) //TODO-
	f, err := fcreate(*fnFlag)
	println(254) //TODO-
	if err != nil {
		println(254) //TODO-
		t.Fatal(err)
	}

	println(259) //TODO-
	defer func() {
		f.Accessor().Sync()
		probed(t, f)
		ec := f.Close()
		er := os.Remove(*fnFlag)
		if ec != nil {
			t.Fatal(ec)
		}

		if er != nil {
			t.Fatal(er)
		}
	}()

	h := make([]Handle, n)
	b := make([]byte, block)
	perc := 1
	if n >= 1000 {
		perc = n / 1000
	}
	println(281) //TODO-
	t0 := time.Now().UnixNano()
	for i := 0; i < n; i++ {
		if h[i], err = f.Alloc(b); err != nil {
			println(285) //TODO-
			t.Fatal(err)
		}
		if i != 0 && i%perc == 0 {
			dt := float64((time.Now().UnixNano() - t0) / 1e9)
			q := float64(i) / float64(n)
			rt := dt/q - dt
			print("w ", (1000*uint64(i))/uint64(n), " eta ", int(rt/60), ":", int(rt)%60, "              \r")
		}
	}
	rng, err := mathutil.NewFC32(0, n-1, true)
	if err != nil {
		println(297) //TODO-
		t.Fatal(err)
	}
	println("\nsync")
	f.Accessor().Sync()
	println("synced")
	t0 = time.Now().UnixNano()
	for i := 0; i < n; i++ {
		if _, err := f.Read(h[rng.Next()]); err != nil {
			println(306) //TODO-
			t.Fatal(err)
		}
		if i != 0 && i%perc == 0 {
			dt := float64((time.Now().UnixNano() - t0) / 1e9)
			q := float64(i) / float64(n)
			rt := dt/q - dt
			print("r ", (1000*uint64(i))/uint64(n), " eta ", int(rt/60), ":", int(rt)%60, "                   \r")
		}
	}
	println()
	println(314) //TODO-
	dt = time.Now().UnixNano() - t0
	if c, ok := f.Accessor().(*storage.Cache); ok {
		x, _ := c.Stat()
		t.Logf("Cache rq %10d, load %10d, purge %10d, top %10d, fs %10d", c.Rq, c.Load, c.Purge, c.Top, x.Size())
	}
	println(323) //TODO-
	return
}

func TestDevBenchReadRnd(t *testing.T) {
	if !*devFlag {
		t.Log("Not enabled")
		return
	}

	defer func() {
		if e := recover(); e != nil {
			t.Fatal(e)
		}
	}()

	dt := testBenchReadRnd(t, *nFlag, int(*blockFlag))
	ft := float64(dt) / 1e9
	nb := int64(*nFlag) * int64(*blockFlag)
	t.Logf(
		"%10d blocks x %8d bytes == %8.3fMB, %8.3fs, %8.3fMB/s, %12.3f blocks/s",
		*nFlag, *blockFlag, float64(nb)/(1<<20), ft, float64(nb)/(1<<20)/ft, float64(*nFlag)/ft,
	)
}
