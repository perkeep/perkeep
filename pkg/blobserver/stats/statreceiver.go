/*
Copyright 2013 The Perkeep AUTHORS

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

// Package stats contains an in-memory StatReceiver that only stores sizes
// of received blobs but not their contents.
package stats // import "perkeep.org/pkg/blobserver/stats"

import (
	"context"
	"io"
	"io/ioutil"
	"sort"
	"sync"

	"perkeep.org/pkg/blob"
)

// Receiver is a dummy blobserver.StatReceiver that doesn't
// store anything; it just collects statistics.
//
// TODO: we have another copy of this same type in
// pk-put/files.go. move them to a common place?  well, the pk-put one
// is probably going away at some point.
type Receiver struct {
	sync.Mutex // guards Have
	Have       map[blob.Ref]int64
}

func (sr *Receiver) NumBlobs() int {
	sr.Lock()
	defer sr.Unlock()
	return len(sr.Have)
}

// Sizes returns the sorted blob sizes.
func (sr *Receiver) Sizes() []int {
	sr.Lock()
	defer sr.Unlock()
	sizes := make([]int, 0, len(sr.Have))
	for _, size := range sr.Have {
		sizes = append(sizes, int(size))
	}
	sort.Ints(sizes)
	return sizes
}

func (sr *Receiver) SumBlobSize() int64 {
	sr.Lock()
	defer sr.Unlock()
	var sum int64
	for _, v := range sr.Have {
		sum += v
	}
	return sum
}

func (sr *Receiver) ReceiveBlob(ctx context.Context, br blob.Ref, source io.Reader) (sb blob.SizedRef, err error) {
	n, err := io.Copy(ioutil.Discard, source)
	if err != nil {
		return
	}
	return sr.ReceiveRef(br, n)
}

func (sr *Receiver) ReceiveRef(br blob.Ref, size int64) (sb blob.SizedRef, err error) {
	sr.Lock()
	defer sr.Unlock()
	if sr.Have == nil {
		sr.Have = make(map[blob.Ref]int64)
	}
	sr.Have[br] = size
	return blob.SizedRef{br, uint32(size)}, nil
}

func (sr *Receiver) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	var sized []blob.SizedRef
	sr.Lock()
	for _, br := range blobs {
		if size, ok := sr.Have[br]; ok {
			sized = append(sized, blob.SizedRef{br, uint32(size)})
		}
	}
	sr.Unlock()

	// Call fn with no locks held:
	for _, sb := range sized {
		if err := fn(sb); err != nil {
			return err
		}
	}
	return nil
}

func (sr *Receiver) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	sr.Lock()
	defer sr.Unlock()
	for _, br := range blobs {
		delete(sr.Have, br)
	}

	return nil
}

func (sr *Receiver) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	sr.Lock()
	defer sr.Unlock()
	defer close(dest)

	refs := blob.SizedByRef{}
	for ref, size := range sr.Have {
		if after != "" && ref.String() <= after {
			continue
		}
		refs = append(refs, blob.SizedRef{Ref: ref, Size: uint32(size)})
	}
	sort.Sort(refs)

	if len(refs) == 0 {
		return nil
	}

	if len(refs) <= limit {
		limit = len(refs)
	}

	for _, sb := range refs[:limit] {
		dest <- sb
	}

	return nil
}
