// Copyright 2014 The dbm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dbm

import (
	"bytes"
	"fmt"
	"io"

	"camlistore.org/third_party/github.com/cznic/exp/lldb"
	"camlistore.org/third_party/github.com/cznic/fileutil"
	"camlistore.org/third_party/github.com/cznic/mathutil"
)

/*

File benchmarks
---------------
pgBits: 10
BenchmarkFileWrSeq	     100	  63036813 ns/op	   0.51 MB/s
BenchmarkFileRdSeq	     100	  26363452 ns/op	   1.21 MB/s

pgBits: 11
BenchmarkFileWrSeq	     100	  26437121 ns/op	   1.21 MB/s
BenchmarkFileRdSeq	     200	  13490639 ns/op	   2.37 MB/s

pgBits: 12
BenchmarkFileWrSeq	     200	  17363191 ns/op	   1.84 MB/s
BenchmarkFileRdSeq	     500	   8960257 ns/op	   3.57 MB/s

pgBits: 13
BenchmarkFileWrSeq	     500	  10005011 ns/op	   3.20 MB/s
BenchmarkFileRdSeq	    1000	   3328100 ns/op	   9.62 MB/s

pgBits: 14
BenchmarkFileWrSeq	     500	   6414419 ns/op	   4.99 MB/s
BenchmarkFileRdSeq	    1000	   1877981 ns/op	  17.04 MB/s

pgBits: 15
BenchmarkFileWrSeq	     500	   4991456 ns/op	   6.41 MB/s
BenchmarkFileRdSeq	    1000	   1144174 ns/op	  27.97 MB/s

pgBits: 16
BenchmarkFileWrSeq	     500	   5019710 ns/op	   6.37 MB/s
BenchmarkFileRdSeq	    2000	   1003166 ns/op	  31.90 MB/s

Bits benchmarks
---------------
pgBits: 7
BenchmarkBitsOn16	    2000	   3598167 ns/op	   0.56 kB/s
BenchmarkBitsOn1024	    1000	   7736769 ns/op	  16.54 kB/s
BenchmarkBitsOn65536	     100	 123242143 ns/op	  66.47 kB/s

pgBits: 8
BenchmarkBitsOn16	    2000	   3735512 ns/op	   0.54 kB/s
BenchmarkBitsOn1024	    1000	   5131015 ns/op	  24.95 kB/s
BenchmarkBitsOn65536	     100	  50443447 ns/op	 162.40 kB/s

pgBits: 9
BenchmarkBitsOn16	    1000	   2681974 ns/op	   0.75 kB/s
BenchmarkBitsOn1024	    2000	   5708185 ns/op	  22.42 kB/s
BenchmarkBitsOn65536	     100	  25916396 ns/op	 316.09 kB/s

pgBits: 10
BenchmarkBitsOn16	    2000	   3931464 ns/op	   0.51 kB/s
BenchmarkBitsOn1024	    2000	   4757425 ns/op	  26.91 kB/s
BenchmarkBitsOn65536	     200	  14795335 ns/op	 553.69 kB/s

pgBits: 11
BenchmarkBitsOn16	    2000	   3917548 ns/op	   0.51 kB/s
BenchmarkBitsOn1024	    2000	   4294720 ns/op	  29.80 kB/s
BenchmarkBitsOn65536	     500	  12468406 ns/op	 657.02 kB/s

pgBits: 12
BenchmarkBitsOn16	    1000	   2883289 ns/op	   0.69 kB/s
BenchmarkBitsOn1024	    1000	   3094400 ns/op	  41.37 kB/s
BenchmarkBitsOn65536	     500	   8869794 ns/op	 923.58 kB/s

pgBits: 13
BenchmarkBitsOn16	    1000	   3216570 ns/op	   0.62 kB/s
BenchmarkBitsOn1024	    1000	   3329923 ns/op	  38.44 kB/s
BenchmarkBitsOn65536	     500	   7135497 ns/op	1148.06 kB/s

pgBits: 14
BenchmarkBitsOn16	    1000	   3883990 ns/op	   0.51 kB/s
BenchmarkBitsOn1024	    1000	   3828543 ns/op	  33.43 kB/s
BenchmarkBitsOn65536	     500	   5282395 ns/op	1550.81 kB/s

pgBits: 15
BenchmarkBitsOn16	     500	   4054525 ns/op	   0.49 kB/s
BenchmarkBitsOn1024	     500	   4126241 ns/op	  31.02 kB/s
BenchmarkBitsOn65536	     500	   5782308 ns/op	1416.74 kB/s

pgBits: 16
BenchmarkBitsOn16	     500	   6809287 ns/op	   0.29 kB/s
BenchmarkBitsOn1024	     500	   6941766 ns/op	  18.44 kB/s
BenchmarkBitsOn65536	     500	   8043347 ns/op	1018.48 kB/s
*/

