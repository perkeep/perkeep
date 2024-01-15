/*
Copyright 2018 The Perkeep Authors

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
	"os"
	"sync"

	"go4.org/syncutil"

	"perkeep.org/pkg/blob"
)

// StatBlob calls bs.StatBlobs to stat a single blob.
// If the blob is not found, the error is os.ErrNotExist.
func StatBlob(ctx context.Context, bs BlobStatter, br blob.Ref) (blob.SizedRef, error) {
	var ret blob.SizedRef
	err := bs.StatBlobs(ctx, []blob.Ref{br}, func(sb blob.SizedRef) error {
		ret = sb
		return nil
	})
	if err == nil && !ret.Ref.Valid() {
		err = os.ErrNotExist
	}
	return ret, err
}

// StatBlobs stats multiple blobs and returns a map
// of the found refs to their sizes.
func StatBlobs(ctx context.Context, bs BlobStatter, blobs []blob.Ref) (map[blob.Ref]blob.SizedRef, error) {
	var m map[blob.Ref]blob.SizedRef
	err := bs.StatBlobs(ctx, blobs, func(sb blob.SizedRef) error {
		if m == nil {
			m = make(map[blob.Ref]blob.SizedRef)
		}
		m[sb.Ref] = sb
		return nil
	})
	return m, err
}

// StatBlobsParallelHelper is for use by blobserver implementations
// that want to issue stats in parallel.  This runs worker in multiple
// goroutines (bounded by gate), but calls fn in serial, per the
// BlobStatter contract, and stops once there's a failure.
//
// The worker func should return two zero values to signal that a blob
// doesn't exist. (This is different than the StatBlob func, which
// returns os.ErrNotExist)
func StatBlobsParallelHelper(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error,
	gate *syncutil.Gate, worker func(blob.Ref) (blob.SizedRef, error)) error {
	if len(blobs) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var fnMu sync.Mutex // serializes calls to fn

	var wg syncutil.Group
Blobs:
	for i := range blobs {
		gate.Start()
		b := blobs[i]

		select {
		case <-ctx.Done():
			// If a previous failed, stop.
			break Blobs
		default:
		}

		wg.Go(func() error {
			defer gate.Done()

			sb, err := worker(b)
			if err != nil {
				cancel()
				return err
			}
			if !sb.Valid() {
				// not found.
				return nil
			}

			fnMu.Lock()
			defer fnMu.Unlock()

			select {
			case <-ctx.Done():
				// If a previous failed, stop.
				return ctx.Err()
			default:
			}

			if err := fn(sb); err != nil {
				cancel() // stop others from running
				return err
			}
			return nil
		})
	}

	if err := wg.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
