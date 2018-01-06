/*
Copyright 2011 The Perkeep Authors

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

package localdisk

import (
	"context"
	"os"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"

	"go4.org/syncutil"
)

const maxParallelStats = 20

var statGate = syncutil.NewGate(maxParallelStats)

func (ds *DiskStorage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	return blobserver.StatBlobsParallelHelper(ctx, blobs, fn, statGate, func(ref blob.Ref) (sb blob.SizedRef, err error) {
		fi, err := os.Stat(ds.blobPath(ref))
		switch {
		case err == nil && fi.Mode().IsRegular():
			return blob.SizedRef{Ref: ref, Size: u32(fi.Size())}, nil
		case err != nil && !os.IsNotExist(err):
			return sb, err
		}
		return sb, nil

	})
}
