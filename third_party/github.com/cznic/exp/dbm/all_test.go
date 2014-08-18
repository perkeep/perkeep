// Copyright 2014 The dbm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dbm

//DONE 2012-04-24 15:56 go test -race -cpu 4 -bench .
//DONE 2012-04-24 16:05 go test -race -cpu 4 -bench . -xact
//DONE 2012-04-24 16:25 go test -race -cpu 4 -bench . -wall

import (
	"bytes"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"camlistore.org/third_party/github.com/cznic/exp/lldb"
	"camlistore.org/third_party/github.com/cznic/fileutil"
)

var (
	oNoZip          = flag.Bool("nozip", false, "disable compression")
	oACIDEnableWAL  = flag.Bool("wal", false, "enable WAL")
	oACIDEnableXACT = flag.Bool("xact", false, "enable structural transactions")
	oACIDGrace      = flag.Duration("grace", time.Second, "Grace period for -wal")
	oBench          = flag.Bool("tbench", false, "enable (long) TestBench* tests")
)

// Bench knobs.
const (
	fileTestChunkSize = 32e3
	fileTotalSize     = 10e6
)

func init() {
	flag.Parse()
	compress = !*oNoZip
	if *oACIDEnableXACT {
		o.ACID = ACIDTransactions
	}
	if *oACIDEnableWAL {
		o.ACID = ACIDFull
		o.GracePeriod = *oACIDGrace
	}
}

func dbg(s string, va ...interface{}) {
	_, fn, fl, _ := runtime.Caller(1)
	fmt.Printf("%s:%d: ", path.Base(fn), fl)
	fmt.Printf(s, va...)
	fmt.Println()
}

func use(...interface{}) {}

var o = &Options{}

func temp() (dir, name string) {
	dir, err := ioutil.TempDir("", "test-dbm-")
	if err != nil {
		panic(err)
	}

	name = filepath.Join(dir, "test.db")
	return
}

func Test0(t *testing.T) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}

	if db, err = Open(dbname, o); err != nil {
		t.Error(err)
		return
	}

	if _, err = db.root(); err != nil {
		t.Error(err)
		return
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}

	if db, err = Open(dbname, o); err != nil {
		t.Error(err)
		return
	}

	if _, err = db.root(); err != nil {
		t.Error(err)
		return
	}

	var tr *lldb.BTree
	if tr, err = db.acache.getTree(db, arraysPrefix, "Test0", false, aCacheSize); err != nil {
		t.Error(err)
		return
	}

	if tr != nil {
		t.Error(tr)
		return
	}

	if err = db.filer.BeginUpdate(); err != nil {
		t.Error(tr)
		return
	}

	if tr, err = db.acache.getTree(db, arraysPrefix, "Test0", true, aCacheSize); err != nil {
		t.Error(err)
		return
	}

	if err = db.filer.EndUpdate(); err != nil {
		t.Error(tr)
		return
	}

	if tr == nil {
		t.Error(tr)
		return
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}

	if db, err = Open(dbname, o); err != nil {
		t.Error(err)
		return
	}

	if err = db.filer.BeginUpdate(); err != nil {
		t.Error(tr)
		return
	}

	if tr, err = db.acache.getTree(db, arraysPrefix, "Test0", true, aCacheSize); err != nil {
		t.Error(err)
		return
	}

	if err = db.filer.EndUpdate(); err != nil {
		t.Error(tr)
		return
	}

	if tr == nil {
		t.Error(tr)
		return
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}
}

func TestSet0(t *testing.T) {
	N := 4000
	if *oACIDEnableWAL {
		N = 4000
	}

	dir, dbname := temp()
	defer os.RemoveAll(dir)

	rng := rand.New(rand.NewSource(42))
	ref := map[int]int{}

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < N; i++ {
		k, v := rng.Int(), rng.Int()
		ref[k] = v
		if err := db.Set(v, "TestSet0", k); err != nil {
			t.Fatal(err)
		}
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}

	if db, err = Open(dbname, o); err != nil {
		t.Error(err)
		return
	}

	for k, v := range ref {
		val, err := db.Get("TestSet0", k)
		if err != nil {
			t.Error(err)
			return
		}

		switch x := val.(type) {
		case int64:
			if g, e := x, int64(v); g != e {
				t.Error(g, e)
				return
			}
		default:
			t.Errorf("%T != int64", x)
		}
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}
}

func TestDocEx(t *testing.T) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	var g, e interface{}

	dump := func(name string, clear bool) {
		array, err := db.Array(name)
		if err != nil {
			t.Fatal(err)
		}

		s, err := dump(array.tree)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("\nDump of %q\n%s", name, s)

		if clear {
			if err = array.Clear(); err != nil {
				t.Fatal(err)
			}
		}

	}

	db.Set(3, "Stock", "slip dress", 4, "blue", "floral")

	g, _ = db.Get("Stock", "slip dress", 4, "blue", "floral") // → 3
	if e = int64(3); g != e {
		t.Error(g, e)
		return
	}

	dump("Stock", true)

	stock, _ := db.Array("Stock")
	stock.Set(3, "slip dress", 4, "blue", "floral")

	g, _ = db.Get("Stock", "slip dress", 4, "blue", "floral") // → 3
	if e = int64(3); g != e {
		t.Error(g, e)
		return
	}

	g, _ = stock.Get("slip dress", 4, "blue", "floral") // → 3
	if e = int64(3); g != e {
		t.Error(g, e)
		return
	}

	dump("Stock", true)

	blueDress, _ := db.Array("Stock", "slip dress", 4, "blue")
	blueDress.Set(3, "floral")

	g, _ = db.Get("Stock", "slip dress", 4, "blue", "floral") // → 3
	if e = int64(3); g != e {
		t.Error(g, e)
		return
	}

	g, _ = blueDress.Get("floral") // → 3
	if e = int64(3); g != e {
		t.Error(g, e)
		return
	}

	dump("Stock", true)

	parts := []struct{ num, qty, price int }{
		{100001, 2, 300},
		{100004, 5, 600},
	}
	invoiceNum := 314159
	customer := "Google"
	when := time.Now().UnixNano()

	invoice, _ := db.Array("Invoice")
	invoice.Set(when, invoiceNum, "Date")
	invoice.Set(customer, invoiceNum, "Customer")
	invoice.Set(len(parts), invoiceNum, "Items") // # of Items in the invoice
	for i, part := range parts {
		invoice.Set(part.num, invoiceNum, "Items", i, "Part")
		invoice.Set(part.qty, invoiceNum, "Items", i, "Quantity")
		invoice.Set(part.price, invoiceNum, "Items", i, "Price")
	}

	g, _ = db.Get("Invoice", invoiceNum, "Customer") // → customer
	if e = customer; g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	g, _ = db.Get("Invoice", invoiceNum, "Date") // → time.Then().UnixName
	if e = when; g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	g, _ = invoice.Get(invoiceNum, "Customer") // → customer
	if e = customer; g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	g, _ = invoice.Get(invoiceNum, "Date") // → time.Then().UnixName
	if e = when; g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	g, _ = invoice.Get(invoiceNum, "Items") // → len(parts)
	if e = int64(len(parts)); g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	for i, part := range parts {
		g, _ = invoice.Get(invoiceNum, "Items", i, "Part") // → part[0].part
		if e = int64(part.num); g != e {
			t.Errorf("|%#v| |%#v|", g, e)
			return
		}

		g, _ = invoice.Get(invoiceNum, "Items", i, "Quantity") // → part[0].qty
		if e = int64(part.qty); g != e {
			t.Errorf("|%#v| |%#v|", g, e)
			return
		}

		g, _ = invoice.Get(invoiceNum, "Items", i, "Price") // → part[0].price
		if e = int64(part.price); g != e {
			t.Errorf("|%#v| |%#v|", g, e)
			return
		}
	}

	dump("Invoice", true)

	invoice, _ = db.Array("Invoice", invoiceNum)
	invoice.Set(when, "Date")
	invoice.Set(customer, "Customer")
	items, _ := invoice.Array("Items")
	items.Set(len(parts)) // # of Items in the invoice
	for i, part := range parts {
		items.Set(part.num, i, "Part")
		items.Set(part.qty, i, "Quantity")
		items.Set(part.price, i, "Price")
	}

	g, _ = db.Get("Invoice", invoiceNum, "Customer") // → customer
	if e = customer; g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	g, _ = db.Get("Invoice", invoiceNum, "Date") // → time.Then().UnixName
	if e = when; g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	g, _ = invoice.Get("Customer") // → customer
	if e = customer; g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	g, _ = invoice.Get("Date") // → time.Then().UnixName
	if e = when; g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	g, _ = items.Get() // → len(parts)
	if e = int64(len(parts)); g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	for i, part := range parts {
		g, _ = items.Get(i, "Part") // → parts[i].part
		if e = int64(part.num); g != e {
			t.Errorf("|%#v| |%#v|", g, e)
			return
		}

		g, _ = items.Get(i, "Quantity") // → part[0].qty
		if e = int64(part.qty); g != e {
			t.Errorf("|%#v| |%#v|", g, e)
			return
		}

		g, _ = items.Get(i, "Price") // → part[0].price
		if e = int64(part.price); g != e {
			t.Errorf("|%#v| |%#v|", g, e)
			return
		}
	}

	dump("Invoice", true)

	invoice, _ = db.Array("Invoice", invoiceNum)
	invoice.Set(when, "Date")
	invoice.Set(customer, "Customer")
	items, _ = invoice.Array("Items")
	items.Set(len(parts)) // # of Items in the invoice
	for i, part := range parts {
		items.Set([]interface{}{part.num, part.qty, part.price}, i)
	}

	dump("Invoice", false)

	g, _ = db.Get("Invoice", invoiceNum, "Customer") // → customer
	if e = customer; g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	g, _ = db.Get("Invoice", invoiceNum, "Date") // → time.Then().UnixName
	if e = when; g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	g, _ = invoice.Get("Customer") // → customer
	if e = customer; g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	g, _ = invoice.Get("Date") // → time.Then().UnixName
	if e = when; g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	g, _ = items.Get() // → len(parts)
	if e = int64(len(parts)); g != e {
		t.Errorf("|%#v| |%#v|", g, e)
		return
	}

	for i, part := range parts {
		g, _ = items.Get(i) // → []interface{parts[i].num, parts[0].qty, parts[i].price}
		gg, ok := g.([]interface{})
		if !ok || len(gg) != 3 {
			t.Error(g)
			return
		}

		if g, e = gg[0], int64(part.num); g != e {
			t.Errorf("|%#v| |%#v|", g, e)
			return
		}

		if g, e = gg[1], int64(part.qty); g != e {
			t.Errorf("|%#v| |%#v|", g, e)
			return
		}

		if g, e = gg[2], int64(part.price); g != e {
			t.Errorf("|%#v| |%#v|", g, e)
			return
		}
	}

	dump("Invoice", true)

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}
}

