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
	"strconv"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/sorted"
)

func (ix *Index) EnumerateBlobs(ctx *context.Context, dest chan<- blob.SizedRef, after string, limit int) (err error) {
	defer close(dest)
	it := ix.s.Find("have:"+after, "have~")
	defer func() {
		closeErr := it.Close()
		if err == nil {
			err = closeErr
		}
	}()

	n := int(0)
	for n < limit && it.Next() {
		k := it.Key()
		if k <= after {
			continue
		}
		if !strings.HasPrefix(k, "have:") {
			break
		}
		n++
		br, ok := blob.Parse(k[len("have:"):])
		size, err := strconv.ParseUint(it.Value(), 10, 32)
		if ok && err == nil {
			select {
			case dest <- blob.SizedRef{br, int64(size)}:
			case <-ctx.Done():
				return context.ErrCanceled
			}
		}
	}
	return nil
}

func (ix *Index) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	for _, br := range blobs {
		key := "have:" + br.String()
		v, err := ix.s.Get(key)
		if err == sorted.ErrNotFound {
			continue
		}
		if err != nil {
			return fmt.Errorf("error looking up key %q: %v", key, err)
		}
		size, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid size for key %q = %q", key, v)
		}
		dest <- blob.SizedRef{br, size}
	}
	return nil
}
