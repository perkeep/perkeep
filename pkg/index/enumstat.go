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
	"camlistore.org/pkg/sorted"
	"golang.org/x/net/context"
)

func (ix *Index) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) (err error) {
	defer close(dest)
	it := ix.s.Find("have:"+after, "have~")
	defer func() {
		closeErr := it.Close()
		if err == nil {
			err = closeErr
		}
	}()

	afterKey := "have:" + after
	n := int(0)
	for n < limit && it.Next() {
		k := it.Key()
		if k <= afterKey {
			continue
		}
		if !strings.HasPrefix(k, "have:") {
			break
		}
		n++
		br, ok := blob.Parse(k[len("have:"):])
		if !ok {
			continue
		}
		size, err := parseHaveVal(it.Value())
		if err == nil {
			select {
			case dest <- blob.SizedRef{br, uint32(size)}:
			case <-ctx.Done():
				return ctx.Err()
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
		size, err := parseHaveVal(v)
		if err != nil {
			return fmt.Errorf("invalid size for key %q = %q", key, v)
		}
		dest <- blob.SizedRef{br, uint32(size)}
	}
	return nil
}

// parseHaveVal takes the value part of an "have" index row and returns
// the blob size found in that value. Examples:
// parseHaveVal("324|indexed") == 324
// parseHaveVal("654") == 654
func parseHaveVal(val string) (size uint64, err error) {
	pipei := strings.Index(val, "|")
	if pipei >= 0 {
		// filter out the "indexed" suffix
		val = val[:pipei]
	}
	return strconv.ParseUint(val, 10, 32)
}
