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

// Program kvcopy copies the rows of one sorted.KeyValue into another, such
// as when moving a server's index, queues, and blobpacked metadata from
// leveldb to SQLite, or prints them to stdout.
//
// Usage:
//
//	kvcopy [flags] SRC [DST]
//
// SRC and DST are each either TYPE:PATH, for a file-based store (leveldb,
// sqlite, or kv), or a JSON sorted.KeyValue config object, as used for
// "storage" in a server config, e.g. {"type":"mysql",...}.
//
// With DST, kvcopy copies the rows into it, which must be empty, and then
// re-reads both stores to check that the copy matches. Without DST, it
// prints the rows, one per line, as a Go-quoted key, a space, and a
// Go-quoted value.
//
// The --start, --end, and --limit flags restrict which rows are copied or
// printed. The servers using the stores should be stopped first.
package main // import "perkeep.org/dev/kvcopy"

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"go4.org/jsonconfig"

	"perkeep.org/pkg/sorted"
	_ "perkeep.org/pkg/sorted/kvfile"
	_ "perkeep.org/pkg/sorted/leveldb"
	_ "perkeep.org/pkg/sorted/mongo"
	_ "perkeep.org/pkg/sorted/mysql"
	_ "perkeep.org/pkg/sorted/postgres"
	_ "perkeep.org/pkg/sorted/sqlite"
)

const batchSize = 10000

func main() {
	log.SetFlags(0)
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		log.Fatalf("kvcopy: %v", err)
	}
}

// rowRange restricts which rows are copied or printed.
type rowRange struct {
	start, end string // as for sorted.KeyValue.Find
	limit      int    // max rows; 0 means no limit
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("kvcopy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: kvcopy [flags] SRC [DST]\n\n"+
			"SRC and DST are TYPE:PATH (TYPE is leveldb, sqlite, or kv) or a JSON\n"+
			"sorted.KeyValue config. Without DST, rows are printed to stdout.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	var rr rowRange
	fs.StringVar(&rr.start, "start", "", "first key to include")
	fs.StringVar(&rr.end, "end", "", "key to stop before; empty means no end")
	fs.IntVar(&rr.limit, "limit", 0, "maximum number of rows; 0 means no limit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		fs.Usage()
		return errors.New("want SRC and optional DST")
	}
	if rr.limit < 0 {
		return errors.New("-limit must not be negative")
	}

	src, err := openKV(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer src.Close()
	if fs.NArg() == 1 {
		return dump(src, rr, stdout)
	}
	if fs.Arg(0) == fs.Arg(1) {
		return errors.New("source and destination are the same")
	}
	dst, err := openKV(fs.Arg(1))
	if err != nil {
		return fmt.Errorf("opening destination: %w", err)
	}
	err = copyRows(src, dst, rr, stderr)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	return err
}

// openKV opens a sorted.KeyValue from a TYPE:PATH or JSON config spec.
func openKV(spec string) (sorted.KeyValue, error) {
	var cfg jsonconfig.Obj
	if strings.HasPrefix(spec, "{") {
		if err := json.Unmarshal([]byte(spec), &cfg); err != nil {
			return nil, fmt.Errorf("invalid JSON config %q: %w", spec, err)
		}
	} else {
		typ, path, ok := strings.Cut(spec, ":")
		if !ok || typ == "" || path == "" {
			return nil, fmt.Errorf("invalid store %q; want TYPE:PATH or a JSON config", spec)
		}
		cfg = jsonconfig.Obj{"type": typ, "file": path}
	}
	return sorted.NewKeyValue(cfg)
}

// rows calls fn for each row of kv in rr, in key order.
func rows(kv sorted.KeyValue, rr rowRange, fn func(key, value string) error) error {
	it := kv.Find(rr.start, rr.end)
	n := 0
	for (rr.limit == 0 || n < rr.limit) && it.Next() {
		if err := fn(it.Key(), it.Value()); err != nil {
			it.Close()
			return err
		}
		n++
	}
	return it.Close()
}

func dump(kv sorted.KeyValue, rr rowRange, w io.Writer) error {
	bw := bufio.NewWriter(w)
	err := rows(kv, rr, func(key, value string) error {
		_, err := fmt.Fprintf(bw, "%q %q\n", key, value)
		return err
	})
	if err != nil {
		return err
	}
	return bw.Flush()
}

func copyRows(src, dst sorted.KeyValue, rr rowRange, logw io.Writer) error {
	// Requiring an empty destination makes the verification below exact and
	// means nothing already there can be overwritten.
	if err := rows(dst, rowRange{limit: 1}, func(key, _ string) error {
		return fmt.Errorf("destination is not empty; it has key %q", key)
	}); err != nil {
		return err
	}

	b := dst.BeginBatch()
	n, inBatch := 0, 0
	err := rows(src, rr, func(key, value string) error {
		b.Set(key, value)
		n++
		if inBatch++; inBatch == batchSize {
			if err := dst.CommitBatch(b); err != nil {
				return err
			}
			b, inBatch = dst.BeginBatch(), 0
			if n%(100*batchSize) == 0 {
				fmt.Fprintf(logw, "kvcopy: copied %d rows\n", n)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := dst.CommitBatch(b); err != nil {
		return err
	}

	srcN, srcSum, err := summarize(src, rr)
	if err != nil {
		return fmt.Errorf("re-reading source: %w", err)
	}
	dstN, dstSum, err := summarize(dst, rowRange{})
	if err != nil {
		return fmt.Errorf("re-reading destination: %w", err)
	}
	if srcN != n || dstN != n || srcSum != dstSum {
		return fmt.Errorf("copy mismatch: copied %d rows; source has %d rows (%s), destination has %d rows (%s)", n, srcN, srcSum, dstN, dstSum)
	}
	fmt.Fprintf(logw, "kvcopy: copied %d rows; source and destination match (sha256 %s)\n", n, srcSum)
	return nil
}

// summarize returns the number of rows of kv in rr and a digest over them.
func summarize(kv sorted.KeyValue, rr rowRange) (n int, sum string, err error) {
	h := sha256.New()
	err = rows(kv, rr, func(key, value string) error {
		fmt.Fprintf(h, "%d:%s%d:%s", len(key), key, len(value), value)
		n++
		return nil
	})
	return n, fmt.Sprintf("%x", h.Sum(nil))[:16], err
}