func dump(t *lldb.BTree) (r string, err error) {
	var b bytes.Buffer
	if err = t.Dump(&b); err != nil {
		if err = noEof(err); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("IsMem: %t\n%s", t.IsMem(), b.String()), nil
}

func strings2D(s string) (r [][]interface{}) {
	for _, v := range strings.Split(s, "|") {
		r = append(r, strings1D(v))
	}
	return
}

func strings1D(s string) (r []interface{}) {
	for _, v := range strings.Split(s, ",") {
		if v != "" {
			r = append(r, v)
		}
	}
	return
}

func TestSlice0(t *testing.T) {
	table := []struct{ prefix, keys, from, to, exp string }{
		// Slice.from == nil && Slice.to == nil
		{"", "", "", "", ""},                       // 0
		{"", "a", "", "", "a"},                     // 1
		{"", "a|b", "", "", "a|b"},                 // 2
		{"", "d|c", "", "", "c|d"},                 // 3
		{"", "a|a,b|a,c|b", "", "", "a|a,b|a,c|b"}, // 4

		// Slice.from == nil && Slice.to != nil
		{"", "", "", "a", ""},                       // 5
		{"", "m", "", "a", ""},                      // 6
		{"", "m", "", "m", "m"},                     // 7
		{"", "m", "", "z", "m"},                     // 8
		{"", "k|p", "", "a", ""},                    // 9
		{"", "k|p", "", "j", ""},                    // 10
		{"", "k|p", "", "k", "k"},                   // 11
		{"", "k|p", "", "l", "k"},                   // 12
		{"", "k|p", "", "o", "k"},                   // 13
		{"", "k|p", "", "p", "k|p"},                 // 14
		{"", "k|p", "", "q", "k|p"},                 // 15
		{"", "k|m|o", "", "j", ""},                  // 16
		{"", "k|m|o", "", "k", "k"},                 // 17
		{"", "k|m|o", "", "l", "k"},                 // 18
		{"", "k|m|o", "", "m", "k|m"},               // 19
		{"", "k|m|o", "", "n", "k|m"},               // 20
		{"", "k|m|o", "", "o", "k|m|o"},             // 21
		{"", "k|m|o", "", "p", "k|m|o"},             // 22
		{"", "k|k,m|k,o|p", "", "j", ""},            // 23
		{"", "k|k,m|k,o|p", "", "k", "k"},           // 24
		{"", "k|k,m|k,o|p", "", "k,l", "k"},         // 25
		{"", "k|k,m|k,o|p", "", "k,m", "k|k,m"},     // 26
		{"", "k|k,m|k,o|p", "", "k,n", "k|k,m"},     // 27
		{"", "k|k,m|k,o|p", "", "k,o", "k|k,m|k,o"}, // 28
		{"", "k|k,m|k,o|p", "", "k,z", "k|k,m|k,o"}, // 29
		{"", "k|k,m|k,o|p", "", "o", "k|k,m|k,o"},   // 30
		{"", "k|k,m|k,o|p", "", "p", "k|k,m|k,o|p"}, // 31
		{"", "k|k,m|k,o|p", "", "q", "k|k,m|k,o|p"}, // 32

		// Slice.from != nil && Slice.to == nil
		{"", "", "m", "", ""},                       // 33
		{"", "a", "0", "", "a"},                     // 34
		{"", "a", "a", "", "a"},                     // 35
		{"", "a", "b", "", ""},                      // 36
		{"", "a|c", "0", "", "a|c"},                 // 37
		{"", "a|c", "a", "", "a|c"},                 // 38
		{"", "a|c", "b", "", "c"},                   // 39
		{"", "a|c", "c", "", "c"},                   // 40
		{"", "a|c", "d", "", ""},                    // 41
		{"", "k|k,m|k,o|p", "j", "", "k|k,m|k,o|p"}, // 42
		{"", "k|k,m|k,o|p", "k", "", "k|k,m|k,o|p"}, // 43
		{"", "k|k,m|k,o|p", "k,l", "", "k,m|k,o|p"}, // 44
		{"", "k|k,m|k,o|p", "k,m", "", "k,m|k,o|p"}, // 45
		{"", "k|k,m|k,o|p", "k,n", "", "k,o|p"},     // 46
		{"", "k|k,m|k,o|p", "k,z", "", "p"},         // 47
		{"", "k|k,m|k,o|p", "o", "", "p"},           // 48
		{"", "k|k,m|k,o|p", "p", "", "p"},           // 49
		{"", "k|k,m|k,o|p", "q", "", ""},            // 50

		// Slice.from != nil && Slice.to != nil
		{"", "", "m", "p", ""},

		{"", "b|d|e", "a", "a", ""},
		{"", "b|d|e", "a", "b", "b"},
		{"", "b|d|e", "a", "c", "b"},
		{"", "b|d|e", "a", "d", "b|d"},
		{"", "b|d|e", "a", "e", "b|d|e"},
		{"", "b|d|e", "a", "f", "b|d|e"},

		{"", "b|d|e", "b", "a", ""},
		{"", "b|d|e", "b", "b", "b"},
		{"", "b|d|e", "b", "c", "b"},
		{"", "b|d|e", "b", "d", "b|d"},
		{"", "b|d|e", "b", "e", "b|d|e"},
		{"", "b|d|e", "b", "f", "b|d|e"},

		{"", "b|d|e", "c", "a", ""},
		{"", "b|d|e", "c", "b", ""},
		{"", "b|d|e", "c", "c", ""},
		{"", "b|d|e", "c", "d", "d"},
		{"", "b|d|e", "c", "e", "d|e"},
		{"", "b|d|e", "c", "f", "d|e"},

		{"", "b|d|e", "d", "a", ""},
		{"", "b|d|e", "d", "b", ""},
		{"", "b|d|e", "d", "c", ""},
		{"", "b|d|e", "d", "d", "d"},
		{"", "b|d|e", "d", "e", "d|e"},
		{"", "b|d|e", "d", "f", "d|e"},

		{"", "b|d|e", "d", "a", ""},
		{"", "b|d|e", "d", "b", ""},
		{"", "b|d|e", "d", "c", ""},
		{"", "b|d|e", "d", "d", "d"},
		{"", "b|d|e", "d", "e", "d|e"},
		{"", "b|d|e", "d", "f", "d|e"},

		{"", "b|d|e", "e", "a", ""},
		{"", "b|d|e", "e", "b", ""},
		{"", "b|d|e", "e", "c", ""},
		{"", "b|d|e", "e", "d", ""},
		{"", "b|d|e", "e", "e", "e"},
		{"", "b|d|e", "e", "f", "e"},

		{"", "b|d|e", "f", "a", ""},
		{"", "b|d|e", "f", "b", ""},
		{"", "b|d|e", "f", "c", ""},
		{"", "b|d|e", "f", "d", ""},
		{"", "b|d|e", "f", "e", ""},
		{"", "b|d|e", "f", "f", ""},

		// more levels
		{"", "b|d,f|h,j|l", "a", "a", ""},
		{"", "b|d,f|h,j|l", "a", "z", "b|d,f|h,j|l"},
		{"", "b|d,f|h,j|l", "c", "k", "d,f|h,j"},

		// w/ prefix
		{"B", "", "M", "P", ""},
		{"B", "", "A", "Z", ""},

		{"B", "D|E", "", "", "D|E"},
		{"B", "D|E", "", "A", ""},
		{"B", "D|E", "", "B", ""},
		{"B", "D|E", "", "C", ""},
		{"B", "D|E", "", "D", "D"},
		{"B", "D|E", "", "E", "D|E"},
		{"B", "D|E", "", "F", "D|E"},

		{"B", "D|E", "A", "", "D|E"},
		{"B", "D|E", "A", "A", ""},
		{"B", "D|E", "A", "B", ""},
		{"B", "D|E", "A", "C", ""},
		{"B", "D|E", "A", "D", "D"},
		{"B", "D|E", "A", "E", "D|E"},
		{"B", "D|E", "A", "F", "D|E"},

		{"B", "D|E", "B", "", "D|E"},
		{"B", "D|E", "B", "A", ""},
		{"B", "D|E", "B", "B", ""},
		{"B", "D|E", "B", "C", ""},
		{"B", "D|E", "B", "D", "D"},
		{"B", "D|E", "B", "E", "D|E"},
		{"B", "D|E", "B", "F", "D|E"},

		{"B", "D|E", "C", "", "D|E"},
		{"B", "D|E", "C", "A", ""},
		{"B", "D|E", "C", "B", ""},
		{"B", "D|E", "C", "C", ""},
		{"B", "D|E", "C", "D", "D"},
		{"B", "D|E", "C", "E", "D|E"},
		{"B", "D|E", "C", "F", "D|E"},

		{"B", "D|E", "D", "", "D|E"},
		{"B", "D|E", "D", "A", ""},
		{"B", "D|E", "D", "B", ""},
		{"B", "D|E", "D", "C", ""},
		{"B", "D|E", "D", "D", "D"},
		{"B", "D|E", "D", "E", "D|E"},
		{"B", "D|E", "D", "F", "D|E"},

		{"B", "D|E", "E", "", "E"},
		{"B", "D|E", "E", "A", ""},
		{"B", "D|E", "E", "B", ""},
		{"B", "D|E", "E", "C", ""},
		{"B", "D|E", "E", "D", ""},
		{"B", "D|E", "E", "E", "E"},
		{"B", "D|E", "E", "F", "E"},

		{"B", "D|E", "F", "", ""},
		{"B", "D|E", "F", "A", ""},
		{"B", "D|E", "F", "B", ""},
		{"B", "D|E", "F", "C", ""},
		{"B", "D|E", "F", "D", ""},
		{"B", "D|E", "F", "E", ""},
		{"B", "D|E", "F", "F", ""},
	}

	for i, test := range table {
		prefix := strings1D(test.prefix)
		keys := strings2D(test.keys)
		from := strings1D(test.from)
		to := strings1D(test.to)
		exp := test.exp

		a0, _ := MemArray()

		a, err := a0.Array(prefix...)
		if err != nil {
			t.Fatal(err)
		}

		if test.prefix != "" {
			a0.Set(-1, "@")
			a0.Set(-1, "Z")
		}
		for i, v := range keys {
			if err = a.Set(i, v...); err != nil {
				t.Error(err)
				return
			}
		}
		d, err := dump(a.tree)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("%d: %q, %q, dump:\n%s", i, test.prefix, test.keys, d)

		s, err := a.Slice(from, to)
		if err != nil {
			t.Fatal(err)
		}

		var ga []string

		if err := s.Do(func(k, v []interface{}) (more bool, err error) {
			a := []string{}
			for _, v := range k {
				a = append(a, v.(string))
			}
			ga = append(ga, strings.Join(a, ","))
			return true, nil
		}); err != nil {
			if !fileutil.IsEOF(err) {
				t.Fatal(i, err)
			}
		}

		g := strings.Join(ga, "|")
		t.Logf("%q", g)
		if g != exp {
			t.Fatalf("%d\n%s\n%s", i, g, exp)
		}
	}
}

func TestSlice1(t *testing.T) {
	f := func(s, val []interface{}) (k, v string) {
		if len(s) != 1 || len(val) != 1 {
			t.Fatal(s, val)
		}

		k, ok := s[0].(string)
		if !ok {
			t.Fatal(s)
		}

		v, ok = val[0].(string)
		if !ok {
			t.Fatal(val)
		}

		return
	}

	a0, err := MemArray()
	if err != nil {
		t.Fatal(err)
	}

	a, err := a0.Array("b")
	if err != nil {
		t.Fatal(err)
	}

	a.Set("1", "d")
	a.Set("2", "f")

	d, err := dump(a0.tree)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("\n%s", d)

	s, err := a.Slice(nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	state := 0
	err = s.Do(func(s, val []interface{}) (bool, error) {
		k, v := f(s, val)
		switch state {
		case 0:
			if k != "d" || v != "1" {
				t.Error(s, val)
				return false, nil
			}

			a.Set("3", k)
			state++
		case 1:
			if k != "f" || v != "2" {
				t.Error(s, val)
				return false, nil
			}

			a.Set("4", k)
			state++
		default:
			t.Error(state)
			return false, nil
		}
		return true, nil
	})

	if err != nil {
		t.Fatal(err)
	}

	if g, e := state, 2; g != e {
		t.Fatal(state)
	}

	d, err = dump(a0.tree)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("\n%s", d)

	v3, err := a0.Get("b", "d")
	if err != nil {
		t.Fatal(err)
	}

	if g, e := v3, interface{}("3"); g != e {
		t.Fatal(g, e)
	}

	v4, err := a0.Get("b", "f")
	if err != nil {
		t.Fatal(err)
	}

	if g, e := v4, interface{}("4"); g != e {
		t.Fatal(g, e)
	}
}

func TestClear(t *testing.T) {
	table := []struct{ prefix, keys, subscripts, exp string }{
		{"", "", "", ""},

		{"", "b", "", ""},
		{"", "b", "a", "b"},
		{"", "b", "b", ""},
		{"", "b", "c", "b"},

		{"", "b|d|f", "", ""},
		{"", "b|d|f", "a", "b|d|f"},
		{"", "b|d|f", "b", "d|f"},
		{"", "b|d|f", "c", "b|d|f"},
		{"", "b|d|f", "d", "b|f"},
		{"", "b|d|f", "e", "b|d|f"},
		{"", "b|d|f", "f", "b|d"},
		{"", "b|d|f", "g", "b|d|f"},

		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "", ""},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "a", "b,d|b,f|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "b", "d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "b,c", "b,d|b,f|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "b,d", "b,f|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "b,e", "b,d|b,f|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "b,f", "b,d|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "c", "b,d|b,f|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "d", "b,d|b,f|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "d,e", "b,d|b,f|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "d,f", "b,d|b,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "d,g", "b,d|b,f|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "d,h", "b,d|b,f|d,f|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "d,i", "b,d|b,f|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "e", "b,d|b,f|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "f", "b,d|b,f|d,f|d,h"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "f,g", "b,d|b,f|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "f,h", "b,d|b,f|d,f|d,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "f,i", "b,d|b,f|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "f,j", "b,d|b,f|d,f|d,h|f,h"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "f,k", "b,d|b,f|d,f|d,h|f,h|f,j"},
		{"", "b,d|b,f|d,f|d,h|f,h|f,j", "g", "b,d|b,f|d,f|d,h|f,h|f,j"},

		{"b", "", "", ""},
		{"b", "d", "c", "b,d"},
		{"b", "d", "d", ""},
		{"b", "d", "e", "b,d"},

		{"b", "d|f", "", ""},
		{"b", "d|f", "c", "b,d|b,f"},
		{"b", "d|f", "d", "b,f"},
		{"b", "d|f", "e", "b,d|b,f"},
		{"b", "d|f", "f", "b,d"},
		{"b", "d|f", "g", "b,d|b,f"},
	}

	for i, test := range table {
		prefix := strings1D(test.prefix)
		keys := strings2D(test.keys)
		subscripts := strings1D(test.subscripts)
		exp := test.exp

		a0, err := MemArray()
		if err != nil {
			t.Fatal(err)
		}

		a, err := a0.Array(prefix...)
		if err != nil {
			t.Fatal(err)
		}

		for i, v := range keys {
			a.Set(i, v...)
		}
		d, err := dump(a.tree)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("before Clear(%v)\n%s", subscripts, d)

		err = a.Clear(subscripts...)
		if err != nil {
			t.Fatal(err)
		}

		d, err = dump(a.tree)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf(" after Clear(%v)\n%s", subscripts, d)

		s, err := a0.Slice(nil, nil)
		if err != nil {
			t.Fatal(err)
		}

		var ga []string

		if err := s.Do(func(k, v []interface{}) (more bool, err error) {
			a := []string{}
			for _, v := range k {
				a = append(a, v.(string))
			}
			ga = append(ga, strings.Join(a, ","))
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}

		g := strings.Join(ga, "|")
		t.Log(g)
		if g != exp {
			t.Fatalf("i %d\ng: %s\ne: %s", i, g, exp)
		}
	}
}

func BenchmarkClear(b *testing.B) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, &Options{})
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	a, err := db.Array("test")
	if err != nil {
		b.Error(err)
		return
	}

	ref := map[int]struct{}{}
	for i := 0; i < b.N; i++ {
		ref[i] = struct{}{}
	}
	for i := range ref {
		a.Set(i, i)
	}
	if err := db.Close(); err != nil {
		b.Fatal(err)
		return
	}

	db2, err := Open(dbname, o)
	if err != nil {
		b.Error(err)
		return
	}

	defer db2.Close()

	a, err = db2.Array("test")
	if err != nil {
		b.Error(err)
		return
	}

	runtime.GC()
	b.ResetTimer()
	a.Clear()
	b.StopTimer()
}

