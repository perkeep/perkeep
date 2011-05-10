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

	"camli/blobref"
)

const maxParallelStats = 20

func (ds *DiskStorage) Stat(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, waitSeconds int) os.Error {
	if len(blobs) == 0 {
		return nil
	}

	var missingLock sync.Mutex
	var missing []*blobref.BlobRef

	statSend := func(ref *blobref.BlobRef, appendMissing bool) os.Error {
		fi, err := os.Stat(ds.blobPath(ds.partition, ref))
		switch {
		case err == nil && fi.IsRegular():
			dest <- blobref.SizedBlobRef{BlobRef: ref, Size: fi.Size}
		case err != nil && appendMissing && errorIsNoEnt(err):
			missingLock.Lock()
			missing = append(missing, ref)
			missingLock.Unlock()
		case err != nil && !errorIsNoEnt(err):
			return err
		}
		return nil
	}

	// Stat all the files in parallel if there are multiple
	if len(blobs) == 1 {
		if err := statSend(blobs[0], waitSeconds > 0); err != nil {
			return err
		}
	} else {
		errCh := make(chan os.Error)
		rateLimiter := make(chan bool, maxParallelStats)
		for _, ref := range blobs {
			go func(ref *blobref.BlobRef) {
				rateLimiter <- true
				errCh <- statSend(ref, waitSeconds > 0)
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
		if waitSeconds > 60 {
			// TODO: use a flag, defaulting to 60?
			waitSeconds = 60
		}
		hub := ds.GetBlobHub()
		ch := make(chan *blobref.BlobRef, 1)
		for _, missblob := range missing {
			hub.RegisterBlobListener(missblob, ch)
			defer hub.UnregisterBlobListener(missblob, ch)
		}
		timer := time.NewTimer(int64(waitSeconds) * 1e9)
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
