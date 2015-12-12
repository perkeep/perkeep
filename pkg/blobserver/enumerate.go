/*
Copyright 2013 Google Inc.

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
	"sync"

	"camlistore.org/pkg/blob"
	"golang.org/x/net/context"
)

// EnumerateAll runs fn for each blob in src.
// If fn returns an error, iteration stops and fn isn't called again.
// EnumerateAll will not return concurrently with fn.
func EnumerateAll(ctx context.Context, src BlobEnumerator, fn func(blob.SizedRef) error) error {
	return EnumerateAllFrom(ctx, src, "", fn)
}

// EnumerateAllFrom is like EnumerateAll, but takes an after parameter.
func EnumerateAllFrom(ctx context.Context, src BlobEnumerator, after string, fn func(blob.SizedRef) error) error {
	const batchSize = 1000
	var mu sync.Mutex // protects returning with an error while fn is still running
	errc := make(chan error, 1)
	for {
		ch := make(chan blob.SizedRef, 16)
		n := 0
		go func() {
			var err error
			for sb := range ch {
				if err != nil {
					continue
				}
				mu.Lock()
				err = fn(sb)
				mu.Unlock()
				after = sb.Ref.String()
				n++
			}
			errc <- err
		}()
		err := src.EnumerateBlobs(ctx, ch, after, batchSize)
		if err != nil {
			mu.Lock() // make sure fn callback finished; no need to unlock
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

// RefTypes returns a list of blobref types appearing on the provided enumerator.
// A blobref type is a string like "sha1", or whatever is on the left side
// of the hyphen in a blobref.
// To get the alphabet valid for the right side of the hyphen, use blob.TypeAlphabet(type).
func RefTypes(src BlobEnumerator) ([]string, error) {
	// TODO(bradfitz): implement, with various short enumerate
	// reads at the right places.
	return []string{"sha1"}, nil
}
