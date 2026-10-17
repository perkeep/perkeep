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
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/health"
	"perkeep.org/pkg/index"
)

// scrubWorkers is how many shards of a storage its scrub checks at once.
const scrubWorkers = 4

// maxScrubProblems is how many of a shard's bad blobs its problem names;
// the log has them all.
const maxScrubProblems = 3

// startScrubs registers a scrub for each storage that the census c
// samples, other than indexes: a sharded health check that fetches each
// of the storage's blobs and checks that it matches its digest, one
// digest prefix of each of its hash types at a time. Bad blobs make the
// check fail. Scrubs only run when asked to, until their schedules are
// set.
func (hl *handlerLoader) startScrubs(c *census) {
	if c == nil {
		return
	}
	for _, pfx := range c.prefixes {
		if combine, _ := c.composition(pfx); combine != nil {
			continue // its parts are scrubbed instead
		}
		sto, ok := hl.handler[pfx].(blobserver.Storage)
		if !ok {
			continue
		}
		if _, ok := sto.(*index.Index); ok {
			continue // it doesn't have the blobs' contents
		}
		s := &scrub{c: c, prefix: pfx, sto: sto}
		s.check = hl.health.Register(health.ID("storage.scrub", pfx), "scrub: the blobs in "+pfx+" match their digests")
		s.check.SetShardedRunner(s.shards, s.scrubShard, scrubWorkers, health.ScheduleOff)
		s.check.FailOnShardProblems()
		if _, ok := blobserver.PricingOf(sto); ok {
			s.check.SetCostEstimator(s.cost)
		}
	}
}

// scrub is the scrub of one storage.
type scrub struct {
	c      *census
	prefix string
	sto    blobserver.Storage
	check  *health.Check

	mu  sync.Mutex
	run scrubRun // the latest run's progress
}

// shards returns the shards of the scrub: 256 digest prefixes of each of
// the storage's hash types, according to the census, or else by listing
// the first blob of each type.
func (s *scrub) shards(ctx context.Context) []string {
	// It's called at the start of each run.
	s.mu.Lock()
	s.run = scrubRun{byHash: make(map[string]*blobserver.HashSample), pos: make(map[string]float64)}
	s.mu.Unlock()

	ests, ok := s.c.estimates(s.prefix)
	if !ok {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		var err error
		if ests, err = blobserver.EstimateByHashType(ctx, s.sto, 1); err != nil {
			log.Printf("scrub %s: listing its hash types: %v", s.prefix, err)
			return nil
		}
	}
	var hashes []string
	for _, e := range ests {
		if e.Blobs > 0 {
			hashes = append(hashes, e.Hash)
		}
	}
	s.check.SetProgressHint(fmt.Sprintf("shards that don't need checking, of %d: 256 digest prefixes × {%s}, the storage's hash types", 256*len(hashes), strings.Join(hashes, ", ")))
	var shards []string
	for _, hash := range hashes {
		for i := range 256 {
			shards = append(shards, fmt.Sprintf("%s-%02x", hash, i))
		}
	}
	return shards
}

var errShardDone = errors.New("sentinel error: past the shard")

// scrubShard checks each of the storage's blobs whose refs start with
// shard, and returns a problem naming those that are bad.
func (s *scrub) scrubShard(ctx context.Context, shard string) (problem string, err error) {
	var bad []string
	n := 0
	err = blobserver.EnumerateAllFrom(ctx, s.sto, shard, func(sb blob.SizedRef) error {
		if !strings.HasPrefix(sb.Ref.String(), shard) {
			return errShardDone
		}
		n++
		if p := checkBlob(ctx, s.sto, sb); p != "" {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("scrub %s: %s", s.prefix, p)
			bad = append(bad, p)
		}
		s.checked(shard, sb)
		return nil
	})
	if errors.Is(err, errShardDone) {
		err = nil
	}
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.run.pos[shard] = 1
	s.mu.Unlock()
	if len(bad) == 0 {
		return "", nil
	}
	shown := bad[:min(len(bad), maxScrubProblems)]
	more := ""
	if len(bad) > len(shown) {
		more = fmt.Sprintf("; and %d more (see the log)", len(bad)-len(shown))
	}
	return fmt.Sprintf("%d of %d blobs are bad: %s%s", len(bad), n, strings.Join(shown, "; "), more), nil
}

