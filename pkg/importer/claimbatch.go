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

package importer

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/schema"
)

// maxBatchClaimSize is about the most bytes of a ClaimBatch's multi
// claim's children, well within schema.MaxSchemaBlobSize.
const maxBatchClaimSize = 256 << 10

// A ClaimBatch sets the attributes of many objects with as few claims as
// hold them: multi claims of the changes of many objects each, rather than
// a claim of each object's, as Object.SetAttrs makes. That's fewer blobs
// to sign, store, verify and index, as when an importer imports many items
// at once.
//
// Each object's changes are in one claim, and so atomic, as with
// Object.SetAttrs, and its cached attributes change only once that's
// stored. A ClaimBatch's methods may be called from many goroutines, but
// it's committed once.
type ClaimBatch struct {
	h *Host

	mu      sync.Mutex
	items   []*batchItem
	byObj   map[*Object]*batchItem
	changed map[*Object]bool // by Commit
}

type batchItem struct {
	o   *Object
	kvs []KeyValue
}

// NewClaimBatch returns an empty claim batch.
func (h *Host) NewClaimBatch() *ClaimBatch {
	return &ClaimBatch{h: h, byObj: make(map[*Object]*batchItem)}
}

// SetAttrs adds setting o's attributes of kvs, as Object.SetAttrs sets
// them, to the batch. Credentials can't be set in a batch: use
// Object.SetAttrs for them.
func (b *ClaimBatch) SetAttrs(o *Object, kvs ...KeyValue) {
	for _, kv := range kvs {
		if isSecretAttribute(kv.Key) {
			panic("importer: ClaimBatch.SetAttrs of a credential, " + kv.Key)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	it, ok := b.byObj[o]
	if !ok {
		it = &batchItem{o: o}
		b.byObj[o] = it
		b.items = append(b.items, it)
	}
	it.kvs = append(it.kvs, kvs...)
}

// Changed reports whether Commit changed any of o's attributes.
func (b *ClaimBatch) Changed(o *Object) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.changed[o]
}

// Commit makes the batch's changes, with as few claims as hold them.
func (b *ClaimBatch) Commit(ctx context.Context) error {
	b.mu.Lock()
	items := slices.Clone(b.items)
	b.changed = make(map[*Object]bool)
	b.mu.Unlock()

	// Lock the objects, in order, so that batches don't deadlock, while
	// their claims are made and stored, so that no other write of them
	// comes between.
	slices.SortFunc(items, func(x, y *batchItem) int { return strings.Compare(x.o.pn.String(), y.o.pn.String()) })
	for _, it := range items {
		it.o.mu.Lock()
		defer it.o.mu.Unlock()
	}

	type change struct {
		it      *batchItem
		claims  []*schema.Builder
		updates map[string]string
		size    int
	}
	var changes []change
	for _, it := range items {
		if err := it.o.prepareWriteLocked(ctx); err != nil {
			return err
		}
		c := change{it: it, updates: make(map[string]string)}
		for _, kv := range it.kvs {
			key, val := kv.Key, kv.Value
			old, ok := c.updates[key]
			if !ok {
				if vs := it.o.attr[key]; len(vs) > 0 {
					old = vs[0]
				}
			}
			if val == old {
				continue
			}
			c.updates[key] = val
			c.claims = append(c.claims, schema.NewSetAttributeClaim(it.o.pn, key, val))
			c.size += len(key) + len(val) + 128
		}
		if len(c.claims) > 0 {
			changes = append(changes, c)
		}
	}

	// Claim, a multi claim at a time, the changes of as many objects as
	// fit in one.
	for len(changes) > 0 {
		n, size := 0, 0
		var claims []*schema.Builder
		for n < len(changes) && (n == 0 || size+changes[n].size <= maxBatchClaimSize) {
			claims = append(claims, changes[n].claims...)
			size += changes[n].size
			n++
		}
		batch := changes[:n]
		changes = changes[n:]

		claim, err := schema.NewMultiClaim(claims...)
		if err != nil {
			return err
		}
		// Date the claim after each of its objects' last, so that their
		// claims apply in the order they're made.
		date := time.Now()
		for _, c := range batch {
			if !date.After(c.it.o.lastClaim) {
				date = c.it.o.lastClaim.Add(time.Nanosecond)
			}
		}
		claim.SetClaimDate(date)
		if _, err := b.h.uploadAt(ctx, claim, date); err != nil {
			return err
		}
		b.mu.Lock()
		for _, c := range batch {
			o := c.it.o
			o.lastClaim = date
			if o.attr == nil {
				o.attr = make(map[string][]string)
			}
			for key, val := range c.updates {
				o.attr[key] = []string{val}
			}
			b.changed[o] = true
		}
		b.mu.Unlock()
	}
	return nil
}

// NewObjects returns n new objects, of new permanodes stored together.
func (h *Host) NewObjects(ctx context.Context, n int) ([]*Object, error) {
	objs := make([]*Object, n)
	blobs := make([]*blob.Blob, n)
	for i := range objs {
		signed, err := schema.NewUnsignedPermanode().Sign(ctx, h.signer)
		if err != nil {
			return nil, err
		}
		br := blob.RefFromString(signed)
		blobs[i] = blob.NewBlob(br, uint32(len(signed)), func(context.Context) ([]byte, error) { return []byte(signed), nil })
		objs[i] = &Object{h: h, pn: br}
	}
	if err := blobserver.ReceiveBlobs(ctx, h.target, blobs); err != nil {
		return nil, err
	}
	return objs, nil
}

// ChildPathObjects returns the children of o of paths, as ChildPathObject
// does, making those it lacks together: their permanodes stored together,
// and their "camliPath:" attributes set with one claim.
func (o *Object) ChildPathObjects(ctx context.Context, paths []string) ([]*Object, error) {
	children := make([]*Object, len(paths))
	var missing []int          // of paths, the first of each path missing
	dups := map[string][]int{} // of paths, the later of each path missing
	var g errgroup.Group
	g.SetLimit(10)
	for i, p := range paths {
		v := o.Attr("camliPath:" + p)
		if v == "" {
			if _, ok := dups[p]; ok {
				dups[p] = append(dups[p], i)
			} else {
				dups[p] = nil
				missing = append(missing, i)
			}
			continue
		}
		g.Go(func() error {
			br, ok := blob.Parse(v)
			if !ok {
				return fmt.Errorf("invalid blobref %q already stored at camliPath %q", v, p)
			}
			var err error
			children[i], err = o.h.ObjectFromRef(br)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if len(missing) == 0 {
		return children, nil
	}
	made, err := o.h.NewObjects(ctx, len(missing))
	if err != nil {
		return nil, err
	}
	attrs := make([]KeyValue, 0, len(missing))
	for j, i := range missing {
		children[i] = made[j]
		for _, d := range dups[paths[i]] {
			children[d] = made[j]
		}
		attrs = append(attrs, KV("camliPath:"+paths[i], made[j].pn.String()))
	}
	if err := o.SetAttrs(attrs...); err != nil {
		return nil, err
	}
	return children, nil
}