func BenchmarkDelete(b *testing.B) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, &Options{})
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	a, err := db.Array("test")
	if err != nil {
		b.Error(err)
		return
	}

	ref := map[int]struct{}{}
	for i := 0; i < b.N; i++ {
		ref[i] = struct{}{}
	}
	for i := range ref {
		a.Set(i, i)
	}
	ref = map[int]struct{}{}
	for i := 0; i < b.N; i++ {
		ref[i] = struct{}{}
	}
	if err := db.Close(); err != nil {
		b.Error(err)
		return
	}

	db2, err := Open(dbname, o)
	if err != nil {
		b.Error(err)
		return
	}

	defer db2.Close()

	a, err = db2.Array("test")
	if err != nil {
		b.Error(err)
		return
	}

	runtime.GC()
	b.ResetTimer()
	for i := range ref {
		a.Delete(i)
	}
	b.StopTimer()
}

func BenchmarkGet(b *testing.B) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, &Options{})
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	a, err := db.Array("test")
	if err != nil {
		b.Error(err)
		return
	}

	ref := map[int]struct{}{}
	for i := 0; i < b.N; i++ {
		ref[i] = struct{}{}
	}
	ref = map[int]struct{}{}
	for i := 0; i < b.N; i++ {
		ref[i] = struct{}{}
	}
	if err := db.Close(); err != nil {
		b.Error(err)
		return
	}

	db2, err := Open(dbname, o)
	if err != nil {
		b.Error(err)
		return
	}

	defer db2.Close()

	a, err = db2.Array("test")
	if err != nil {
		b.Error(err)
		return
	}

	runtime.GC()
	b.ResetTimer()
	for i := range ref {
		a.Get(i)
	}
	b.StopTimer()
}

