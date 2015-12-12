/*
Copyright 2011 Google Inc.

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
	"camlistore.org/pkg/blob"
	"golang.org/x/net/context"
)

const buffered = 8

// MergedEnumerate implements the BlobEnumerator interface by
// merge-joining 0 or more sources.
func MergedEnumerate(ctx context.Context, dest chan<- blob.SizedRef, sources []BlobEnumerator, after string, limit int) error {
	return mergedEnumerate(ctx, dest, len(sources), func(i int) BlobEnumerator { return sources[i] }, after, limit)
}

// MergedEnumerateStorage implements the BlobEnumerator interface by
// merge-joining 0 or more sources.
//
// In this version, the sources implement the Storage interface, even
// though only the BlobEnumerator interface is used.
func MergedEnumerateStorage(ctx context.Context, dest chan<- blob.SizedRef, sources []Storage, after string, limit int) error {
	return mergedEnumerate(ctx, dest, len(sources), func(i int) BlobEnumerator { return sources[i] }, after, limit)
}

func mergedEnumerate(ctx context.Context, dest chan<- blob.SizedRef, nsrc int, getSource func(int) BlobEnumerator, after string, limit int) error {
	defer close(dest)

	subctx, cancel := context.WithCancel(ctx)
	defer cancel()

	startEnum := func(source BlobEnumerator) (*blob.ChanPeeker, <-chan error) {
		ch := make(chan blob.SizedRef, buffered)
		errch := make(chan error, 1)
		go func() {
			errch <- source.EnumerateBlobs(subctx, ch, after, limit)
		}()
		return &blob.ChanPeeker{Ch: ch}, errch
	}

	peekers := make([]*blob.ChanPeeker, 0, nsrc)
	errs := make([]<-chan error, 0, nsrc)
	for i := 0; i < nsrc; i++ {
		peeker, errch := startEnum(getSource(i))
		peekers = append(peekers, peeker)
		errs = append(errs, errch)
	}

	nSent := 0
	var lastSent blob.Ref
	tooLow := func(br blob.Ref) bool { return lastSent.Valid() && (br == lastSent || br.Less(lastSent)) }
	for nSent < limit {
		lowestIdx := -1
		var lowest blob.SizedRef
		for idx, peeker := range peekers {
			for !peeker.Closed() && tooLow(peeker.MustPeek().Ref) {
				peeker.Take()
			}
			if peeker.Closed() {
				continue
			}
			sb := peeker.MustPeek() // can't be nil if not Closed
			if lowestIdx == -1 || sb.Ref.Less(lowest.Ref) {
				lowestIdx = idx
				lowest = sb
			}
		}
		if lowestIdx == -1 {
			// all closed
			break
		}

		dest <- lowest
		nSent++
		lastSent = lowest.Ref
	}

	// If any part returns an error, we return an error.
	var retErr error
	for _, errch := range errs {
		if err := <-errch; err != nil {
			retErr = err
		}
	}
	return retErr
}
