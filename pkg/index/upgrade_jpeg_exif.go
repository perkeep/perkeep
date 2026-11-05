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
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/perkeep/goexif/exif"
	"go4.org/strutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
)

// isJPEGEXIFUpgrade reports whether an index of schemaVersion can be
// upgraded to the required version by addJPEGEXIF, without a reindex.
func isJPEGEXIFUpgrade(schemaVersion int) bool {
	return schemaVersion == videoTimesSchemaVersion && requiredSchemaVersion == 13
}

var errMissingJPEGEXIF = errors.New("index: missing the JPEG EXIF of index schema version 13")

// jpegEXIFContents is the contents of the indexed JPEG files with wholeRef,
// which addJPEGEXIF may reindex.
type jpegEXIFContents struct {
	wholeRef blob.Ref
	files    []blob.Ref
}

// jpegEXIFResult is the changes to the rows of a jpegEXIFContents.
type jpegEXIFResult struct {
	mm      *mutationMap
	deletes []string // keys of rows of the old EXIF of the contents
}

// addJPEGEXIF upgrades a version 12 index to version 13, which reads a
// JPEG's EXIF from its first APP1 segment with EXIF. Earlier versions
// read the APP1 segment at the first FF E1 byte pair, which may be XMP
// that precedes the EXIF, or not a segment at all. They also didn't retry
// with the whole file for the time or image size of a JPEG whose EXIF
// segment ends past the prefix populateFile retains. Only such JPEGs'
// rows change, so rather than reindexing everything, addJPEGEXIF reads
// the headers of the indexed JPEGs and reindexes those files with
// populateFile, deleting their contents' rows from the old EXIF, then
// sets the schema version. It's idempotent, so if it's interrupted, the
// next start runs it again.
func (x *Index) addJPEGEXIF(ctx context.Context, fetcher blob.Fetcher) (err error) {
	// Like fixMissingWholeRef, guard against misuse.
	if !isJPEGEXIFUpgrade(x.schemaVersion()) {
		panic("addJPEGEXIF should only be used when upgrading from v12 to v13 of the index schema")
	}
	start := time.Now()
	x.logf("upgrading index schema from version %d to %d by finding JPEGs' EXIF...", videoTimesSchemaVersion, requiredSchemaVersion)
	defer func() {
		if err != nil {
			x.logf("finding JPEGs' EXIF failed: %v", err)
		}
	}()

	contents, err := x.jpegEXIFContents()
	if err != nil {
		return err
	}
	x.logf("reading the headers of %d JPEG contents...", len(contents))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan jpegEXIFContents)
	results := make(chan jpegEXIFResult)
	var failed, changed atomic.Int64
	var wg sync.WaitGroup
	for range max(1, ReindexMaxProcs()) {
		wg.Go(func() {
			for c := range work {
				res, err := x.addContentsJPEGEXIF(ctx, fetcher, c)
				if err != nil {
					// Like a failed reindex of the files, which
					// wouldn't have these rows either.
					if failed.Add(1) <= 10 {
						x.logf("finding the EXIF of JPEG %v: %v", c.wholeRef, err)
					}
				}
				if len(res.mm.kv) > 0 {
					changed.Add(1)
				}
				select {
				case results <- res:
				case <-ctx.Done():
					return
				}
			}
		})
	}
	go func() {
		defer close(work)
		for _, c := range contents {
			select {
			case work <- c:
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
	var batch []jpegEXIFResult
	var done int
	commit := func() error {
		bm := x.s.BeginBatch()
		for _, res := range batch {
			for _, k := range res.deletes {
				bm.Delete(k)
			}
			for k, v := range res.mm.kv {
				bm.Set(k, v)
			}
		}
		batch = batch[:0]
		return x.s.CommitBatch(bm)
	}
	for res := range results {
		done++
		if len(res.mm.kv) > 0 {
			batch = append(batch, res)
		}
		if len(batch) >= digestBatchSize {
			if err := commit(); err != nil {
				return err
			}
		}
		select {
		case <-ticker.C:
			x.logf("read the headers of %d of %d JPEG contents; %d reindexed", done, len(contents), changed.Load())
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
	x.logf("upgraded index schema to version %d in %v; %d JPEG contents reindexed, %d failed",
		requiredSchemaVersion, time.Since(start).Round(time.Millisecond), changed.Load(), failed.Load())
	return nil
}

// jpegEXIFContents returns the contents of the indexed JPEG files.
func (x *Index) jpegEXIFContents() (contents []jpegEXIFContents, err error) {
	var vals []string
	index := make(map[blob.Ref]int) // wholeRef to its index in contents
	it := x.queryPrefix(keyFileInfo)
	defer closeIterator(it, &err)
	for it.Next() {
		// fileinfo|fileRef = size|fileName|mimeType|wholeRef
		fileRef, ok := blob.Parse(strings.TrimPrefix(it.Key(), keyFileInfo.name+"|"))
		if !ok {
			continue
		}
		vals = strutil.AppendSplitN(vals[:0], it.Value(), "|", 4)
		if len(vals) != 4 || urld(vals[2]) != "image/jpeg" {
			continue
		}
		wholeRef, ok := blob.Parse(urld(vals[3]))
		if !ok {
			continue
		}
		i, ok := index[wholeRef]
		if !ok {
			i = len(contents)
			index[wholeRef] = i
			contents = append(contents, jpegEXIFContents{wholeRef: wholeRef})
		}
		contents[i].files = append(contents[i].files, fileRef)
	}
	return contents, nil
}

// addContentsJPEGEXIF returns the changes to the rows of c: if
// jpegEXIFMoved, the rows populateFile indexes for its files, and the
// deletion of its contents' other EXIF, GPS and media tag rows, which the
// old EXIF had.
func (x *Index) addContentsJPEGEXIF(ctx context.Context, fetcher blob.Fetcher, c jpegEXIFContents) (res jpegEXIFResult, err error) {
	res.mm = new(mutationMap)
	fetchFile := func(fileRef blob.Ref) (*schema.Blob, error) {
		rc, _, err := fetcher.Fetch(ctx, fileRef)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return schema.BlobFromReader(fileRef, rc)
	}
	b, err := fetchFile(c.files[0])
	if err != nil {
		return res, err
	}
	fr, err := b.NewFileReader(fetcher)
	if err != nil {
		return res, err
	}
	moved, err := jpegEXIFMoved(bufio.NewReaderSize(fr, 64<<10))
	fr.Close()
	if err != nil || !moved {
		return res, err
	}

	mm := new(mutationMap)
	for i, fileRef := range c.files {
		if i > 0 {
			if b, err = fetchFile(fileRef); err != nil {
				return res, err
			}
		}
		if err := x.populateFile(ctx, &missTrackFetcher{fetcher: fetcher}, b, mm); err != nil {
			return res, err
		}
	}
	for _, key := range []*keyType{keyEXIFTag, keyMediaTag} {
		it := x.queryPrefix(key, c.wholeRef)
		for it.Next() {
			if _, ok := mm.kv[it.Key()]; !ok {
				res.deletes = append(res.deletes, it.Key())
			}
		}
		if err := it.Close(); err != nil {
			return res, err
		}
	}
	gpsKey := keyEXIFGPS.Key(c.wholeRef)
	if _, ok := mm.kv[gpsKey]; !ok {
		if _, err := x.s.Get(gpsKey); err == nil {
			res.deletes = append(res.deletes, gpsKey)
		} else if !errors.Is(err, sorted.ErrNotFound) {
			return res, err
		}
	}
	res.mm = mm
	return res, nil
}

// jpegEXIFMoved reports whether version 13 may index JPEG r differently
// from version 12: whether version 12's search for the APP1 segment to
// decode doesn't find r's first APP1 segment with EXIF, or that segment ends
// past the image prefix that populateFile retains. It also reports true
// for a JPEG that ends before it finds whether r has such a segment, which
// version 13 reads differently too.
func jpegEXIFMoved(r io.Reader) (bool, error) {
	var head bytes.Buffer
	off, data, err := exif.FindJPEGExif(io.TeeReader(r, &head))
	switch {
	case errors.Is(err, exif.ErrNoJPEGExif):
		return false, nil
	case errors.Is(err, io.ErrUnexpectedEOF):
		return true, nil
	case err != nil:
		return false, err
	}
	if off+4+int64(len(data)) > imagePrefixSize {
		return true, nil
	}
	return goexifAPP1(head.Bytes()[:off+4]) != off, nil
}

// goexifAPP1 returns the offset in b of the FF E1 byte pair whose APP1
// segment the exif.Decode of github.com/rwcarlsen/goexif, which version
// 12 used, decodes as the EXIF of a JPEG starting with b, or -1 if it
// doesn't find one in b. Like it, goexifAPP1 searches raw bytes for FF
// followed by E1, skipping FF and its next byte if that isn't E1, and the
// pairs of segments of length 2, with no contents.
func goexifAPP1(b []byte) int64 {
	for i := 0; ; {
		j := bytes.IndexByte(b[i:], 0xff)
		if j < 0 || i+j+4 > len(b) {
			return -1
		}
		j += i
		i = j + 2
		if b[j+1] != 0xe1 {
			continue
		}
		i = j + 4
		if binary.BigEndian.Uint16(b[j+2:]) == 2 {
			continue
		}
		return int64(j)
	}
}
