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
	"maps"
	"runtime"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/sorted"
	"perkeep.org/pkg/types/camtypes"
)

// scanUnit is one piece of the work of loading the corpus after its meta
// and signer rows: either all of the rows with some prefixes, in turn, or
// one range of the claim rows.
type scanUnit struct {
	prefixes []string // if non-empty, the prefixes to scan
	claims   keyRange // otherwise, the claim rows to scan
}

// restScanUnits returns the units that together load the rest of
// slurpPrefixes after the meta and signer rows. Each claim range is a
// unit; all of a permanode's claims are in the same one, since claim
// keys start with the permanode.
func restScanUnits() []scanUnit {
	var units []scanUnit
	var files []string
	for _, prefix := range slurpPrefixes[2:] {
		switch prefix {
		case "claim|":
		case "fileinfo|", keyFileTimes.name + "|":
			files = append(files, prefix)
		default:
			units = append(units, scanUnit{prefixes: []string{prefix}})
		}
	}
	// fileinfo and filetimes rows both update the same FileInfos, so
	// they're loaded by the same worker, in turn.
	units = append(units, scanUnit{prefixes: files})
	for _, r := range scanRanges("claim|") {
		units = append(units, scanUnit{claims: r})
	}
	return units
}

// newScanWorker returns a corpus for one of scanRest's goroutines to load
// rows into, to be merged into c afterwards. It shares c's blobs and keyId
// maps, which are complete by then and only read from.
func (c *Corpus) newScanWorker() *Corpus {
	w := newCorpus()
	w.logger = c.logger
	w.building = true
	w.blobs = c.blobs
	w.keyId = c.keyId
	return w
}

// scanRest loads the rest of slurpPrefixes after the meta and signer rows,
// with NumCPU goroutines taking units from restScanUnits in turn. Each
// loads rows into its own corpus (see newScanWorker), which are merged
// into c at the end, so the per-row merge funcs and their caches (such as
// for interning) aren't shared between goroutines.
func (c *Corpus) scanRest(s sorted.KeyValue) error {
	t0 := time.Now()
	units := restScanUnits()
	workers := make([]*Corpus, min(runtime.GOMAXPROCS(0), len(units)))

	var claimRows atomic.Int64
	var claimRangesLeft atomic.Int64
	for _, u := range units {
		if len(u.prefixes) == 0 {
			claimRangesLeft.Add(1)
		}
	}

	grp, ctx := errgroup.WithContext(context.Background())
	var next atomic.Int64 // index into units of the next one to load
	for i := range workers {
		w := c.newScanWorker()
		workers[i] = w
		grp.Go(func() error {
			for ctx.Err() == nil {
				i := int(next.Add(1) - 1)
				if i >= len(units) {
					break
				}
				u := units[i]
				if len(u.prefixes) == 0 {
					n, err := w.scanRange(s, u.claims, (*Corpus).mergeClaimRow)
					if err != nil {
						return err
					}
					claimRows.Add(int64(n))
					if claimRangesLeft.Add(-1) == 0 && logCorpusStats {
						c.logf("loaded prefix %q: %d rows, %v", "claim", claimRows.Load(), time.Since(t0))
					}
					continue
				}
				for _, prefix := range u.prefixes {
					n, err := w.scanRange(s, prefixRange(prefix), corpusMergeFunc[typeOfKey(prefix)])
					if err != nil {
						return err
					}
					if logCorpusStats {
						c.logf("loaded prefix %q: %d rows, %v", prefix[:len(prefix)-1], n, time.Since(t0))
					}
				}
			}
			// Its claim ranges included all of the claims of each of
			// its permanodes, so they can be put in order now. The
			// parent corpus's deletion map is complete and read-only.
			for _, pm := range w.permanodes {
				if err := pm.restoreInvariants(c.keyId, c.IsDeleted); err != nil {
					return err
				}
			}
			return nil
		})
	}
	if err := grp.Wait(); err != nil {
		return err
	}
	c.mergeScanWorkers(workers)
	return nil
}

