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
	"strings"
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
}

// shards returns the shards of the scrub: 256 digest prefixes of each of
// the storage's hash types, according to the census, or else by listing
// the first blob of each type.
func (s *scrub) shards(ctx context.Context) []string {
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
		return nil
	})
	if errors.Is(err, errShardDone) {
		err = nil
	}
	if err != nil {
		return "", err
	}
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
