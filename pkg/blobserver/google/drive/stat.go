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

package drive

import (
	"context"
	"os"

	"go4.org/syncutil"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

var statGate = syncutil.NewGate(20) // arbitrary

func (sto *driveStorage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	return blobserver.StatBlobsParallelHelper(ctx, blobs, fn, statGate, func(br blob.Ref) (sb blob.SizedRef, err error) {
		size, err := sto.service.Stat(ctx, br.String())
		switch err {
		case os.ErrNotExist:
			return sb, nil
		case nil:
			return blob.SizedRef{Ref: br, Size: uint32(size)}, nil
		default:
			return sb, err
		}
	})
}