const (
	fSize = "size"

	pgBits = 16
	pgSize = 1 << pgBits
	pgMask = pgSize - 1
)

var (
	bfSize = []byte(fSize)
)

// File is a database blob with a file-like API. Values in Arrays are limited
// in size to about 64kB. To put a larger value into an Array, the value can be
// written to a File and the path stored in the Array instead of the too big
// value.
type File Array

// As os.File.Name().
func (f *File) Name() string {
	return f.name
}

// As os.File.FileInfo().Size().
func (f *File) Size() (sz int64, err error) {
	if err = f.db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		f.db.leave(&err)
	}()

	if ok, err := (*Array)(f).validate(false); !ok {
		return 0, err
	}

	return f.size()
}

func (f *File) size() (sz int64, err error) {
	a := (*Array)(f)
	v, err := a.get(fSize)
	if err != nil {
		return
	}

	switch x := v.(type) {
	case int64:
		return x, nil
	case nil:
		return
	}

	return 0, &lldb.ErrINVAL{Src: "dbm.File.Size", Val: v}
}

// PunchHole deallocates space inside a "file" in the byte range starting at
// off and continuing for size bytes.  The Filer size (as reported by `Size()`
// does not change when hole punching, even when puching the end of a file off.
func (f *File) PunchHole(off, size int64) (err error) {
	if off < 0 {
		return &lldb.ErrINVAL{Src: f.Name() + ":PunchHole off", Val: off}
	}

	fsize, err := f.Size()
	if err != nil {
		return
	}

	if size < 0 || off+size > fsize {
		return &lldb.ErrINVAL{Src: f.Name() + ":PunchHole size", Val: size}
	}

	first := off >> pgBits
	if off&pgMask != 0 {
		first++
	}
	off += size - 1
	last := off >> pgBits
	if off&pgMask != 0 {
		last--
	}
	if limit := fsize >> pgBits; last > limit {
		last = limit
	}
	for pg := first; pg <= last; pg++ {
		if err = (*Array)(f).Delete(pg); err != nil {
			return
		}
	}
	return
}

var zeroPage [pgSize]byte

// As os.File.ReadAt.
func (f *File) ReadAt(b []byte, off int64) (n int, err error) {
	return f.readAt(b, off, false)
}

func (f *File) readAt(b []byte, off int64, bits bool) (n int, err error) {
	var fsize int64
	if !bits {
		fsize, err = f.Size()
		if err != nil {
			return
		}
	}

	avail := fsize - off
	pgI := off >> pgBits
	pgO := int(off & pgMask)
	rem := len(b)
	if !bits && int64(rem) >= avail {
		rem = int(avail)
		err = io.EOF
	}
	for rem != 0 && (bits || avail > 0) {
		v, err := (*Array)(f).Get(pgI)
		if err != nil {
			return n, err
		}

		pg, _ := v.([]byte)
		if len(pg) == 0 {
			pg = zeroPage[:]
		}

		nc := copy(b[:mathutil.Min(rem, pgSize)], pg[pgO:])
		pgI++
		pgO = 0
		rem -= nc
		n += nc
		b = b[nc:]
	}
	return
}

// As os.File.WriteAt().
func (f *File) WriteAt(b []byte, off int64) (n int, err error) {
	return f.writeAt(b, off, false)
}

