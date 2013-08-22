// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*

From: https://code.google.com/p/leveldb/

Performance

Here is a performance report (with explanations) from the run of the included
db_bench program. The results are somewhat noisy, but should be enough to get a
ballpark performance estimate.

Setup

We use a database with a million entries. Each entry has a 16 byte key, and a
100 byte value. Values used by the benchmark compress to about half their
original size.

   LevelDB:    version 1.1
   Date:       Sun May  1 12:11:26 2011
   CPU:        4 x Intel(R) Core(TM)2 Quad CPU    Q6600  @ 2.40GHz
   CPUCache:   4096 KB
   Keys:       16 bytes each
   Values:     100 bytes each (50 bytes after compression)
   Entries:    1000000
   Raw Size:   110.6 MB (estimated)
   File Size:  62.9 MB (estimated)

Write performance

The "fill" benchmarks create a brand new database, in either sequential, or
random order. The "fillsync" benchmark flushes data from the operating system
to the disk after every operation; the other write operations leave the data
sitting in the operating system buffer cache for a while. The "overwrite"
benchmark does random writes that update existing keys in the database.

   fillseq      :       1.765 micros/op;   62.7 MB/s
   fillsync     :     268.409 micros/op;    0.4 MB/s (10000 ops)
   fillrandom   :       2.460 micros/op;   45.0 MB/s
   overwrite    :       2.380 micros/op;   46.5 MB/s

Each "op" above corresponds to a write of a single key/value pair. I.e., a
random write benchmark goes at approximately 400,000 writes per second.

Each "fillsync" operation costs much less (0.3 millisecond) than a disk seek
(typically 10 milliseconds). We suspect that this is because the hard disk
itself is buffering the update in its memory and responding before the data has
been written to the platter. This may or may not be safe based on whether or
not the hard disk has enough power to save its memory in the event of a power
failure.

Read performance

We list the performance of reading sequentially in both the forward and reverse
direction, and also the performance of a random lookup. Note that the database
created by the benchmark is quite small. Therefore the report characterizes the
performance of leveldb when the working set fits in memory. The cost of reading
a piece of data that is not present in the operating system buffer cache will
be dominated by the one or two disk seeks needed to fetch the data from disk.
Write performance will be mostly unaffected by whether or not the working set
fits in memory.

   readrandom   :      16.677 micros/op;  (approximately 60,000 reads per second)
   readseq      :       0.476 micros/op;  232.3 MB/s
   readreverse  :       0.724 micros/op;  152.9 MB/s

LevelDB compacts its underlying storage data in the background to improve read
performance. The results listed above were done immediately after a lot of
random writes. The results after compactions (which are usually triggered
automatically) are better.

   readrandom   :      11.602 micros/op;  (approximately 85,000 reads per second)
   readseq      :       0.423 micros/op;  261.8 MB/s
   readreverse  :       0.663 micros/op;  166.9 MB/s

Some of the high cost of reads comes from repeated decompression of blocks read
from disk. If we supply enough cache to the leveldb so it can hold the
uncompressed blocks in memory, the read performance improves again:

   readrandom   :       9.775 micros/op;  (approximately 100,000 reads per second before compaction)
   readrandom   :       5.215 micros/op;  (approximately 190,000 reads per second after compaction)

*/

/*

Executing leveldb's db_bench on local machine:

(10:49) jnml@fsc-r550:~/src/code.google.com/p/leveldb$ ./db_bench
LevelDB:    version 1.10
Date:       Fri May 17 10:49:37 2013
CPU:        4 * Intel(R) Xeon(R) CPU           X5450  @ 3.00GHz
CPUCache:   6144 KB
Keys:       16 bytes each
Values:     100 bytes each (50 bytes after compression)
Entries:    1000000
RawSize:    110.6 MB (estimated)
FileSize:   62.9 MB (estimated)
------------------------------------------------
fillseq      :       5.334 micros/op;   20.7 MB/s
fillsync     :   41386.875 micros/op;    0.0 MB/s (1000 ops)
fillrandom   :       9.583 micros/op;   11.5 MB/s
overwrite    :      15.441 micros/op;    7.2 MB/s
readrandom   :      12.136 micros/op; (1000000 of 1000000 found)
readrandom   :       8.612 micros/op; (1000000 of 1000000 found)
readseq      :       0.303 micros/op;  365.1 MB/s
readreverse  :       0.560 micros/op;  197.5 MB/s
compact      : 2394003.000 micros/op;
readrandom   :       6.504 micros/op; (1000000 of 1000000 found)
readseq      :       0.271 micros/op;  407.5 MB/s
readreverse  :       0.515 micros/op;  214.7 MB/s
fill100K     :    4793.916 micros/op;   19.9 MB/s (1000 ops)
crc32c       :       3.709 micros/op; 1053.2 MB/s (4K per op)
snappycomp   :       9.545 micros/op;  409.3 MB/s (output: 55.1%)
snappyuncomp :       1.506 micros/op; 2593.9 MB/s
acquireload  :       0.349 micros/op; (each op is 1000 loads)
(10:51) jnml@fsc-r550:~/src/code.google.com/p/leveldb$

*/

package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"time"

	"camlistore.org/third_party/github.com/cznic/bufs"
	"camlistore.org/third_party/github.com/cznic/exp/lldb"
)

const (
	N = 1e6
)

var (
	value100 = []byte("Here is a performance report (with explanatioaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	memprofile = flag.String("memprofile", "", "write memory profile to this file")
)

func main() {
	flag.Parse()
	log.SetFlags(log.Lshortfile | log.Ltime)
	fmt.Printf(
		`lldb:      version exp
Keys:       16 bytes each
Values:     100 bytes each (50 bytes after compression)
Entries:    1000000
RawSize:    110.6 MB (estimated)
FileSize:   62.9 MB (estimated)
------------------------------------------------
`)
	if *memprofile != "" {
		runtime.MemProfileRate = 1
	}
	fillseq()
	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.WriteHeapProfile(f)
		f.Close()
		return
	}

}

func fillseq() {
	dbname := os.Args[0] + ".db"
	f, err := os.OpenFile(dbname, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0666)
	if err != nil {
		log.Fatal(err)
	}

	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()

	filer := lldb.NewSimpleFileFiler(f)
	a, err := lldb.NewAllocator(filer, &lldb.Options{})
	if err != nil {
		log.Println(err)
		return
	}

	a.Compress = true
	b, _, err := lldb.CreateBTree(a, nil)
	if err != nil {
		log.Println(err)
		return
	}

	var keys [N][16]byte
	for i := range keys {
		binary.BigEndian.PutUint32(keys[i][:], uint32(i))
	}

	debug.FreeOSMemory()
	t0 := time.Now()
	for _, key := range keys {
		if err = b.Set(key[:], value100); err != nil {
			log.Println(err)
			return
		}
	}
	if err := filer.Sync(); err != nil {
		log.Println(err)
		return
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	d := time.Since(t0)
	fi, err := f.Stat()
	if err != nil {
		log.Println(err)
		return
	}

	secs := float64(d/time.Nanosecond) / float64(time.Second)
	sz := fi.Size()
	fmt.Printf("fillseq      :%19v/op;%7.1f MB/s (%g secs, %d bytes)\n", d/N, float64(sz)/secs/1e6, secs, sz)
	nn, bytes := bufs.GCache.Stats()
	fmt.Printf("%d %d\n", nn, bytes)
	fmt.Printf("%+v\n", ms)
}