func BenchmarkSet(b *testing.B) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	a, err := db.Array("test")
	if err != nil {
		b.Error(err)
		return
	}

	ref := map[int]struct{}{}
	for i := 0; i < b.N; i++ {
		ref[i] = struct{}{}
	}
	runtime.GC()
	b.ResetTimer()
	for i := range ref {
		a.Set(i, i)
	}
	b.StopTimer()
}

func BenchmarkDo(b *testing.B) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, &Options{})
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	a, err := db.Array("test")
	if err != nil {
		b.Error(err)
		return
	}

	ref := map[int]struct{}{}
	for i := 0; i < b.N; i++ {
		ref[i] = struct{}{}
	}
	for i := range ref {
		a.Set(i, i)
	}
	if err := db.Close(); err != nil {
		b.Error(err)
		return
	}

	db2, err := Open(dbname, o)
	if err != nil {
		b.Error(err)
		return
	}

	a, err = db2.Array("test")
	if err != nil {
		b.Error(err)
		return
	}

	s, err := a.Slice(nil, nil)
	if err != nil {
		b.Error(err)
		return
	}

	runtime.GC()
	b.ResetTimer()
	s.Do(func(subscripts, value []interface{}) (bool, error) {
		return true, nil
	})
	b.StopTimer()
}

func TestRemoveArray0(t *testing.T) {
	const aname = "TestRemoveArray0"

	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	err = db.Set(42, aname, 1, 2)
	if err != nil {
		t.Error(err)
		return
	}

	_, err = db.Get(aname, 1, 2)
	if err != nil {
		t.Error(err)
		return
	}

	err = db.RemoveArray(aname)
	if err != nil {
		t.Error(err)
		return
	}

	if err = db.enter(); err != nil {
		t.Error(err)
		return
	}

	tr, err := db.acache.getTree(db, arraysPrefix, aname, false, aCacheSize)
	if err != nil {
		db.leave(&err)
		t.Error(err)
		return
	}

	if err = db.leave(&err); err != nil {
		t.Error(err)
		return
	}

	if tr != nil {
		t.Error(tr)
		return
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}

	if db, err = Open(dbname, o); err != nil {
		t.Error(err)
		return
	}

	for {
		<-time.After(time.Second)
		if atomic.LoadInt32(&activeVictors) == 0 {
			break
		}
	}

	if err := db.BeginUpdate(); err != nil {
		t.Error(err)
		return
	}

	err = db.alloc.Verify(
		lldb.NewMemFiler(),
		func(err error) bool {
			t.Error(err)
			return true
		},
		nil,
	)

	if err != nil {
		t.Error(err)
		return
	}

	if err := db.EndUpdate(); err != nil {
		t.Error(err)
		return
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}
}

