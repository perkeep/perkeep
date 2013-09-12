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

package s3

import (
	"fmt"
	"os"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/syncutil"
)

var statGate = syncutil.NewGate(20) // arbitrary

func (sto *s3Storage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	errc := make(chan error, len(blobs))
	for _, br := range blobs {
		statGate.Start()
		go func(br blob.Ref) {
			defer statGate.Done()
			size, err := sto.s3Client.Stat(br.String(), sto.bucket)
			switch err {
			case nil:
				dest <- blob.SizedRef{Ref: br, Size: size}
				errc <- nil
			case os.ErrNotExist:
				errc <- nil
			default:
				errc <- fmt.Errorf("error statting %v: %v", br, err)
			}
		}(br)
	}
	var firstErr error
	for _ = range blobs {
		if err := <-errc; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
