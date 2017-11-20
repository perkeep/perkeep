// Copyright 2014 The kv Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*

Command kvaudit verifies kv databases.

Installation:

    $ go get github.com/cznic/kv/kvaudit

Usage:

    kvaudit [-d] [-f key] [-l key] [-max n] [-s] [-v] file

Options:

	-b	analyze allocated/free blocks sizes vs FLT buckets

	-d	dump file to stdout in cdbmake[2] format even for empty -f and -l.

	-f key	dump from key, first existing if empty

	-l key	dump to key, last existing if empty

	-max	maximum number of errors to report. Default 10.	The actual
		number of reported errors, if any, may be less because many
		errors do not allow to reliably continue the audit.

	-s	List DB statistics

	-v	List every error in addition to the overall one.

Arguments:

	file	For example: ~/foo/bar.db

Implementation Notes

The performed verification is described at [0]. This tool was hacked quickly
to assist with resolving [1].

Known Issues

In this first release there's no file locking checked or enforced. The auditing
process will _not_ write to the DB, so this cannot introduce a DB corruption
(it's opened in R/O mode anyway).  However, if the DB is opened and updated by
another process, the reported errors may be caused only by the updates.

In other words, to use this initial version properly, you must manually ensure
that the audited database is not being updated by any other process.

Links

Referenced from above:

  [0]: http://godoc.org/github.com/cznic/lldb#Allocator.Verify
  [1]: https://code.google.com/p/camlistore/issues/detail?id=216
  [2]: http://cr.yp.to/cdb/cdbmake.html

*/
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"github.com/cznic/exp/lldb"
)

func rep(s string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, s, a...)
}

func null(s string, a ...interface{}) {}

func main() {
	oBuckets := flag.Bool("b", false, "show FLT buckets analysis")
	oDump := flag.Bool("d", false, "send a cdbmake formatted dump to stdout even if -f and -l is empty")
	oFirst := flag.String("f", "", "first key to dump (if non empty)")
	oLast := flag.String("l", "", "last key to dump (if non empty)")
	oMax := flag.Uint("max", 10, "errors reported limit.")
	oStat := flag.Bool("s", false, "show DB stats.")
	oVerbose := flag.Bool("v", false, "verbose mode.")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Need exactly one file argument")
		os.Exit(1)
	}

	if *oStat {
		*oVerbose = true
	}

	r := rep
	if !*oVerbose {
		r = null
	}

	if err := main0(flag.Arg(0), int(*oMax), r, *oStat, *oFirst, *oLast,
		*oDump, *oBuckets); err != nil {
		fmt.Fprintf(os.Stderr, "kvaudit: %v\n", err)
		os.Exit(1)
	}
}

func main0(fn string, oMax int, w func(s string, a ...interface{}), oStat bool, first, last string, dump bool, oBuckets bool) error {
	f, err := os.Open(fn) // O_RDONLY
	if err != nil {
		return err
	}

	defer f.Close()

	bits, err := ioutil.TempFile("", "kvaudit-")
	if err != nil {
		return err
	}

	defer func() {
		nm := bits.Name()
		bits.Close()
		os.Remove(nm)
	}()

	a, err := lldb.NewAllocator(lldb.NewInnerFiler(lldb.NewSimpleFileFiler(f), 16), &lldb.Options{})
	if err != nil {
		return err
	}

	cnt := 0
	var stats lldb.AllocStats
	err = a.Verify(lldb.NewSimpleFileFiler(bits), func(err error) bool {
		cnt++
		w("%d: %v\n", cnt, err)
		return cnt < oMax
	}, &stats)
	if err != nil {
		return err
	}

	if oStat {
		w("Handles     %10d  // total valid handles in use\n", stats.Handles)
		w("Compression %10d  // number of compressed blocks\n", stats.Compression)
		w("TotalAtoms  %10d  // total number of atoms == AllocAtoms + FreeAtoms\n", stats.TotalAtoms)
		w("AllocBytes  %10d  // bytes allocated (after decompression, if/where used)\n", stats.AllocBytes)
		w("AllocAtoms  %10d  // atoms allocated/used, including relocation atoms\n", stats.AllocAtoms)
		w("Relocations %10d  // number of relocated used blocks\n", stats.Relocations)
		w("FreeAtoms   %10d  // atoms unused\n", stats.FreeAtoms)
	}
	if oBuckets {
		var alloc, free [14]int64
		sizes := [14]int64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 4112}
		for atoms, cnt := range stats.AllocMap {
			if atoms >= 4096 {
				alloc[13] += cnt
				continue
			}

			for i := range sizes {
				if sizes[i+1] > atoms {
					alloc[i] += cnt
					break
				}
			}
		}
		for atoms, cnt := range stats.FreeMap {
			if atoms > 4096 {
				free[13] += cnt
				continue
			}

			for i := range sizes {
				if sizes[i+1] > atoms {
					free[i] += cnt
					break
				}
			}
		}
		w("Alloc blocks\n")
		for i, v := range alloc {
			w("%4d: %10d\n", sizes[i], v)
		}
		w("Free blocks\n")
		for i, v := range free {
			w("%4d: %10d\n", sizes[i], v)
		}
	}

	if !(first != "" || last != "" || dump) {
		return nil
	}

	t, err := lldb.OpenBTree(a, nil, 1)
	if err != nil {
		return err
	}

	dw := bufio.NewWriter(os.Stdout)
	defer dw.Flush()

	var e *lldb.BTreeEnumerator
	switch {
	case first != "":
		e, _, err = t.Seek([]byte(first))
	default:
		e, err = t.SeekFirst()
	}
	if err != nil {
		if err == io.EOF {
			err = nil
		}
		return err
	}

	blast := []byte(last)
	sep := []byte("->")
	for {
		k, v, err := e.Next()
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return err
		}

		dw.WriteString(fmt.Sprintf("+%d,%d:", len(k), len(v)))
		dw.Write(k)
		dw.Write(sep)
		dw.Write(v)
		dw.WriteByte('\n')

		if len(blast) != 0 && bytes.Compare(k, blast) >= 0 {
			break
		}
	}

	return nil
}