func (db *DB) dumpAll(w io.Writer, msg string) {
	fmt.Fprintln(w, msg)
	root, err := db.root()
	if err != nil {
		fmt.Fprintln(w, "\nerror: ", err)
		return
	}

	fmt.Fprintln(w, "====\nroot\n====")
	if err = root.tree.Dump(w); err != nil {
		fmt.Fprintln(w, "\nerror: ", err)
		return
	}

	s, err := root.Slice(nil, nil)
	if err != nil {
		fmt.Fprintln(w, "\nerror: ", err)
		return
	}

	if err = s.Do(func(subscripts, value []interface{}) (bool, error) {
		v, err := root.get(subscripts...)
		if err != nil {
			fmt.Fprintln(w, "\nerror: ", err)
			return false, nil
		}

		h := v.(int64)
		t, err := lldb.OpenBTree(db.alloc, collate, h)
		if err != nil {
			fmt.Fprintln(w, "\nerror: ", err)
			return false, err
		}

		fmt.Fprintf(w, "----\n%#v @ %d\n----\n", subscripts[1], h)
		if err = t.Dump(w); err != nil {
			fmt.Fprintln(w, "\nerror: ", err)
			return false, err
		}

		return true, nil
	}); err != nil {
		fmt.Fprintln(w, "\nerror: ", err)
		return
	}
}

func TestRemoveFile0(t *testing.T) {
	const fname = "TestRemoveFile0"

	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	f, err := db.File(fname)
	if err != nil {
		t.Error(err)
		return
	}

	n, err := f.WriteAt([]byte{42}, 314)
	if n != 1 || err != nil {
		t.Error(err)
		return
	}

	files, err := db.Files()
	if err != nil {
		t.Error(err)
		return
	}

	v, err := files.Get(fname)
	if v == nil || err != nil {
		t.Error(err, v)
		return
	}

	err = db.RemoveFile(fname)
	if err != nil {
		t.Error(err)
		return
	}

	v, err = files.Get(fname)
	if v != nil || err != nil {
		t.Errorf("%#v %#v", err, v)
		return
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}
}

func TestRemove1(t *testing.T) {
	const (
		aname = "TestRemove1"
		N     = 100
	)

	compress = false // Test may correctly fail w/ compression.
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	sz0, err := db.Size()
	if err != nil {
		t.Error(err)
		return
	}

	for i := 0; i < N; i++ {
		if err = db.Set(fmt.Sprintf("V%06d", i), aname, fmt.Sprintf("K%06d", i)); err != nil {
			t.Error(err)
			return
		}
	}
	sz1, err := db.Size()
	if err != nil {
		t.Error(err)
		return
	}

	err = db.RemoveArray(aname)
	if err != nil {
		t.Error(err)
		return
	}

	err = db.Close()
	if err != nil {
		t.Error(err)
		return
	}

	fi, err := os.Stat(dbname)
	if err != nil {
		t.Error(err)
		return
	}

	sz2 := fi.Size()

	if db, err = Open(dbname, o); err != nil {
		t.Error(err)
		return
	}

	for i := 0; i < N/2+1; i++ {
		runtime.Gosched()
	}
	sz3, err := db.Size()
	if err != nil {
		t.Error(err)
		return
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}

	if db, err = Open(dbname, o); err != nil {
		t.Error(err)
		return
	}

	for i := 0; i < 2*N; i++ {
		runtime.Gosched()
	}
	sz4, err := db.Size()
	if err != nil {
		t.Error(err)
		return
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}

	t.Log(sz0)
	t.Log(sz1)
	t.Log(sz2)
	t.Log(sz3)
	t.Log(sz4)

	// Unstable
	//	if !(sz4 < sz3) {
	//		t.Error(sz3, sz4)
	//	}
}

func enumStrKeys(a Array) (k []string, err error) {
	s, err := a.Slice(nil, nil)
	if err != nil {
		return
	}

	return k, s.Do(func(subscripts, value []interface{}) (bool, error) {
		if len(subscripts) != 1 {
			return false, (fmt.Errorf("internal error: %#v", subscripts))
		}

		v, ok := subscripts[0].(string)
		if !ok {
			return false, (fmt.Errorf("internal error: %T %#v", subscripts, subscripts))
		}

		k = append(k, v)
		return true, nil
	})
}

func TestArrays(t *testing.T) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	a, err := db.Arrays()
	if err != nil {
		t.Error(err)
		return
	}

	names, err := enumStrKeys(a)
	if err != nil {
		t.Error(err)
		return
	}

	if g, e := len(names), 0; g != e {
		t.Error(g, e)
		return
	}

	if err = db.Set(nil, "foo"); err != nil {
		t.Error(err)
		return
	}

	names, err = enumStrKeys(a)
	if err != nil {
		t.Error(err)
		return
	}

	if g, e := len(names), 1; g != e {
		t.Error(g, e)
		return
	}

	if g, e := names[0], "foo"; g != e {
		t.Error(g, e)
		return
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}

}

func TestFiles(t *testing.T) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	a, err := db.Files()
	if err != nil {
		t.Error(err)
		return
	}

	names, err := enumStrKeys(a)
	if err != nil {
		t.Error(err)
		return
	}

	if g, e := len(names), 0; g != e {
		t.Error(g, e)
		return
	}

	f, err := db.File("foo")
	if err != nil {
		t.Error(err)
		return
	}

	if n, err := f.WriteAt([]byte{42}, 0); n != 1 {
		t.Error(err)
		return
	}

	names, err = enumStrKeys(a)
	if err != nil {
		t.Error(err)
		return
	}

	if g, e := len(names), 1; g != e {
		t.Error(g, e)
		return
	}

	if g, e := names[0], "foo"; g != e {
		t.Error(g, e)
		return
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}

}

func TestInc0(t *testing.T) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	db.Set(10, "TestInc", "ten")
	db.Set(nil, "TestInc", "nil")
	db.Set("string", "TestInc", "string")

	a, err := db.Array("TestInc")
	if err != nil {
		t.Fatal(err)
	}

	d, err := dump(a.tree)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("\n%s", d)

	n, err := db.Inc(1, "TestInc", "nonexisting")
	if err != nil || n != 1 {
		t.Error(n, err)
		return
	}

	n, err = db.Inc(2, "TestInc", "ten")
	if err != nil || n != 12 {
		t.Error(n, err)
		return
	}

	n, err = db.Inc(3, "TestInc", "nil")
	if err != nil || n != 3 {
		t.Error(n, err)
		return
	}

	n, err = db.Inc(4, "TestInc", "string")
	if err != nil || n != 4 {
		t.Error(n, err)
		return
	}

	d, err = dump(a.tree)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("\n%s", d)
}

func TestInc1(t *testing.T) {
	const (
		M = 3
	)
	N := 1000
	if *oACIDEnableWAL {
		N = 20
	}

	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	runtime.GOMAXPROCS(M)
	c := make(chan int64, M)
	for i := 0; i < M; i++ {
		go func() {
			sum := int64(0)
			for i := 0; i < N; i++ {
				n, err := db.Inc(1, "TestInc1", "Invoice", 314159, "Items")
				if err != nil {
					t.Error(err)
					break
				}

				sum += n
			}
			c <- sum
		}()
	}
	total := int64(0)
	for i := 0; i < M; i++ {
		select {
		case <-time.After(time.Second * 20):
			t.Error("timeouted")
			return
		case v := <-c:
			total += v
		}
	}

	nn := int64(M * N)
	if g, e := total, int64((nn*nn+nn)/2); g != e {
		t.Error(g, e)
		return
	}
}

