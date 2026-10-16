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

package serverinit

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"slices"
	"strings"
	"sync"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/health"
)

// censusSample is how many blobs of each hash type the census lists from
// each storage it samples (see blobserver.EstimateByHashType).
//
// An S3 listing returns up to 1000 blobs, so on S3 it's one request for
// each hash type.
const censusSample = 1000

// census estimates how many blobs of each hash type each storage has,
// reporting them to a health check for each. It samples the blobs of
// storage it can't derive from the storage it composes, and derives
// the rest: a "cond" storage's from the one it reads from, a "replica"'s
// and a "union"'s from the largest of its parts, and a "shard"'s from
// the sum of its shards.
//
// Between runs, it notices blobs that this server receives into a storage
// it samples (see blobserver.GetHub) of a hash type the storage didn't
// have, such as the first sha256 blobs of an importer that makes them,
// and counts them, so that the census (and what uses it, such as
// sync validations) knows of the new type before the next run.
type census struct {
	hl       *handlerLoader
	prefixes []string                 // of the storage handlers, sorted
	checks   map[string]*health.Check // by prefix

	mu sync.Mutex
	// results are the estimates of each storage, by prefix, as of the
	// last run, with the hash types received since.
	results map[string]censusResult
	// added are the hash types of each storage, by prefix, that it
	// didn't have at the last run, but has received blobs of since.
	added map[string]map[string]bool
}

// startCensus registers the census's checks with hl's health monitor: a
// row for each storage, and one that runs the census, at startup by
// default.
func (hl *handlerLoader) startCensus() {
	if hl.health == nil {
		return
	}
	c := &census{hl: hl, checks: make(map[string]*health.Check)}
	for pfx, h := range hl.config {
		if !strings.HasPrefix(h.htype, "storage-") {
			continue
		}
		if _, ok := hl.handler[pfx].(blobserver.BlobEnumerator); !ok {
			continue
		}
		c.prefixes = append(c.prefixes, pfx)
	}
	slices.Sort(c.prefixes)
	if len(c.prefixes) == 0 {
		return
	}
	for _, pfx := range c.prefixes {
		c.checks[pfx] = hl.health.Register(blobserver.CensusID(pfx), "census: the blobs in "+pfx+", by hash type")
	}
	for _, pfx := range c.prefixes {
		sto := hl.handler[pfx]
		// Hubs are by storage, so it must be comparable, as a pointer is.
		if combine, _ := c.composition(pfx); combine == nil && reflect.ValueOf(sto).Comparable() {
			blobserver.GetHub(sto).AddReceiveHook(func(sb blob.SizedRef) error {
				c.received(pfx, sb)
				return nil
			})
		}
	}
	runID := health.ID("storage.census", "all")
	run := hl.health.Register(runID, "census: estimate the blobs in each storage")
	run.SetProgressHint(fmt.Sprintf("storages counted; it lists up to %d blobs of each hash type from each one that isn't made of others", censusSample))
	// Daily, and at startup too, as the results aren't saved.
	run.SetRunner(func(ctx context.Context) error {
		return c.run(ctx, run)
	}, health.ScheduleDaily)
	if err := hl.health.RunNow(runID); err != nil {
		log.Printf("census: %v", err)
	}
}

// received notes that the storage at pfx, which the census samples,
// received sb. If the storage had no blobs of sb's hash type at the last
// run, the census adds it, counting the blobs it receives of it until
// the next run, and updates the storage made of it.
func (c *census) received(pfx string, sb blob.SizedRef) {
	hash := sb.Ref.HashName()
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.results[pfx]
	if !ok || r.err != nil {
		return // the next run counts it
	}
	i := slices.IndexFunc(r.ests, func(e blobserver.HashEstimate) bool { return e.Hash == hash })
	switch {
	case i < 0:
		if c.added[pfx] == nil {
			c.added[pfx] = make(map[string]bool)
		}
		c.added[pfx][hash] = true
		r.ests = append(slices.Clone(r.ests), blobserver.HashEstimate{Hash: hash, Exact: true})
		slices.SortFunc(r.ests, func(a, b blobserver.HashEstimate) int { return strings.Compare(a.Hash, b.Hash) })
		i = slices.IndexFunc(r.ests, func(e blobserver.HashEstimate) bool { return e.Hash == hash })
	case c.added[pfx][hash]:
		r.ests = slices.Clone(r.ests)
	default:
		return // the last run counted them
	}
	r.ests[i].Blobs++
	r.ests[i].Bytes += int64(sb.Size)
	c.results[pfx] = r
	c.deriveLocked()
	for _, p := range c.prefixes {
		if r, ok := c.results[p]; ok && r.err == nil {
			c.checks[p].SetDetails(r.ests)
		}
	}
}

// deriveLocked recomputes, from c.results, the results of the storage
// made of other storage.
func (c *census) deriveLocked() {
	done := make(map[string]bool)
	var visiting []string
	var derive func(pfx string) (censusResult, bool)
	derive = func(pfx string) (censusResult, bool) {
		combine, parts := c.composition(pfx)
		if combine == nil || done[pfx] {
			r, ok := c.results[pfx]
			return r, ok
		}
		if slices.Contains(visiting, pfx) {
			return censusResult{}, false // a loop; the run reports it
		}
		visiting = append(visiting, pfx)
		defer func() { visiting = visiting[:len(visiting)-1] }()
		var partEsts [][]blobserver.HashEstimate
		for _, part := range parts {
			pr, ok := derive(part)
			if !ok || pr.err != nil {
				return c.results[pfx], false
			}
			partEsts = append(partEsts, pr.ests)
		}
		r := censusResult{ests: combine(partEsts)}
		c.results[pfx] = r
		done[pfx] = true
		return r, true
	}
	for _, pfx := range c.prefixes {
		derive(pfx)
	}
}