func (f *File) writeAt(b []byte, off int64, bits bool) (n int, err error) {
	var fsize int64
	a := (*Array)(f)
	if !bits {
		fsize, err = f.Size()
		if err != nil {
			return
		}
	}

	pgI := off >> pgBits
	pgO := int(off & pgMask)
	rem := len(b)
	var nc int
	for rem != 0 {
		if pgO == 0 && rem >= pgSize && bytes.Equal(b[:pgSize], zeroPage[:]) {
			if err = a.Delete(pgI); err != nil {
				return
			}

			nc = pgSize
			n += nc
		} else {
			v, err := a.Get(pgI)
			if err != nil {
				return n, err
			}

			pg, _ := v.([]byte)
			if len(pg) == 0 {
				pg = make([]byte, pgSize)
			}

			nc = copy(pg[pgO:], b)
			n += nc
			if err = a.Set(pg, pgI); err != nil {
				return n, err
			}

		}
		pgI++
		pgO = 0
		rem -= nc
		b = b[nc:]
	}
	if !bits {
		if newSize := mathutil.MaxInt64(fsize, off+int64(n)); newSize != fsize {
			return n, a.Set(newSize, fSize)
		}
	}

	return
}

// As os.File.Truncate().
func (f *File) Truncate(size int64) (err error) {
	if err = f.db.enter(); err != nil {
		return
	}

	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	a := (*Array)(f)
	switch {
	case size < 0:
		if f.db.leave(&err); err != nil {
			return
		}

		return &lldb.ErrINVAL{Src: "dbm.File.Truncate size", Val: size}
	case size == 0:
		if f.db.leave(&err); err != nil {
			return
		}

		return a.Clear()
	}

	if f.db.leave(&err) != nil {
		return
	}

	first := size >> pgBits
	if size&pgMask != 0 {
		first++
	}

	fsize, err := f.Size()
	if err != nil {
		return
	}

	last := fsize >> pgBits
	if fsize&pgMask != 0 {
		last++
	}
	for ; first < last; first++ {
		if err = a.Delete(first); err != nil {
			return
		}
	}

	return a.Set(size, fSize)
}

// ReadFrom is a helper to populate File's content from r.  'n' reports the
// number of bytes read from 'r'.
func (f *File) ReadFrom(r io.Reader) (n int64, err error) {
	if err = f.Truncate(0); err != nil {
		return
	}

	var (
		b   [pgSize]byte
		rn  int
		off int64
	)

	var rerr error
	for rerr == nil {
		if rn, rerr = r.Read(b[:]); rn != 0 {
			f.WriteAt(b[:rn], off)
			off += int64(rn)
			n += int64(rn)
		}
	}
	if !fileutil.IsEOF(rerr) {
		err = rerr
	}
	return
}

// WriteTo is a helper to copy/persist File's content to w.  If w is also
// an io.WriterAt then WriteTo may attempt to _not_ write any big, for some
// value of big, runs of zeros, i.e. it will attempt to punch holes, where
// possible, in `w` if that happens to be a freshly created or to zero length
// truncated OS file.  'n' reports the number of bytes written to 'w'.
func (f *File) WriteTo(w io.Writer) (n int64, err error) {
	var (
		b      [pgSize]byte
		wn, rn int
		off    int64
		rerr   error
	)

	if wa, ok := w.(io.WriterAt); ok {
		fsize, err := f.Size()
		if err != nil {
			return n, err
		}

		lastPgI := fsize >> pgBits
		for pgI := int64(0); pgI <= lastPgI; pgI++ {
			sz := pgSize
			if pgI == lastPgI {
				sz = int(fsize & pgMask)
			}
			v, err := (*Array)(f).Get(pgI)
			if err != nil {
				return n, err
			}

			pg, _ := v.([]byte)
			if len(pg) != 0 {
				wn, err = wa.WriteAt(pg[:sz], off)
				if err != nil {
					return n, err
				}

				n += int64(wn)
				off += int64(sz)
				if wn != sz {
					return n, io.ErrShortWrite
				}
			}
		}
		return n, err
	}

	var werr error
	for rerr == nil {
		if rn, rerr = f.ReadAt(b[:], off); rn != 0 {
			off += int64(rn)
			if wn, werr = w.Write(b[:rn]); werr != nil {
				return n, werr
			}

			n += int64(wn)
		}
	}
	if !fileutil.IsEOF(rerr) {
		err = rerr
	}
	return
}

// Bits return a bitmap index backed by f.
func (f *File) Bits() *Bits {
	return &Bits{f: f, page: -1}
}
