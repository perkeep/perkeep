/*
Copyright 2026 The Perkeep Authors

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

package blobsniffer

import (
	"sync"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
)

// Grow lazily so cold pools don't allocate 1 MiB for every small upload.
var snifferPool = sync.Pool{
	New: func() any { return new(BlobSniffer) },
}

// Allow bounded slice-growth slack beyond the retained schema prefix.
const maxPooledSnifferCapacity = 2 * schema.MaxSchemaBlobSize

// Get returns a pooled sniffer for ref, which must be valid. The caller must
// return it with Put, and must not retain its Body afterwards. SchemaBlob owns
// its decoded data and JSON and may outlive the sniffer.
func Get(ref blob.Ref) *BlobSniffer {
	if !ref.Valid() {
		panic("invalid ref")
	}
	sn := snifferPool.Get().(*BlobSniffer)
	sn.br = ref
	return sn
}

// Put returns a sniffer obtained from Get to the pool. Neither the sniffer nor
// its Body may be used afterwards. Sniffers returned by New are not pooled.
func Put(sn *BlobSniffer) {
	contents := sn.contents
	if cap(contents) > maxPooledSnifferCapacity {
		contents = nil
	}
	*sn = BlobSniffer{contents: contents[:0]}
	snifferPool.Put(sn)
}
