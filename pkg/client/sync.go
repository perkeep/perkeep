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

package client

import (
	"camlistore.org/pkg/blob"
)

// ListMissingDestinationBlobs reads from 'srcch' and 'dstch' (sorted
// enumerations of blobs from two blob servers) and sends to
// 'destMissing' any blobs which appear on the source but not at the
// destination.  destMissing is closed at the end.
func ListMissingDestinationBlobs(destMissing chan<- blob.SizedRef, sizeMismatch func(blob.Ref), srcch, dstch <-chan blob.SizedRef) {
	defer close(destMissing)

	src := &blob.ChanPeeker{Ch: srcch}
	dst := &blob.ChanPeeker{Ch: dstch}

	for {
		_, ok := src.Peek()
		if !ok {
			break
		}

		// If the destination has reached its end, anything
		// remaining in the source is needed.
		if _, ok := dst.Peek(); !ok {
			destMissing <- src.MustTake()
			continue
		}

		srcStr := src.MustPeek().Ref
		dstStr := dst.MustPeek().Ref

		switch {
		case srcStr == dstStr:
			// Skip both
			sb := src.MustTake()
			db := dst.MustTake()
			if sb.Size != db.Size {
				sizeMismatch(sb.Ref)
			}
		case srcStr.Less(dstStr):
			destMissing <- src.MustTake()
		case dstStr.Less(srcStr):
			dst.Take()
		}
	}
}
