// Copyright 2016 The Internal Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package buffer

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path"
	"runtime"
	"strings"
	"testing"
)

func caller(s string, va ...interface{}) {
	if s == "" {
		s = strings.Repeat("%v ", len(va))
	}
	_, fn, fl, _ := runtime.Caller(2)
	fmt.Fprintf(os.Stderr, "caller: %s:%d: ", path.Base(fn), fl)
	fmt.Fprintf(os.Stderr, s, va...)
	fmt.Fprintln(os.Stderr)
	_, fn, fl, _ = runtime.Caller(1)
	fmt.Fprintf(os.Stderr, "\tcallee: %s:%d: ", path.Base(fn), fl)
	fmt.Fprintln(os.Stderr)
	os.Stderr.Sync()
}

func dbg(s string, va ...interface{}) {
	if s == "" {
		s = strings.Repeat("%v ", len(va))
	}
	_, fn, fl, _ := runtime.Caller(1)
	fmt.Fprintf(os.Stderr, "dbg %s:%d: ", path.Base(fn), fl)
	fmt.Fprintf(os.Stderr, s, va...)
	fmt.Fprintln(os.Stderr)
	os.Stderr.Sync()
}

func TODO(...interface{}) string { //TODOOK
	_, fn, fl, _ := runtime.Caller(1)
	return fmt.Sprintf("TODO: %s:%d:\n", path.Base(fn), fl) //TODOOK
}

func use(...interface{}) {}

func init() {
	use(caller, dbg, TODO) //TODOOK
}

// ============================================================================

func test(t testing.TB, allocs, goroutines int) {
	ready := make(chan int, goroutines)
	run := make(chan int)
	done := make(chan int, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			a := rand.Perm(allocs)
			ready <- 1
			<-run
			for _, v := range a {
				p := Get(v)
				b := *p
				if g, e := len(b), v; g != e {
					t.Error(g, e)
					break
				}

				Put(p)
			}
			done <- 1
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-ready
	}
	close(run)
	for i := 0; i < goroutines; i++ {
		<-done
	}
}

func Test2(t *testing.T) {
	test(t, 1<<15, 32)
}

func Benchmark1(b *testing.B) {
	const (
		allocs     = 1000
		goroutines = 100
	)
	for i := 0; i < b.N; i++ {
		test(b, allocs, goroutines)
	}
	b.SetBytes(goroutines * (allocs*allocs + allocs) / 2)
}

func TestBytes(t *testing.T) {
	const N = 1e6
	src := make([]byte, N)
	for i := range src {
		src[i] = byte(rand.Int())
	}
	var b Bytes
	for i, v := range src {
		b.WriteByte(v)
		if g, e := b.Len(), i+1; g != e {
			t.Fatal("Len", g, e)
		}
	}

	if !bytes.Equal(b.Bytes(), src) {
		t.Fatal("content differs")
	}

	b.Close()
	if g, e := b.Len(), 0; g != e {
		t.Fatal("Len", g, e)
	}

	if g, e := len(b.Bytes()), 0; g != e {
		t.Fatal("Bytes", g, e)
	}

	s2 := src
	for len(s2) != 0 {
		n := rand.Intn(127)
		if n > len(s2) {
			n = len(s2)
		}
		n2, _ := b.Write(s2[:n])
		if g, e := n2, n; g != e {
			t.Fatal("Write", g, e)
		}

		s2 = s2[n:]
	}

	if !bytes.Equal(b.Bytes(), src) {
		t.Fatal("content differs")
	}

	b.Close()
	s2 = src
	for len(s2) != 0 {
		n := rand.Intn(31)
		if n > len(s2) {
			n = len(s2)
		}
		n2, _ := b.WriteString(string(s2[:n]))
		if g, e := n2, n; g != e {
			t.Fatal("Write", g, e)
		}

		s2 = s2[n:]
	}

	if !bytes.Equal(b.Bytes(), src) {
		t.Fatal("content differs")
	}
}

func benchmarkWriteByte(b *testing.B, n int) {
	for i := 0; i < b.N; i++ {
		var buf Bytes
		for j := 0; j < n; j++ {
			buf.WriteByte(0)
		}
		buf.Close()
	}
	b.SetBytes(int64(n))
}

func BenchmarkWriteByte_1e3(b *testing.B) { benchmarkWriteByte(b, 1e3) }
func BenchmarkWriteByte_1e4(b *testing.B) { benchmarkWriteByte(b, 1e4) }
func BenchmarkWriteByte_1e5(b *testing.B) { benchmarkWriteByte(b, 1e5) }
func BenchmarkWriteByte_1e6(b *testing.B) { benchmarkWriteByte(b, 1e6) }
func BenchmarkWriteByte_1e7(b *testing.B) { benchmarkWriteByte(b, 1e7) }
