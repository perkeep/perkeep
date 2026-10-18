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

package schema

import (
	"context"
	"io"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

// WriteFileMapGrouped writes a file's blobs to dst in groups of up to
// groupBlobs blobs, or of about groupBytes bytes.
const (
	groupBlobs = 256
	groupBytes = 16 << 20
)

// WriteFileMapGrouped is like WriteFileMap, but writes the blobs to dst in
// groups, with blobserver.ReceiveBlobsNoHash, so that storage can share
// the cost of making them durable (such as syncs to disk) among a group's
// blobs, rather than paying it for each chunk. It asks dst which of each
// group's blobs it has, and writes only the others.
//
// It's for writing files to storage within the server. While it writes
// one group, it cuts the next, so it holds the contents of up to two
// groups (about 32 MB) in memory.
//
// The file schema blob is written last, once the chunks and bytes schema
// blobs it refers to are durable.
func WriteFileMapGrouped(ctx context.Context, dst blobserver.StatReceiver, file *Builder, r io.Reader) (blob.Ref, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	groups := make(chan []*blob.Blob) // unbuffered: one group written, one cut
	written := make(chan error, 1)
	go func() {
		var err error
		for group := range groups {
			if err == nil {
				err = writeGroup(ctx, dst, group)
				if err != nil {
					cancel() // stop cutting
				}
			}
		}
		written <- err
	}()

	var group []*blob.Blob
	var size int
	send := func() error {
		select {
		case groups <- group:
		case <-ctx.Done():
			return ctx.Err()
		}
		group, size = nil, 0
		return nil
	}
	add := func(br blob.Ref, data []byte) error {
		group = append(group, blob.NewBlob(br, uint32(len(data)), func(context.Context) ([]byte, error) {
			return data, nil
		}))
		size += len(data)
		if len(group) >= groupBlobs || size >= groupBytes {
			return send()
		}
		return nil
	}

	seen := make(map[blob.Ref]bool) // chunks repeated within the file
	fc, err := CutFile(r, func(br blob.Ref, data []byte) error {
		if seen[br] {
			return nil
		}
		seen[br] = true
		return add(br, data)
	})
	if err == nil {
		// The bytes schema blobs go with the last chunks. One written
		// without all the chunks it refers to is harmless, unlike a file
		// schema blob: nothing refers to it.
		var bytesBlobs []*Blob
		bytesBlobs, err = fc.Populate(file)
		for _, b := range bytesBlobs {
			if err == nil {
				err = add(b.BlobRef(), []byte(b.JSON()))
			}
		}
	}
	if err == nil && len(group) > 0 {
		err = send()
	}
	close(groups)
	if werr := <-written; werr != nil {
		return blob.Ref{}, werr
	}
	if err != nil {
		return blob.Ref{}, err
	}

	json := file.Blob().JSON()
	br := blob.RefFromString(json)
	if err := writeGroup(ctx, dst, []*blob.Blob{blob.NewBlob(br, uint32(len(json)), func(context.Context) ([]byte, error) {
		return []byte(json), nil
	})}); err != nil {
		return blob.Ref{}, err
	}
	return br, nil
}

// writeGroup writes the blobs of group that dst doesn't have to it.
func writeGroup(ctx context.Context, dst blobserver.StatReceiver, group []*blob.Blob) error {
	refs := make([]blob.Ref, len(group))
	for i, b := range group {
		refs[i] = b.Ref()
	}
	have, err := blobserver.StatBlobs(ctx, dst, refs)
	if err != nil {
		return err
	}
	missing := group[:0:0]
	for _, b := range group {
		if _, ok := have[b.Ref()]; !ok {
			missing = append(missing, b)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return blobserver.ReceiveBlobsNoHash(ctx, dst, missing)
}
