/*
Copyright 2014 The Perkeep Authors

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

package azure

import (
	"context"
	"os"

	"go4.org/syncutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

var statGate = syncutil.NewGate(20) // arbitrary

func (sto *azureStorage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) (err error) {
	// TODO: use sto.cache
	return blobserver.StatBlobsParallelHelper(ctx, blobs, fn, statGate, func(br blob.Ref) (sb blob.SizedRef, err error) {
		size, err := sto.azureClient.Stat(ctx, br.String(), sto.container)
		if err == nil {
			return blob.SizedRef{Ref: br, Size: uint32(size)}, nil
		}
		if err == os.ErrNotExist {
			return sb, nil
		}
		return sb, err
	})
}