// censusResult is a storage's estimates, or why there aren't any.
type censusResult struct {
	ests []blobserver.HashEstimate
	err  error
}

func (c *census) run(ctx context.Context, progress *health.Check) error {
	results := make(map[string]censusResult)
	var visiting []string
	var count func(pfx string) censusResult
	count = func(pfx string) censusResult {
		if r, ok := results[pfx]; ok {
			return r
		}
		if slices.Contains(visiting, pfx) {
			return censusResult{err: fmt.Errorf("loop in the storage config: %q", append(visiting, pfx))}
		}
		visiting = append(visiting, pfx)
		defer func() { visiting = visiting[:len(visiting)-1] }()

		var r censusResult
		if combine, parts := c.composition(pfx); combine != nil {
			var partEsts [][]blobserver.HashEstimate
			for _, part := range parts {
				pr := count(part)
				if pr.err != nil {
					r.err = fmt.Errorf("%s: %w", part, pr.err)
					break
				}
				partEsts = append(partEsts, pr.ests)
			}
			if r.err == nil {
				r.ests = combine(partEsts)
			}
		} else {
			sto := c.hl.handler[pfx].(blobserver.BlobEnumerator)
			r.ests, r.err = blobserver.EstimateByHashType(ctx, sto, censusSample)
		}
		results[pfx] = r
		return r
	}
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.results, c.added = results, make(map[string]map[string]bool)
	}()

	for i, pfx := range c.prefixes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r := count(pfx)
		check := c.checks[pfx]
		if r.err != nil && ctx.Err() == nil {
			check.Warn(fmt.Errorf("can't count its blobs: %w", r.err))
		} else if r.err == nil {
			// The status page's storage table shows them.
			check.SetDetails(r.ests)
			check.OK()
		}
		progress.Progress(int64(i+1), int64(len(c.prefixes)))
	}
	return nil
}

// composition returns, for the storage at pfx, how to combine the
// estimates of the storage it's made of, and their prefixes, or nil if
// its estimates must come from listing its own blobs.
func (c *census) composition(pfx string) (combine func([][]blobserver.HashEstimate) []blobserver.HashEstimate, parts []string) {
	h := c.hl.config[pfx]
	strs := func(key string) []string {
		var ss []string
		list, _ := h.conf[key].([]any)
		for _, v := range list {
			s, ok := v.(string)
			if !ok {
				return nil
			}
			ss = append(ss, s)
		}
		return ss
	}
	switch h.htype {
	case "storage-cond":
		if read, ok := h.conf["read"].(string); ok {
			return combineMax, []string{read}
		}
	case "storage-replica":
		if ps := strs("backends"); len(ps) > 0 {
			return combineMax, ps
		}
	case "storage-union":
		if ps := strs("subsets"); len(ps) > 0 {
			return combineMax, ps
		}
	case "storage-shard":
		if ps := strs("backends"); len(ps) > 0 {
			return combineSum, ps
		}
	}
	return nil, nil
}

// combineMax combines estimates by taking, for each hash type, the one
// of the most blobs: for storage whose parts have the same blobs (or
// overlap), such as replicas.
func combineMax(parts [][]blobserver.HashEstimate) []blobserver.HashEstimate {
	return combine(parts, func(a, b blobserver.HashEstimate) blobserver.HashEstimate {
		if b.Blobs > a.Blobs {
			return b
		}
		return a
	})
}

// combineSum combines estimates by adding them, for each hash type: for
// storage whose parts have different blobs, such as shards.
func combineSum(parts [][]blobserver.HashEstimate) []blobserver.HashEstimate {
	return combine(parts, func(a, b blobserver.HashEstimate) blobserver.HashEstimate {
		return blobserver.HashEstimate{
			Hash:     a.Hash,
			Blobs:    a.Blobs + b.Blobs,
			Bytes:    a.Bytes + b.Bytes,
			Exact:    a.Exact && b.Exact,
			BlobsErr: blobserver.AddErrs(a.BlobsErr, b.BlobsErr),
			BytesErr: blobserver.AddErrs(a.BytesErr, b.BytesErr),
		}
	})
}

func combine(parts [][]blobserver.HashEstimate, f func(a, b blobserver.HashEstimate) blobserver.HashEstimate) []blobserver.HashEstimate {
	var out []blobserver.HashEstimate
	for _, ests := range parts {
		for _, e := range ests {
			i := slices.IndexFunc(out, func(o blobserver.HashEstimate) bool { return o.Hash == e.Hash })
			if i < 0 {
				out = append(out, e)
				continue
			}
			out[i] = f(out[i], e)
		}
	}
	slices.SortFunc(out, func(a, b blobserver.HashEstimate) int { return strings.Compare(a.Hash, b.Hash) })
	return out
}
