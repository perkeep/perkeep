/*
Copyright 2013 The Perkeep Authors

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

package blobserver

import (
	"context"
	"sync/atomic"

	"perkeep.org/pkg/blob"
)

// EnumerateAll runs fn for each blob in src.
// If fn returns an error, iteration stops and fn isn't called again.
// EnumerateAll will not return concurrently with fn.
func EnumerateAll(ctx context.Context, src BlobEnumerator, fn func(blob.SizedRef) error) error {
	return EnumerateAllFrom(ctx, src, "", fn)
}

// ListAllRefs calls fn once for each blob in src, in no particular
// order, using src's ListBlobRefs if it implements RefLister and
// EnumerateAll otherwise. Calls to fn are not concurrent. If fn
// returns an error, listing stops and ListAllRefs returns that error.
func ListAllRefs(ctx context.Context, src BlobEnumerator, fn func(blob.Ref) error) error {
	if rl, ok := src.(RefLister); ok {
		return rl.ListBlobRefs(ctx, fn)
	}
	return EnumerateAll(ctx, src, func(sb blob.SizedRef) error {
		return fn(sb.Ref)
	})
}

// EnumerateAllFrom is like EnumerateAll, but takes an after parameter.
func EnumerateAllFrom(ctx context.Context, src BlobEnumerator, after string, fn func(blob.SizedRef) error) error {
	const batchSize = 1000
	errc := make(chan error, 1)
	for {
		ch := make(chan blob.SizedRef, 16)
		n := 0
		var stop atomic.Bool // set if src fails, to not call fn again
		go func() {
			var err error
			for sb := range ch {
				if err != nil || stop.Load() {
					continue
				}
				err = fn(sb)
				after = sb.Ref.String()
				n++
			}
			errc <- err
		}()
		err := src.EnumerateBlobs(ctx, ch, after, batchSize)
		if err != nil {
			// Wait for fn to return, and for the rest of ch, which src
			// has closed, to drain.
			stop.Store(true)
			<-errc
			return err
		}
		if err := <-errc; err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
}
