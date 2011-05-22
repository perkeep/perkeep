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
	"camli/blobref"
)

// ListMissingDestinationBlobs reads from 'srcch' and 'dstch' (sorted
// enumerations of blobs from two blob servers) and sends to
// 'destMissing' any blobs which appear on the source but not at the
// destination.  destMissing is closed at the end.
func ListMissingDestinationBlobs(destMissing, srcch, dstch chan blobref.SizedBlobRef) {
	defer close(destMissing)

	src := &blobref.ChanPeeker{Ch: srcch}
	dst := &blobref.ChanPeeker{Ch: dstch}

	for src.Peek() != nil {
		// If the destination has reached its end, anything
		// remaining in the source is needed.
		if dst.Peek() == nil {
			destMissing <- *(src.Take())
			continue
		}

		srcStr := src.Peek().BlobRef.String()
		dstStr := dst.Peek().BlobRef.String()

		switch {
		case srcStr == dstStr:
			// Skip both
			src.Take()
			dst.Take()
		case srcStr < dstStr:
			destMissing <- *(src.Take())
		case srcStr > dstStr:
			dst.Take()
		}
	}
}
