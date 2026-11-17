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
	"testing"
	"testing/synctest"

	"go4.org/syncutil"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

func TestStatBlobsParallelHelperCanceledReleasesGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := syncutil.NewGate(1)
		br := blob.RefFromString("stat blob")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := blobserver.StatBlobsParallelHelper(ctx, []blob.Ref{br}, func(blob.SizedRef) error {
			t.Fatal("callback called after cancellation")
			return nil
		}, gate, func(blob.Ref) (blob.SizedRef, error) {
			t.Fatal("worker called after cancellation")
			return blob.SizedRef{}, nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("StatBlobsParallelHelper error = %v; want context.Canceled", err)
		}

		acquired := make(chan struct{})
		go func() {
			gate.Start()
			gate.Done()
			close(acquired)
		}()
		synctest.Wait()
		select {
		case <-acquired:
		default:
			// Unblock the probe before failing, even if the helper leaked the slot.
			gate.Done()
			synctest.Wait()
			t.Fatal("canceled stat leaked its gate slot")
		}
	})
}
