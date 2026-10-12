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

package index

import (
	"io"
	"sync"

	"perkeep.org/pkg/schema"
)

var copyBufferPool = sync.Pool{
	New: func() any { return new([32 << 10]byte) },
}

// copyWithBuffer preserves io.Copy's fast paths, borrowing scratch only when
// neither endpoint supplies one. Callers can hide WriterTo when its
// implementation would allocate its own scratch (notably io.MultiReader).
func copyWithBuffer(dst io.Writer, src io.Reader) (int64, error) {
	if wt, ok := src.(io.WriterTo); ok {
		return wt.WriteTo(dst)
	}
	if rf, ok := dst.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	buf := copyBufferPool.Get().(*[32 << 10]byte)
	defer copyBufferPool.Put(buf)
	return io.CopyBuffer(dst, src, buf[:])
}

// Only ReceiveBlob borrows these buffers. Exported BlobSniffers must continue
// to own their buffers, since Body exposes the backing slice to callers.
// Grow lazily so cold pools don't allocate 1 MiB for every small upload.
var snifferBufferPool = sync.Pool{
	New: func() any { return new([]byte) },
}

// Allow bounded slice-growth slack beyond the retained schema prefix.
const maxPooledSnifferCapacity = 2 * schema.MaxSchemaBlobSize

func getSnifferBuffer() *[]byte {
	return snifferBufferPool.Get().(*[]byte)
}

func putSnifferBuffer(buf *[]byte, contents []byte) {
	if cap(contents) > maxPooledSnifferCapacity {
		*buf = nil
	} else {
		*buf = contents[:0]
	}
	snifferBufferPool.Put(buf)
}