// scanRange calls fn on w for each row in r, returning how many there were.
func (w *Corpus) scanRange(s sorted.KeyValue, r keyRange, fn func(c *Corpus, k, v []byte) error) (n int, err error) {
	it := s.Find(r.start, r.end)
	defer closeIterator(it, &err)
	for it.Next() {
		n++
		if err := fn(w, it.KeyBytes(), it.ValueBytes()); err != nil {
			return n, err
		}
	}
	return n, nil
}

// mergeScanWorkers adds what scanRest's workers loaded to c. Workers
// loaded different prefixes, or claims for different permanodes, so
// their maps have different keys, except for claimBack and
// permanodesSetByNodeType, whose values are combined.
func (c *Corpus) mergeScanWorkers(workers []*Corpus) {
	mergeDisjoint(&c.files, workers, func(w *Corpus) map[blob.Ref]camtypes.FileInfo { return w.files })
	mergeDisjoint(&c.permanodes, workers, func(w *Corpus) map[blob.Ref]*PermanodeMeta { return w.permanodes })
	mergeDisjoint(&c.imageInfo, workers, func(w *Corpus) map[blob.Ref]camtypes.ImageInfo { return w.imageInfo })
	mergeDisjoint(&c.fileWholeRef, workers, func(w *Corpus) map[blob.Ref]blob.Ref { return w.fileWholeRef })
	mergeDisjoint(&c.fileOfWholeRef, workers, func(w *Corpus) map[blob.Ref]blob.Ref { return w.fileOfWholeRef })
	mergeDisjoint(&c.moreFilesOfWholeRef, workers, func(w *Corpus) map[blob.Ref][]blob.Ref { return w.moreFilesOfWholeRef })
	mergeDisjoint(&c.gps, workers, func(w *Corpus) map[blob.Ref]latLong { return w.gps })
	mergeDisjoint(&c.camera, workers, func(w *Corpus) map[blob.Ref]cameraInfo { return w.camera })
	mergeDisjoint(&c.mediaTags, workers, func(w *Corpus) map[blob.Ref]map[string]string { return w.mediaTags })
	mergeDisjoint(&c.dirChildren, workers, func(w *Corpus) map[blob.Ref]map[blob.Ref]struct{} { return w.dirChildren })
	mergeDisjoint(&c.fileParents, workers, func(w *Corpus) map[blob.Ref]map[blob.Ref]struct{} { return w.fileParents })
	for _, w := range workers {
		for br, claims := range w.claimBack {
			// The order of a claimBack slice doesn't matter.
			c.claimBack[br] = append(c.claimBack[br], claims...)
		}
		for nodeType, pns := range w.permanodesSetByNodeType {
			if dst, ok := c.permanodesSetByNodeType[nodeType]; ok {
				dst.AddSet(pns)
			} else {
				c.permanodesSetByNodeType[nodeType] = pns
			}
		}
		c.hasLegacySHA1 = c.hasLegacySHA1 || w.hasLegacySHA1
		c.brInterns += w.brInterns
	}
}

// mergeDisjoint adds the entries of each worker's map (as returned by m)
// to *dst. The maps' keys must not overlap. If only one has any entries
// and *dst is empty, *dst becomes that map; otherwise *dst is replaced by
// a map sized for all of them.
func mergeDisjoint[K comparable, V any](dst *map[K]V, workers []*Corpus, m func(*Corpus) map[K]V) {
	total := len(*dst)
	var only map[K]V // the only non-empty source, if just one
	sources := 0
	for _, w := range workers {
		if src := m(w); len(src) > 0 {
			total += len(src)
			only = src
			sources++
		}
	}
	switch {
	case sources == 0:
		return
	case sources == 1 && len(*dst) == 0:
		*dst = only
		return
	}
	merged := make(map[K]V, total)
	maps.Copy(merged, *dst)
	for _, w := range workers {
		maps.Copy(merged, m(w))
	}
	if len(merged) != total {
		panic("corpus scan workers loaded the same key")
	}
	*dst = merged
}