// checkBlob fetches sb from sto and checks that it's sb.Size bytes that
// match its digest, returning a description of what's wrong if not.
func checkBlob(ctx context.Context, sto blob.Fetcher, sb blob.SizedRef) string {
	rc, size, err := sto.Fetch(ctx, sb.Ref)
	if err != nil {
		return fmt.Sprintf("%v: can't fetch it: %v", sb.Ref, err)
	}
	defer rc.Close()
	h := sb.Ref.Hash()
	if h == nil {
		return fmt.Sprintf("%v: unsupported hash type", sb.Ref)
	}
	n, err := io.Copy(h, rc)
	switch {
	case err != nil:
		return fmt.Sprintf("%v: can't read it: %v", sb.Ref, err)
	case size != sb.Size || n != int64(sb.Size):
		return fmt.Sprintf("%v: is %d bytes (fetched as %d), but listed as %d", sb.Ref, n, size, sb.Size)
	case !sb.Ref.HashMatches(h):
		return fmt.Sprintf("%v: doesn't match its digest", sb.Ref)
	}
	return ""
}

// scrubReportInterval is how often a scrub's run reports how it's going,
// after it reports its first blob.
const scrubReportInterval = 5 * time.Second

// scrubRun is the progress of a scrub's run.
type scrubRun struct {
	reported time.Time                         // when it was last reported, or zero
	all      blobserver.HashSample             // the blobs checked
	byHash   map[string]*blobserver.HashSample // the blobs checked, by hash type
	// pos is how far through each shard started in the run it's
	// checked, from 0 to 1.
	pos map[string]float64
}

// checked notes that the scrub's run checked sb, in shard, and reports
// how the run is going if it's the run's first blob, or if it hasn't for
// scrubReportInterval.
func (s *scrub) checked(shard string, sb blob.SizedRef) {
	s.mu.Lock()
	r := &s.run
	r.all.Add(sb.Size)
	hash := sb.Ref.HashName()
	hs := r.byHash[hash]
	if hs == nil {
		hs = new(blobserver.HashSample)
		r.byHash[hash] = hs
	}
	hs.Add(sb.Size)
	r.pos[shard] = shardPos(shard, sb.Ref)
	now := time.Now()
	report := r.reported.IsZero() || now.Sub(r.reported) >= scrubReportInterval
	if report {
		r.reported = now
	}
	s.mu.Unlock()
	if report {
		s.check.RunningMessage(s.progress())
	}
}

// shardPos returns how far through shard, a hash type and hex digest
// prefix such as "sha224-3a", ref is, from 0 to 1.
func shardPos(shard string, ref blob.Ref) float64 {
	hash, hex, _ := strings.Cut(shard, "-")
	v, err := strconv.ParseUint(hex, 16, 64)
	if err != nil {
		return 0
	}
	digest := strings.TrimPrefix(ref.String(), hash+"-")
	pos := blobserver.DigestFraction(digest)*shardsOfDigests(hex) - float64(v)
	return min(1, max(0, pos))
}

// shardsOfDigests returns how many shards the space of digests has, if
// each is the digests with a prefix as long as hex.
func shardsOfDigests(hex string) float64 {
	return math.Ldexp(1, 4*len(hex))
}