func BenchmarkInc(b *testing.B) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	a, err := db.Array("test")
	if err != nil {
		b.Error(err)
		return
	}

	runtime.GC()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Inc(279, 314)
	}
	b.StopTimer()
}

func TestFile0(t *testing.T) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	a, err := db.Files()
	if err != nil {
		t.Error(err)
		return
	}

	names, err := enumStrKeys(a)
	if err != nil {
		t.Error(err)
		return
	}

	if g, e := len(names), 0; g != e {
		t.Error(g, e)
		return
	}

	f, err := db.File("foo")
	if err != nil {
		t.Error(err)
		return
	}

	if _, err = f.WriteAt([]byte("ABCDEF"), 4096); err != nil {
		t.Error(err)
		return
	}

	names, err = enumStrKeys(a)
	if err != nil {
		t.Error(err)
		return
	}

	if g, e := len(names), 1; g != e {
		t.Error(g, e)
		return
	}

	if g, e := names[0], "foo"; g != e {
		t.Error(g, e)
		return
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}
}

func TestFileTruncate0(t *testing.T) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	f, err := db.File("TestFileTruncate")
	if err != nil {
		t.Error(err)
		return
	}

	fsz := func() int64 {
		n, err := f.Size()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Check Truncate works.
	sz := int64(1e6)
	if err := f.Truncate(sz); err != nil {
		t.Error(err)
		return
	}

	if g, e := fsz(), sz; g != e {
		t.Error(g, e)
		return
	}

	sz *= 2
	if err := f.Truncate(sz); err != nil {
		t.Error(err)
		return
	}

	if g, e := fsz(), sz; g != e {
		t.Error(g, e)
		return
	}

	sz = 0
	if err := f.Truncate(sz); err != nil {
		t.Error(err)
		return
	}

	if g, e := fsz(), sz; g != e {
		t.Error(g, e)
		return
	}

	// Check Truncate(-1) doesn't work.
	sz = -1
	if err := f.Truncate(sz); err == nil {
		t.Error(err)
		return
	}

	d, err := dump(f.tree)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("\n%s", d)
}

func TestFileReadAtWriteAt(t *testing.T) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	f, err := db.File("TestFileReadAtWriteAt")
	if err != nil {
		t.Error(err)
		return
	}

	fsz := func() int64 {
		n, err := f.Size()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	const (
		N = 1 << 16
		M = 200
	)

	s := make([]byte, N)
	e := make([]byte, N)
	rnd := rand.New(rand.NewSource(42))
	for i := range e {
		s[i] = byte(rnd.Intn(256))
	}
	n2 := 0
	for i := 0; i < M; i++ {
		var from, to int
		for {
			from = rnd.Intn(N)
			to = rnd.Intn(N)
			if from != to {
				break
			}
		}
		if from > to {
			from, to = to, from
		}
		for i := range s[from:to] {
			s[from+i] = byte(rnd.Intn(256))
		}
		copy(e[from:to], s[from:to])
		if to > n2 {
			n2 = to
		}
		n, err := f.WriteAt(s[from:to], int64(from))
		if err != nil {
			t.Error(err)
			return
		}

		if g, e := n, to-from; g != e {
			t.Error(g, e)
			return
		}
	}

	if g, e := fsz(), int64(n2); g != e {
		t.Error(g, e)
		return
	}

	b := make([]byte, n2)
	for i := 0; i <= M; i++ {
		from := rnd.Intn(n2)
		to := rnd.Intn(n2)
		if from > to {
			from, to = to, from
		}
		if i == M {
			from, to = 0, n2
		}
		n, err := f.ReadAt(b[from:to], int64(from))
		if err != nil && (!fileutil.IsEOF(err) && n != 0) {
			t.Error(fsz(), from, to, err)
			return
		}

		if g, e := n, to-from; g != e {
			t.Error(g, e)
			return
		}

		if g, e := b[from:to], e[from:to]; !bytes.Equal(g, e) {
			t.Errorf(
				"i %d from %d to %d len(g) %d len(e) %d\n---- got ----\n%s\n---- exp ----\n%s",
				i, from, to, len(g), len(e), hex.Dump(g), hex.Dump(e),
			)
			return
		}
	}

	mf := f
	buf := &bytes.Buffer{}
	if _, err := mf.WriteTo(buf); err != nil {
		t.Error(err)
		return
	}

	if g, e := buf.Bytes(), e[:n2]; !bytes.Equal(g, e) {
		t.Errorf("\nlen %d\n%s\nlen %d\n%s", len(g), hex.Dump(g), len(e), hex.Dump(e))
		return
	}

	if err := mf.Truncate(0); err != nil {
		t.Error(err)
		return
	}

	if _, err := mf.ReadFrom(buf); err != nil {
		t.Error(err)
		return
	}

	roundTrip := make([]byte, n2)
	if n, err := mf.ReadAt(roundTrip, 0); err != nil && n == 0 {
		t.Error(err)
		return
	}

	if g, e := roundTrip, e[:n2]; !bytes.Equal(g, e) {
		t.Errorf("\nlen %d\n%s\nlen %d\n%s", len(g), hex.Dump(g), len(e), hex.Dump(e))
		return
	}
}

func TestFileReadAtHole(t *testing.T) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	f, err := db.File("TestFileReadAtHole")
	if err != nil {
		t.Error(err)
		return
	}

	n, err := f.WriteAt([]byte{1}, 40000)
	if err != nil {
		t.Error(err)
		return
	}

	if n != 1 {
		t.Error(n)
		return
	}

	n, err = f.ReadAt(make([]byte, 1000), 20000)
	if err != nil {
		t.Error(err)
		return
	}

	if n != 1000 {
		t.Error(n)
		return
	}
}

func BenchmarkFileWrSeq(b *testing.B) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	buf := make([]byte, fileTestChunkSize)
	for i := range buf {
		buf[i] = byte(rand.Int())
	}
	b.SetBytes(fileTestChunkSize)
	f, err := db.File("BenchmarkMemFilerWrSeq")
	if err != nil {
		b.Error(err)
		return
	}

	runtime.GC()
	b.ResetTimer()
	var ofs int64
	for i := 0; i < b.N; i++ {
		_, err := f.WriteAt(buf, ofs)
		if err != nil {
			b.Fatal(err)
		}

		ofs = (ofs + fileTestChunkSize) % fileTotalSize
	}
	b.StopTimer()
}

func BenchmarkFileRdSeq(b *testing.B) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, &Options{})
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	buf := make([]byte, fileTestChunkSize)
	for i := range buf {
		buf[i] = byte(rand.Int())
	}
	b.SetBytes(fileTestChunkSize)
	f, err := db.File("BenchmarkFileRdSeq")
	if err != nil {
		b.Error(err)
		return
	}

	var ofs int64
	for i := 0; i < b.N; i++ {
		_, err := f.WriteAt(buf, ofs)
		if err != nil {
			b.Fatal(err)
		}

		ofs = (ofs + fileTestChunkSize) % fileTotalSize
	}
	if err := db.Close(); err != nil {
		b.Fatal(err)
		return
	}

	db2, err := Open(dbname, o)
	if err != nil {
		b.Error(err)
		return
	}

	defer db2.Close()

	f, err = db2.File("BenchmarkFileRdSeq")
	if err != nil {
		b.Error(err)
		return
	}

	runtime.GC()
	b.ResetTimer()
	ofs = 0
	for i := 0; i < b.N; i++ {
		n, err := f.ReadAt(buf, ofs)
		if err != nil && n == 0 {
			b.Fatal(err)
		}

		ofs = (ofs + fileTestChunkSize) % fileTotalSize
	}
	b.StopTimer()
}

