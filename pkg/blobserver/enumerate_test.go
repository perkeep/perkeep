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

package blobserver_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

// failingLister lists n blobs and then fails.
type failingLister struct{ n int }

var errListing = errors.New("listing failed")

func (l failingLister) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	for i := range l.n {
		dest <- blob.SizedRef{Ref: blob.RefFromString(fmt.Sprint(i)), Size: 1}
	}
	return errListing
}

// TestEnumerateAllFromListingError checks that when the listing fails
// while fn is still busy with the blobs before, EnumerateAllFrom returns
// its error once fn has returned, without leaving a goroutine behind,
// which synctest.Test would wait for forever.
func TestEnumerateAllFromListingError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		err := blobserver.EnumerateAllFrom(t.Context(), failingLister{n: 10}, "", func(blob.SizedRef) error {
			calls++
			time.Sleep(time.Second) // still busy when the listing fails
			return nil
		})
		if !errors.Is(err, errListing) {
			t.Errorf("err = %v; want %v", err, errListing)
		}
		if calls > 10 {
			t.Errorf("fn called %d times; want at most 10", calls)
		}
	})
}