// progress describes how the scrub's run is going: about how many of the
// storage's blobs don't need checking, or have been checked in the
// shards it's checking, of an estimate of how many there are, which is
// the census's until the run has checked more blobs of a type than the
// census sampled; the same as a fraction of the shards, and only
// counting those it's finished, which a restart doesn't check again;
// and how many blobs the run itself has checked.
func (s *scrub) progress() string {
	done, inProgress, total := s.check.ShardProgress()
	ests, _ := s.c.estimates(s.prefix)
	s.mu.Lock()
	defer s.mu.Unlock()
	r := &s.run

	covered := make(map[string]float64) // the fraction of each hash type's digests the run checked
	for shard, pos := range r.pos {
		hash, hex, _ := strings.Cut(shard, "-")
		covered[hash] += pos / shardsOfDigests(hex)
	}
	// estimate estimates the blobs of type hash from those the run
	// checked.
	estimate := func(hash string) blobserver.HashEstimate {
		hs := r.byHash[hash]
		if covered[hash] >= 1 {
			return blobserver.HashEstimate{Hash: hash, Blobs: hs.Blobs, Bytes: hs.Bytes, Exact: true}
		}
		return hs.Estimate(hash, covered[hash])
	}
	byHash := make(map[string]blobserver.HashEstimate)
	for _, e := range ests {
		if hs := r.byHash[e.Hash]; hs != nil && (hs.Blobs > censusSample || covered[e.Hash] >= 1) {
			e = estimate(e.Hash)
		}
		byHash[e.Hash] = e
	}
	for hash := range r.byHash {
		if _, ok := byHash[hash]; !ok {
			byHash[hash] = estimate(hash)
		}
	}
	sum := blobserver.HashEstimate{Exact: true}
	for _, e := range byHash {
		sum.Blobs += e.Blobs
		sum.Bytes += e.Bytes
		sum.BlobsErr = blobserver.AddErrs(sum.BlobsErr, e.BlobsErr)
		sum.BytesErr = blobserver.AddErrs(sum.BytesErr, e.BytesErr)
		sum.Exact = sum.Exact && e.Exact
	}

	run := fmt.Sprintf("%d blobs (%s) in this run", r.all.Blobs, blobserver.HashEstimate{Bytes: r.all.Bytes}.BytesString())
	if total == 0 || sum.Blobs == 0 {
		return "scrubbed " + run
	}
	// The blobs checked, of each hash type, are estimated from the
	// fraction of its digests in the shards done, and how far through
	// those being checked the run is.
	var checked, partial float64
	shardBlobs := func(shard string) float64 {
		hash, hex, _ := strings.Cut(shard, "-")
		return float64(byHash[hash].Blobs) / shardsOfDigests(hex)
	}
	for _, shard := range done {
		checked += shardBlobs(shard)
	}
	for _, shard := range inProgress {
		partial += r.pos[shard]
		checked += r.pos[shard] * shardBlobs(shard)
	}
	return fmt.Sprintf("≈%s of %s blobs (%s) checked: %0.2f%%, %0.2f%% checkpointed; %s",
		blobserver.HashEstimate{Blobs: int64(checked)}.BlobsString(), sum.BlobsWithErr(), sum.BytesWithErr(),
		100*(float64(len(done))+partial)/float64(total), 100*float64(len(done))/float64(total), run)
}

// cost estimates what a full pass of the scrub costs: listing the
// storage's blobs, and fetching them all.
func (s *scrub) cost() string {
	p, ok := blobserver.PricingOf(s.sto)
	if !ok {
		return ""
	}
	ests, ok := s.c.estimates(s.prefix)
	if !ok {
		return "unknown until the storage census has counted the blobs in " + s.prefix
	}
	var e blobserver.CostEstimate
	var blobs, bytes, requests int64
	for _, est := range ests {
		const shards = 256
		perShard := (est.Blobs/shards + int64(p.ListPage) - 1) / int64(p.ListPage)
		requests += shards * max(1, perShard)
		blobs += est.Blobs
		bytes += est.Bytes
	}
	e.AddListing(p, blobs, requests)
	e.AddGets(p, blobs, bytes)
	return "a full pass: " + e.String()
}