func TestBits0(t *testing.T) {
	const (
		M = 1024
	)

	N := 100
	if *oACIDEnableWAL {
		N = 50
	}

	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	f, err := db.File("TestBits0")
	if err != nil {
		t.Error(err)
		return
	}

	b := f.Bits()
	ref := map[uint64]bool{}

	rng := rand.New(rand.NewSource(42))
	for i := 0; i < N; i++ {
		bit := uint64(rng.Int63())
		run := uint64(rng.Intn(M))
		if rng.Int()&1 == 1 {
			run = 1
		}
		op := rng.Intn(3)

		switch op {
		case opOn:
			if err = b.On(bit, run); err != nil {
				t.Error(err)
				return
			}
			for i := bit; i < bit+run; i++ {
				ref[i] = true
			}
		case opOff:
			if err = b.Off(bit, run); err != nil {
				t.Error(err)
				return
			}
			for i := bit; i < bit+run; i++ {
				ref[i] = false
			}
		case opCpl:
			if err = b.Cpl(bit, run); err != nil {
				t.Error(err)
				return
			}
			for i := bit; i < bit+run; i++ {
				ref[i] = !ref[i]
			}
		}

	}

	for bit, v := range ref {
		gv, err := b.Get(bit)
		if err != nil {
			t.Error(err)
			return
		}

		if gv != v {
			d, err := dump(f.tree)
			if err != nil {
				t.Log(err)
			}
			t.Logf("\n%s", d)
			t.Errorf("%#x %t %t", bit, gv, v)
			return
		}
	}
}

func benchmarkBitsOn(b *testing.B, n uint64) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	f, err := db.File("TestBits0")
	if err != nil {
		b.Error(err)
		return
	}

	bits := f.Bits()

	rng := rand.New(rand.NewSource(42))
	a := make([]uint64, 1024*1024)
	for i := range a {
		a[i] = uint64(rng.Int63())
	}

	b.SetBytes(int64(n) / 8)
	runtime.GC()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bits.On(a[i&0xfffff], n)
	}

	b.StopTimer()
}

func BenchmarkBitsOn16(b *testing.B) {
	benchmarkBitsOn(b, 16)
}

func BenchmarkBitsOn1024(b *testing.B) {
	benchmarkBitsOn(b, 1024)
}

func BenchmarkBitsOn65536(b *testing.B) {
	benchmarkBitsOn(b, 65536)
}

func BenchmarkBitsGetSeq(b *testing.B) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	f, err := db.File("TestBitsGetSeq")
	if err != nil {
		b.Error(err)
		return
	}

	rng := rand.New(rand.NewSource(42))
	buf := make([]byte, 1024*1024)
	for i := range buf {
		buf[i] = byte(rng.Int63())
	}

	if _, err := f.WriteAt(buf, 0); err != nil {
		b.Fatal(err)
	}

	bits := f.Bits()
	runtime.GC()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bits.Get(uint64(i) & 0x7fffff)
	}
	b.StopTimer()
}

func BenchmarkBitsGetRnd(b *testing.B) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		b.Fatal(err)
	}

	defer db.Close()

	f, err := db.File("TestBitsGetRnd")
	if err != nil {
		b.Error(err)
		return
	}

	rng := rand.New(rand.NewSource(42))
	buf := make([]byte, 1024*1024)
	for i := range buf {
		buf[i] = byte(rng.Int63())
	}

	if _, err := f.WriteAt(buf, 0); err != nil {
		b.Fatal(err)
	}

	bits := f.Bits()

	a := make([]uint64, 1024*1024)
	for i := range a {
		a[i] = uint64(rng.Int63() & 0x7fffff)
	}

	runtime.GC()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bits.Get(a[i&0xfffff])
	}
	b.StopTimer()
}

func TestTmpDirRemoval(t *testing.T) {
	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	names := []string{"b", "/b", "/b/", "tmp", "/tmp", "/tmp/", "/tmp/foo", "z", "/z", "/z/"}

	for i, name := range names {
		if err := db.Set(i, name, 1, 2, 3); err != nil {
			t.Error(err)
			return
		}
	}

	for i, name := range names {

		f, err := db.File(name)
		if err != nil {
			t.Error(err)
			return
		}

		if _, err := f.WriteAt([]byte{byte(i)}, int64(i)); err != nil {
			t.Error(err)
			return
		}
	}

	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	ref := map[string]bool{}
	for _, name := range names {
		ref[name] = true
	}

	aa, err := db.Arrays()
	if err != nil {
		t.Error(err)
		return
	}

	s, err := aa.Slice(nil, nil)
	if err := s.Do(func(subscripts, value []interface{}) (bool, error) {
		k := subscripts[0].(string)
		delete(ref, k)
		return true, nil
	}); err != nil {
		t.Error(err)
		return
	}

	if len(ref) == 0 {
		t.Error(0)
		return
	}

	for k := range ref {
		if !strings.HasPrefix(k, "/tmp/") {
			t.Error(k)
			return
		}
	}

	ref = map[string]bool{}
	for _, name := range names {
		ref[name] = true
	}

	ff, err := db.Files()
	if err != nil {
		t.Error(err)
		return
	}

	s, err = ff.Slice(nil, nil)
	if err := s.Do(func(subscripts, value []interface{}) (bool, error) {
		k := subscripts[0].(string)
		delete(ref, k)
		return true, nil
	}); err != nil {
		t.Error(err)
		return
	}

	if len(ref) == 0 {
		t.Error(0)
		return
	}

	for k := range ref {
		if !strings.HasPrefix(k, "/tmp/") {
			t.Error(k)
			return
		}
	}

}

