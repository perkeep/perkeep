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

package index

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/sorted"
)

// keyRange is the range of keys [start, end).
type keyRange struct {
	start, end string
}

// prefixRange returns the range of keys starting with prefix.
func prefixRange(prefix string) keyRange {
	last := prefix[len(prefix)-1]
	if last == 0xff {
		panic("unsupported prefix ending in 0xff")
	}
	return keyRange{prefix, prefix[:len(prefix)-1] + string(last+1)}
}

// scanRanges returns key ranges that together cover every row starting
// with prefix, for rows whose keys continue with a blobref (such as
// "meta:" and "claim|"), to scan them in parallel: one for each leading
// byte of each hash type's digests, and ones for the gaps before, between,
// and after those, where no well-formed rows are. The ranges are ordered
// round-robin across hash types (sha1-00, sha224-00, sha256-00, sha1-01,
// ...), so the first few to be scanned are an even sample of all of them,
// followed by the gaps.
func scanRanges(prefix string) []keyRange {
	hashes := blob.HashFuncs()
	slices.Sort(hashes)
	// Range boundaries, in key order. "." sorts just after "-", so
	// prefix+"sha1." ends the last sha1 range.
	bounds := []string{prefix}
	for _, h := range hashes {
		for b := range 256 {
			bounds = append(bounds, fmt.Sprintf("%s%s-%02x", prefix, h, b))
		}
		bounds = append(bounds, prefix+h+".")
	}
	bounds = append(bounds, prefixRange(prefix).end)
	if !slices.IsSorted(bounds) {
		panic("scan range boundaries aren't sorted")
	}

	byStart := make(map[string]keyRange, len(bounds))
	var gaps []keyRange
	for i := 0; i+1 < len(bounds); i++ {
		r := keyRange{bounds[i], bounds[i+1]}
		if strings.HasSuffix(r.start, ".") || r.start == prefix {
			gaps = append(gaps, r)
		} else {
			byStart[r.start] = r
		}
	}
	ranges := make([]keyRange, 0, len(bounds)-1)
	for b := range 256 {
		for _, h := range hashes {
			ranges = append(ranges, byStart[fmt.Sprintf("%s%s-%02x", prefix, h, b)])
		}
	}
	return append(ranges, gaps...)
}

// metaShard is the parsed contents of one meta scan range.
type metaShard struct {
	metas []*corpusBlobMeta
	bytes int64 // total size of metas' blobs
}

func scanMetaRange(s sorted.KeyValue, r keyRange) (sh metaShard, err error) {
	it := s.Find(r.start, r.end)
	defer closeIterator(it, &err)
	for it.Next() {
		bm, ok := kvBlobMeta_bytes(it.KeyBytes(), it.ValueBytes())
		if !ok {
			return sh, fmt.Errorf("bogus meta row: %q -> %q", it.KeyBytes(), it.ValueBytes())
		}
		sh.metas = append(sh.metas, newCorpusBlobMeta(bm))
		sh.bytes += int64(bm.Size)
	}
	return sh, nil
}

// metaSizingShards is how many scanned ranges scanMeta waits for before
// estimating how big to make its maps.
const metaSizingShards = 32

// scanMeta loads the "meta:" rows into c.blobs and c.camBlobs, which
// must be empty. It's the first and largest part of loading the corpus.
//
// Several goroutines scan and parse the rows, taking ranges of keys
// from scanRanges in turn, while this one adds them to the maps.
// It waits for a sample of ranges first, to estimate how big to make
// the maps, so they don't have to grow repeatedly.
func (c *Corpus) scanMeta(s sorted.KeyValue) error {
	if len(c.blobs) != 0 || len(c.camBlobs) != 0 {
		panic("scanMeta called with blobs already loaded")
	}
	t0 := time.Now()
	ranges := scanRanges("meta:")
	workers := min(runtime.GOMAXPROCS(0), len(ranges))

	grp, ctx := errgroup.WithContext(context.Background())
	shards := make(chan metaShard, workers)
	var next atomic.Int64 // index into ranges of the next range to scan
	for range workers {
		grp.Go(func() error {
			for ctx.Err() == nil {
				i := int(next.Add(1) - 1)
				if i >= len(ranges) {
					return nil
				}
				sh, err := scanMetaRange(s, ranges[i])
				if err != nil {
					return err
				}
				shards <- sh // never blocks for long: the loop below always drains shards
			}
			return nil
		})
	}
	scanErr := make(chan error, 1)
	go func() {
		scanErr <- grp.Wait()
		close(shards)
	}()

	var sample []metaShard // shards received before the maps were sized
	sized := false
	n := 0
	for sh := range shards {
		n++
		if !sized {
			sample = append(sample, sh)
			if n < metaSizingShards {
				continue
			}
			c.sizeMetaMaps(sample, n, len(ranges))
			sized = true
			for _, sh := range sample {
				c.mergeMetaShard(sh)
			}
			sample = nil
			continue
		}
		c.mergeMetaShard(sh)
	}
	for _, sh := range sample { // fewer ranges than metaSizingShards
		c.mergeMetaShard(sh)
	}
	if err := <-scanErr; err != nil {
		return err
	}
	if logCorpusStats {
		c.logf("loaded prefix %q: %d rows, %v (%d goroutines)", "meta", len(c.blobs), time.Since(t0), workers)
	}
	return nil
}

// sizeMetaMaps makes c.blobs and c.camBlobs, sized for all of the meta
// rows, estimated from sample, the shards from the first n of total ranges.
func (c *Corpus) sizeMetaMaps(sample []metaShard, n, total int) {
	rows := 0
	types := map[schema.CamliType]int{}
	for _, sh := range sample {
		rows += len(sh.metas)
		for _, m := range sh.metas {
			if t := m.camliTypeValue(); t != "" {
				types[t]++
			}
		}
	}
	// Leave a little room, since the estimate can be low.
	estimate := func(sampled int) int {
		e := sampled * total / n
		return e + e/16
	}
	c.blobs = make(map[blob.Ref]*corpusBlobMeta, estimate(rows))
	for t, sampled := range types {
		c.camBlobs[t] = make(map[blob.Ref]*corpusBlobMeta, estimate(sampled))
	}
}

// mergeMetaShard adds sh to c.blobs and c.camBlobs, like mergeBlobMeta
// does for each row when the corpus isn't being built.
func (c *Corpus) mergeMetaShard(sh metaShard) {
	for _, m := range sh.metas {
		n := len(c.blobs)
		c.blobs[m.Ref] = m
		if len(c.blobs) == n {
			panic("dup blob seen")
		}
		if t := m.camliTypeValue(); t != "" {
			cm, ok := c.camBlobs[t]
			if !ok {
				cm = make(map[blob.Ref]*corpusBlobMeta)
				c.camBlobs[t] = cm
			}
			cm[m.Ref] = m
		}
	}
	c.sumBlobBytes += sh.bytes
}
