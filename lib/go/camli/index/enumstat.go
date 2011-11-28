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

package index

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"camli/blobref"
)

func (ix *Index) EnumerateBlobs(dest chan<- blobref.SizedBlobRef, after string, limit uint, waitSeconds int) os.Error {
	defer close(dest)
	it := ix.s.Find("have:"+after)
	n := uint(0)
	for n < limit && it.Next() {
		k := it.Key()
		if !strings.HasPrefix(k, "have:") {
			break
		}
		n++
		br := blobref.Parse(k[len("have:"):])
		size, err := strconv.Atoi64(it.Value())
		if br != nil && err == nil {
			dest <- blobref.SizedBlobRef{br, size}
		}
	}
	return it.Close()
}

func (ix *Index) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, waitSeconds int) os.Error {
	for _, br := range blobs {
		key := "have:" + br.String()
		v, err := ix.s.Get(key)
		if err == ErrNotFound {
			continue
		}
		if err != nil {
			return fmt.Errorf("error looking up key %q: %v", key, err)
		}
		size, err := strconv.Atoi64(v)
		if err != nil {
			return fmt.Errorf("invalid size for key %q = %q", key, v)
		}
		dest <- blobref.SizedBlobRef{br, size}
	}
	return nil
}
