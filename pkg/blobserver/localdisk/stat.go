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
	"sync"
	"time"

	"camlistore.org/pkg/blob"
)

const maxParallelStats = 20

func (ds *DiskStorage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref, wait time.Duration) error {
	if len(blobs) == 0 {
		return nil
	}

	var missingLock sync.Mutex
	var missing []blob.Ref

	statSend := func(ref blob.Ref, appendMissing bool) error {
		fi, err := os.Stat(ds.blobPath(ds.partition, ref))
		switch {
		case err == nil && !fi.IsDir():
			dest <- blob.SizedRef{Ref: ref, Size: fi.Size()}
		case err != nil && appendMissing && os.IsNotExist(err):
			missingLock.Lock()
			missing = append(missing, ref)
			missingLock.Unlock()
		case err != nil && !os.IsNotExist(err):
			return err
		}
		return nil
	}

	// Stat all the files in parallel if there are multiple
	if len(blobs) == 1 {
		if err := statSend(blobs[0], wait > 0); err != nil {
			return err
		}
	} else {
		errCh := make(chan error)
		rateLimiter := make(chan bool, maxParallelStats)
		for _, ref := range blobs {
			go func(ref blob.Ref) {
				rateLimiter <- true
				errCh <- statSend(ref, wait > 0)
				<-rateLimiter
			}(ref)
		}
		for _, _ = range blobs {
			if err := <-errCh; err != nil {
				return err
			}
		}
	}

	if needed := len(missing); needed > 0 {
		if wait > 1*time.Minute {
			// TODO: use a flag, defaulting to a minute?
			wait = 1 * time.Minute
		}
		hub := ds.GetBlobHub()
		ch := make(chan blob.Ref, 1)
		for _, missblob := range missing {
			hub.RegisterBlobListener(missblob, ch)
			defer hub.UnregisterBlobListener(missblob, ch)
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
			// Done waiting.
			return nil
		case blobref := <-ch:
			statSend(blobref, false)
			if needed--; needed == 0 {
				return nil
			}
		}
	}
	return nil
}
