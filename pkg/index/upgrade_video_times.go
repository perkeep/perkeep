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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go4.org/strutil"
	"go4.org/types"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
)

// videoTimesSchemaVersion is the index schema version that addVideoTimes
// upgrades version 11 indexes to.
const videoTimesSchemaVersion = 12

// isVideoTimesUpgrade reports whether an index of schemaVersion can be
// upgraded to the required version by addVideoTimes, and the upgrades
// after it, without a reindex.
func isVideoTimesUpgrade(schemaVersion int) bool {
	return schemaVersion == digestsSchemaVersion && isJPEGEXIFUpgrade(videoTimesSchemaVersion)
}

var errMissingVideoTimes = errors.New("index: missing the video recording times of index schema version 12")

// videoTimesFile is a QuickTime or MP4 file whose times addVideoTimes
// adds its recording time to.
type videoTimesFile struct {
	fileRef blob.Ref
	times   string // its keyFileTimes value, unescaped, or ""
}

// addVideoTimes upgrades a version 11 index to version 12, whose file
// times include a QuickTime or MP4 movie's recording time, as
// populateFile adds it. It reads each such file's movie metadata, and
// rewrites the file's times if they change. Its times are a function of
// the old ones and the recording time, so if it's interrupted, the next
// start does it all again, to the same effect.
func (x *Index) addVideoTimes(ctx context.Context, fetcher blob.Fetcher) (err error) {
	// Like fixMissingWholeRef, guard against misuse.
	if !isVideoTimesUpgrade(x.schemaVersion()) {
		panic("addVideoTimes should only be used when upgrading from v11 to v12 of the index schema")
	}
	start := time.Now()
	x.logf("upgrading index schema from version %d to %d by adding videos' recording times...", digestsSchemaVersion, videoTimesSchemaVersion)
	defer func() {
		if err != nil {
			x.logf("adding videos' recording times failed: %v", err)
		}
	}()

	files, err := x.videoTimesFiles()
	if err != nil {
		return err
	}
	x.logf("reading the recording times of %d video files...", len(files))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan videoTimesFile)
	results := make(chan *mutationMap)
	var failed, changed atomic.Int64
	var wg sync.WaitGroup
	for range max(1, ReindexMaxProcs()) {
		wg.Go(func() {
			for f := range work {
				mm := new(mutationMap)
				if err := x.addVideoTime(ctx, fetcher, f, mm); err != nil {
					// Like a failed reindex of the file, which
					// wouldn't have the time either.
					if failed.Add(1) <= 10 {
						x.logf("adding the recording time of %v: %v", f.fileRef, err)
					}
				}
				if len(mm.kv) > 0 {
					changed.Add(1)
				}
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
			x.logf("read the recording times of %d of %d video files; %d changed", done, len(files), changed.Load())
		default:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := commit(); err != nil {
		return err
	}
	if err := x.s.Set(keySchemaVersion.name, fmt.Sprint(videoTimesSchemaVersion)); err != nil {
		return fmt.Errorf("could not write index schema version %d: %w", videoTimesSchemaVersion, err)
	}
	x.logf("upgraded index schema to version %d in %v; %d video files' times changed, %d failed",
		videoTimesSchemaVersion, time.Since(start).Round(time.Millisecond), changed.Load(), failed.Load())
	return nil
}

// videoTimesFiles returns the indexed QuickTime and MP4 files, with their
// times.
func (x *Index) videoTimesFiles() (files []videoTimesFile, err error) {
	var vals []string
	it := x.queryPrefix(keyFileInfo)
	defer closeIterator(it, &err)
	for it.Next() {
		// fileinfo|fileRef = size|fileName|mimeType|wholeRef
		fileRef, ok := blob.Parse(strings.TrimPrefix(it.Key(), keyFileInfo.name+"|"))
		if !ok {
			continue
		}
		vals = strutil.AppendSplitN(vals[:0], it.Value(), "|", 4)
		if len(vals) != 4 || !isQuickTimeMIME(urld(vals[2])) {
			continue
		}
		files = append(files, videoTimesFile{fileRef: fileRef})
	}
	for i := range files {
		v, err := x.s.Get(keyFileTimes.Key(files[i].fileRef))
		if err != nil && !errors.Is(err, sorted.ErrNotFound) {
			return nil, err
		}
		files[i].times = urld(v)
	}
	return files, nil
}

// addVideoTime adds to mm f's times row, with its recording time, if
// that changes it.
func (x *Index) addVideoTime(ctx context.Context, fetcher blob.Fetcher, f videoTimesFile, mm *mutationMap) error {
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
	md := x.readQuickTime(fr, fr.Size())
	if md == nil || md.Created.IsZero() {
		return nil
	}
	var times []time.Time
	if f.times != "" {
		for _, s := range strings.Split(f.times, ",") {
			if t := types.ParseTime3339OrNil(s); t != nil {
				times = append(times, time.Time(*t))
			}
		}
	}
	if v := fileTimesValue(append(times, md.Created)); v != f.times {
		mm.Set(keyFileTimes.Key(f.fileRef), keyFileTimes.Val(v))
	}
	return nil
}
