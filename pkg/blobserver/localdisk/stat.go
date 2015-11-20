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

package localdisk

import (
	"os"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/types"

	"go4.org/syncutil"
)

const maxParallelStats = 20

var statGate = syncutil.NewGate(maxParallelStats)

func (ds *DiskStorage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	if len(blobs) == 0 {
		return nil
	}

	statSend := func(ref blob.Ref) error {
		fi, err := os.Stat(ds.blobPath(ref))
		switch {
		case err == nil && fi.Mode().IsRegular():
			dest <- blob.SizedRef{Ref: ref, Size: types.U32(fi.Size())}
			return nil
		case err != nil && !os.IsNotExist(err):
			return err
		}
		return nil
	}

	if len(blobs) == 1 {
		return statSend(blobs[0])
	}

	var wg syncutil.Group
	for _, ref := range blobs {
		ref := ref
		statGate.Start()
		wg.Go(func() error {
			defer statGate.Done()
			return statSend(ref)
		})
	}
	return wg.Err()
}
