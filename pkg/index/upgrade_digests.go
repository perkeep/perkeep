/*
Copyright 2026 The Perkeep Authors.

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

package index

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go4.org/strutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
)

// isDigestsUpgrade reports whether an index of schemaVersion can be
// upgraded to the required version by addDigests, without a reindex.
func isDigestsUpgrade(schemaVersion int) bool {
	return schemaVersion == 10 && requiredSchemaVersion == 11
}

var errMissingDigests = errors.New("index: missing the file digest rows of index schema version 11")

// digestFile is a file whose contents addDigests indexes the digests of.
type digestFile struct {
	fileRef  blob.Ref // any of the files with wholeRef
	wholeRef blob.Ref
	size     int64
}

// digestBatchSize is how many files' rows addDigests commits at once.
const digestBatchSize = 500

// addDigests upgrades a version 10 index to version 11, which adds the
// digests of each file's contents (see DigestTypes). It reads each
// indexed content once, by one of its files, and computes the digests as
// populateFile does, then sets the schema version. It skips contents that
// already have digests, so if it's interrupted, the next start continues
// where it left off.
func (x *Index) addDigests(ctx context.Context, fetcher blob.Fetcher) (err error) {
	// Like fixMissingWholeRef, guard against misuse.
	if !isDigestsUpgrade(x.schemaVersion()) {
		panic("addDigests should only be used when upgrading from v10 to v11 of the index schema")
	}
	start := time.Now()
	x.logf("upgrading index schema from version 10 to %d by adding file digests...", requiredSchemaVersion)
	defer func() {
		if err != nil {
			x.logf("adding file digests failed: %v", err)
		}
	}()

	files, err := x.digestFiles()
	if err != nil {
		return err
	}
	var totalBytes int64
	for _, f := range files {
		totalBytes += f.size
	}
	x.logf("adding digests of %d file contents (%d MiB)...", len(files), totalBytes>>20)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan digestFile)
	results := make(chan *mutationMap)
	var failed atomic.Int64
	var doneBytes atomic.Int64
	var wg sync.WaitGroup
	for range max(1, ReindexMaxProcs()) {
		wg.Go(func() {
			for f := range work {
				mm := new(mutationMap)
				if err := x.addFileDigests(ctx, fetcher, f, mm); err != nil {
					// Like a failed reindex of the file, which
					// wouldn't have these rows either.
					if failed.Add(1) <= 10 {
						x.logf("adding digests of %v (%v): %v", f.wholeRef, f.fileRef, err)
					}
				}
				doneBytes.Add(f.size)
				select {
				case results <- mm:
				case <-ctx.Done():
					return
				}
			}
		})
	}
	go func() {
		defer close(work)
		for _, f := range files {
			select {
			case work <- f:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var batch []*mutationMap
	var done int
	commit := func() error {
		bm := x.s.BeginBatch()
		for _, mm := range batch {
			for k, v := range mm.kv {
				bm.Set(k, v)
			}
		}
		batch = batch[:0]
		return x.s.CommitBatch(bm)
	}
	for mm := range results {
		done++
		if len(mm.kv) > 0 {
			batch = append(batch, mm)
		}
		if len(batch) >= digestBatchSize {
			if err := commit(); err != nil {
				return err
			}
		}
		select {
		case <-ticker.C:
			x.logf("added digests of %d of %d file contents; %s", done, len(files), progress(doneBytes.Load(), totalBytes, time.Since(start)))
		default:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := commit(); err != nil {
		return err
	}
	if err := x.s.Set(keySchemaVersion.name, fmt.Sprint(requiredSchemaVersion)); err != nil {
		return fmt.Errorf("could not write index schema version %d: %w", requiredSchemaVersion, err)
	}
	x.logf("upgraded index schema to version %d in %v; %d file contents failed", requiredSchemaVersion, time.Since(start).Round(time.Millisecond), failed.Load())
	return nil
}

// progress describes having read done of total bytes in elapsed time.
func progress(done, total int64, elapsed time.Duration) string {
	s := fmt.Sprintf("%d of %d MiB", done>>20, total>>20)
	if done == 0 || elapsed <= 0 {
		return s
	}
	rate := float64(done) / elapsed.Seconds()
	s += fmt.Sprintf(", %.1f MiB/s", rate/(1<<20))
	if total > done {
		eta := time.Duration(float64(total-done) / rate * float64(time.Second))
		s += fmt.Sprintf(", about %v left", eta.Round(time.Minute))
	}
	return s
}

// digestFiles returns one file of each indexed content that doesn't have
// digests yet.
func (x *Index) digestFiles() (files []digestFile, err error) {
	have := make(map[blob.Ref]bool)
	it := x.queryPrefix(keyFileDigests)
	for it.Next() {
		// filedigests|wholeRef = digests
		if wholeRef, ok := blob.Parse(strings.TrimPrefix(it.Key(), keyFileDigests.name+"|")); ok {
			have[wholeRef] = true
		}
	}
	if err := it.Close(); err != nil {
		return nil, err
	}

	var vals []string
	it = x.queryPrefix(keyFileInfo)
	defer closeIterator(it, &err)
	for it.Next() {
		// fileinfo|fileRef = size|fileName|mimeType|wholeRef
		fileRef, ok := blob.Parse(strings.TrimPrefix(it.Key(), keyFileInfo.name+"|"))
		if !ok {
			continue
		}
		vals = strutil.AppendSplitN(vals[:0], it.Value(), "|", 4)
		if len(vals) != 4 {
			continue
		}
		// Directories and symlinks have no wholeRef.
		wholeRef, ok := blob.Parse(urld(vals[3]))
		if !ok || have[wholeRef] {
			continue
		}
		size, err := strconv.ParseInt(vals[0], 10, 64)
		if err != nil {
			continue
		}
		have[wholeRef] = true
		files = append(files, digestFile{fileRef: fileRef, wholeRef: wholeRef, size: size})
	}
	return files, nil
}

// addFileDigests adds to mm the digest rows of f's contents.
func (x *Index) addFileDigests(ctx context.Context, fetcher blob.Fetcher, f digestFile, mm *mutationMap) error {
	rc, _, err := fetcher.Fetch(ctx, f.fileRef)
	if err != nil {
		return err
	}
	b, err := schema.BlobFromReader(f.fileRef, rc)
	rc.Close()
	if err != nil {
		return err
	}
	fr, err := b.NewFileReader(fetcher)
	if err != nil {
		return err
	}
	defer fr.Close()
	d := newFileDigester()
	if _, err := copyWithBuffer(d, fr); err != nil {
		return err
	}
	digests := d.digests()
	// Check the contents against wholeRef, if it's one of the digests.
	_, known := digestHexLen[f.wholeRef.HashName()]
	if known && !slices.Contains(digests, f.wholeRef.String()) {
		return fmt.Errorf("contents don't match their wholeRef; got digests %q", digests)
	}
	setDigestRows(mm, f.wholeRef, digests)
	return nil
}
