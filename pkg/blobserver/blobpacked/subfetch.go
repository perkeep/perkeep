/*
Copyright 2014 The Perkeep AUTHORS

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

package blobpacked

import (
	"context"
	"io"

	"perkeep.org/pkg/blob"
)

// Ensure we implement the optional interface correctly.
var _ blob.SubFetcher = (*storage)(nil)

// SubFetch returns part of a blob.
// The caller must close the returned io.ReadCloser.
// The Reader may return fewer than 'length' bytes. Callers should
// check. The returned error should be os.ErrNotExist if the blob
// doesn't exist.
func (s *storage) SubFetch(ctx context.Context, ref blob.Ref, offset, length int64) (io.ReadCloser, error) {
	// TODO: pass ctx to more calls within this method.
	m, err := s.getMetaRow(ref)
	if err != nil {
		return nil, err
	}
	if m.isPacked() {
		length, err = capOffsetLength(m.size, offset, length)
		if err != nil {
			return nil, err
		}
		// get the blob from the large subfetcher
		return s.large.SubFetch(ctx, m.largeRef, int64(m.largeOff)+offset, length)
	}
	if sf, ok := s.small.(blob.SubFetcher); ok {
		rc, err := sf.SubFetch(ctx, ref, offset, length)
		if err != blob.ErrUnimplemented {
			return rc, err
		}
	}
	rc, size, err := s.small.Fetch(ctx, ref)
	if err != nil {
		return rc, err
	}
	length, err = capOffsetLength(size, offset, length)
	if err != nil {
		rc.Close()
		return nil, err
	}
	if offset != 0 {
		if _, err = io.CopyN(io.Discard, rc, offset); err != nil {
			rc.Close()
			return nil, err
		}
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(rc, length), rc}, nil
}

func capOffsetLength(size uint32, offset, length int64) (newLength int64, err error) {
	if offset < 0 || length < 0 {
		return 0, blob.ErrNegativeSubFetch
	}
	if offset > int64(size) {
		return 0, blob.ErrOutOfRangeOffsetSubFetch
	}
	if over := (offset + length) - int64(size); over > 0 {
		length -= over
	}
	return length, nil
}