/*

2013-04-25
==========

(15:54) jnml@fsc-r550:~/src/github.com/cznic/exp/dbm$ . bench
++ go test -v -run Bench -keep -tbench -cpu 4
=== RUN TestBenchArraySetGet-4
--- PASS: TestBenchArraySetGet-4 (114.30 seconds)
	all_test.go:2820: WR: 51580 ops in 6.000e+01 s, 8.597e+02 ops/s, 1.163e-03 s/op
	all_test.go:2869: RD: 51580 ops in 5.425e+01 s, 9.508e+02 ops/s, 1.052e-03 s/op
PASS
ok  	github.com/cznic/exp/dbm	114.311s
++ go test -v -run Bench -keep -tbench -cpu 4 -xact
=== RUN TestBenchArraySetGet-4
--- PASS: TestBenchArraySetGet-4 (112.85 seconds)
	all_test.go:2820: WR: 46338 ops in 6.000e+01 s, 7.723e+02 ops/s, 1.295e-03 s/op
	all_test.go:2869: RD: 46338 ops in 5.279e+01 s, 8.778e+02 ops/s, 1.139e-03 s/op
PASS
ok  	github.com/cznic/exp/dbm	112.859s
++ go test -v -run Bench -keep -tbench -cpu 4 -wal -grace 0ms
=== RUN TestBenchArraySetGet-4
--- PASS: TestBenchArraySetGet-4 (60.38 seconds)
	all_test.go:2820: WR: 602 ops in 6.009e+01 s, 1.002e+01 ops/s, 9.982e-02 s/op, max WAL size 7056
	all_test.go:2869: RD: 602 ops in 1.244e-01 s, 4.838e+03 ops/s, 2.067e-04 s/op, max WAL size 0
PASS
ok  	github.com/cznic/exp/dbm	60.396s
++ go test -v -run Bench -keep -tbench -cpu 4 -wal -grace 1ms
=== RUN TestBenchArraySetGet-4
--- PASS: TestBenchArraySetGet-4 (94.13 seconds)
	all_test.go:2820: WR: 33664 ops in 6.003e+01 s, 5.608e+02 ops/s, 1.783e-03 s/op, max WAL size 37328
	all_test.go:2869: RD: 33664 ops in 3.380e+01 s, 9.961e+02 ops/s, 1.004e-03 s/op, max WAL size 0
PASS
ok  	github.com/cznic/exp/dbm	94.140s
++ go test -v -run Bench -keep -tbench -cpu 4 -wal -grace 10ms
=== RUN TestBenchArraySetGet-4
--- PASS: TestBenchArraySetGet-4 (99.36 seconds)
	all_test.go:2820: WR: 37880 ops in 6.000e+01 s, 6.313e+02 ops/s, 1.584e-03 s/op, max WAL size 48224
	all_test.go:2869: RD: 37880 ops in 3.916e+01 s, 9.673e+02 ops/s, 1.034e-03 s/op, max WAL size 0
PASS
ok  	github.com/cznic/exp/dbm	99.372s
++ go test -v -run Bench -keep -tbench -cpu 4 -wal -grace 100ms
=== RUN TestBenchArraySetGet-4
--- PASS: TestBenchArraySetGet-4 (100.20 seconds)
	all_test.go:2820: WR: 38464 ops in 6.018e+01 s, 6.392e+02 ops/s, 1.564e-03 s/op, max WAL size 46928
	all_test.go:2869: RD: 38464 ops in 3.981e+01 s, 9.661e+02 ops/s, 1.035e-03 s/op, max WAL size 0
PASS
ok  	github.com/cznic/exp/dbm	100.213s
++ go test -v -run Bench -keep -tbench -cpu 4 -wal -grace 1s
=== RUN TestBenchArraySetGet-4
--- PASS: TestBenchArraySetGet-4 (108.00 seconds)
	all_test.go:2820: WR: 44508 ops in 6.000e+01 s, 7.418e+02 ops/s, 1.348e-03 s/op, max WAL size 57264
	all_test.go:2869: RD: 44508 ops in 4.781e+01 s, 9.310e+02 ops/s, 1.074e-03 s/op, max WAL size 0
PASS
ok  	github.com/cznic/exp/dbm	108.016s
++ go test -v -run Bench -keep -tbench -cpu 4 -wal -grace 10s
=== RUN TestBenchArraySetGet-4
--- PASS: TestBenchArraySetGet-4 (111.36 seconds)
	all_test.go:2820: WR: 47565 ops in 6.000e+01 s, 7.927e+02 ops/s, 1.261e-03 s/op, max WAL size 162992
	all_test.go:2869: RD: 47565 ops in 5.113e+01 s, 9.302e+02 ops/s, 1.075e-03 s/op, max WAL size 0
PASS
ok  	github.com/cznic/exp/dbm	111.376s
(16:08) jnml@fsc-r550:~/src/github.com/cznic/exp/dbm$

2013-05-09
==========

# 16:05
jnml@fsc-r630:~/src/github.com/cznic/exp/dbm$ go test -tbench -run TestBench -v -wal -xact
=== RUN TestBenchArraySetGet
--- PASS: TestBenchArraySetGet (106.22 seconds)
	all_test.go:2759: WR: 27178 ops in 6.000e+01 s, 4.530e+02 ops/s, 2.208e-03 s/op, max WAL size 35120
	all_test.go:2808: RD: 27178 ops in 4.600e+01 s, 5.909e+02 ops/s, 1.692e-03 s/op, max WAL size 0
PASS
ok  	github.com/cznic/exp/dbm	106.234s
jnml@fsc-r630:~/src/github.com/cznic/exp/dbm$

*/
func TestBenchArraySetGet(t *testing.T) {
	if !*oBench {
		t.Log("Must be enabled by -tbench")
		return
	}

	dir, dbname := temp()
	defer os.RemoveAll(dir)

	db, err := Create(dbname, o)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	a, err := db.Array("test")
	if err != nil {
		t.Error(err)
		return
	}

	c := time.After(time.Minute)
	t0 := time.Now()
	var maxSet int64
loop:
	for i := 0; ; {
		select {
		case <-c:
			maxSet = int64(i - 1)
			ftot := float64(time.Since(t0)) / float64(time.Second)
			s := ""
			if af, ok := db.filer.(*lldb.ACIDFiler0); ok {
				s = fmt.Sprintf(", max WAL size %d", af.PeakWALSize())
			}
			t.Logf("WR: %d ops in %8.3e s, %8.3e ops/s, %8.3e s/op%s", i, ftot, float64(i)/ftot, ftot/float64(i), s)
			break loop
		default:
		}

		if err = a.Set(i^0x55555555, i); err != nil {
			t.Error(err)
			return
		}

		i++
	}

	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}

	if db, err = Open(dbname, o); err != nil {
		t.Error(err)
		return
	}

	a, err = db.Array("test")
	if err != nil {
		t.Error(err)
		return
	}

	t0 = time.Now()
	for i := int64(0); i <= maxSet; i++ {
		v, err := a.Get(i)
		if err != nil {
			t.Error(err)
			return
		}

		if g, e := v, int64(i^0x55555555); g != e {
			t.Errorf("i %d: %T(%v) %T(%v)", i, g, g, e, e)
			return
		}
	}

	ftot := float64(time.Since(t0)) / float64(time.Second)
	i := maxSet + 1
	s := ""
	if af, ok := db.filer.(*lldb.ACIDFiler0); ok {
		s = fmt.Sprintf(", max WAL size %d", af.PeakWALSize())
	}
	t.Logf("RD: %d ops in %8.3e s, %8.3e ops/s, %8.3e s/op%s", i, ftot, float64(i)/ftot, ftot/float64(i), s)
}

func TestLocking(t *testing.T) {
	db, err := CreateTemp("", "test-dbm-", ".db", &Options{})
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if db != nil {
			n := db.Name()
			db.Close()
			os.Remove(n)
		}
	}()

	// Must fail on lock or file exists
	if _, err = Create(db.Name(), &Options{}); err == nil {
		t.Error(err)
		return
	}

	t.Log(err)
	// Must fail on lock
	if _, err = Open(db.Name(), &Options{}); err == nil {
		t.Error(err)
		return
	}

	t.Log(err)
	n := db.Name()
	if err = db.Close(); err != nil {
		t.Error(err)
		return
	}

	// Must fail on DB file exists
	if _, err = Create(n, &Options{}); err == nil {
		t.Error(err)
		return
	}

	t.Log(err)
	// Must succeed
	if db, err = Open(n, &Options{}); err != nil {
		t.Error(err)
		return
	}
}

func TestBug20130712(t *testing.T) {
	db, err := CreateMem(&Options{})
	if err != nil {
		t.Fatal(err)
	}

	a, err := db.Array("t")
	if err != nil {
		t.Fatal(err)
	}

	if err := a.Set(nil, 1, 2); err != nil {
		t.Fatal(err)
	}

	if err := a.Set(nil, 171, 1); err != nil {
		t.Fatal(err)
	}

	a, err = a.Array(1)
	if err != nil {
		t.Fatal(err)
	}

	s, err := a.Slice(nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Do(func(subscripts, value []interface{}) (bool, error) {
		t.Log(subscripts, value)
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateWithEmptyWAL(t *testing.T) {
	dir, err := ioutil.TempDir("", "dbm-test-create")
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

	if err = db.Set("val", "subscript"); err != nil {
		t.Error(err)
	}
	db.Close()
}

func TestCreateWithNonEmptyWAL(t *testing.T) {
	dir, err := ioutil.TempDir("", "dbm-test-create")
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

	if _, err = Create(dbName, &Options{ACID: ACIDFull}); err == nil {
		t.Error("Unexpected success")
		return
	}
}

func TestIsMem(t *testing.T) {
	db, err := CreateTemp("", "dbm-test", ".tmp", &Options{})
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		nm := db.Name()
		db.Close()
		os.Remove(nm)
	}()

	if g, e := db.IsMem(), false; g != e {
		t.Error(g, e)
		return
	}

	db2, err := CreateMem(&Options{})
	if err != nil {
		t.Fatal(err)
	}

	if g, e := db2.IsMem(), true; g != e {
		t.Error(g, e)
		return
	}
}
