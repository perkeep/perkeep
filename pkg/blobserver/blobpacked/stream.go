/*
Copyright 2014 The Camlistore AUTHORS

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
	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/types"
)

// StreamBlobs impl.

func (s *storage) StreamBlobs(ctx *context.Context, dest chan<- blobserver.BlobAndToken, contToken string) (err error) {
	return blobserver.NewMultiBlobStreamer(
		smallBlobStreamer{s},
		largeBlobStreamer{s},
	).StreamBlobs(ctx, dest, contToken)
}

type smallBlobStreamer struct{ sto *storage }
type largeBlobStreamer struct{ sto *storage }

// stream the loose blobs
func (st smallBlobStreamer) StreamBlobs(ctx *context.Context, dest chan<- blobserver.BlobAndToken, contToken string) (err error) {
	small := st.sto.small
	if bs, ok := small.(blobserver.BlobStreamer); ok {
		return bs.StreamBlobs(ctx, dest, contToken)
	}
	defer close(dest)
	donec := ctx.Done()
	return blobserver.EnumerateAllFrom(ctx, small, contToken, func(sb blob.SizedRef) error {
		select {
		case dest <- blobserver.BlobAndToken{
			Blob: blob.NewBlob(sb.Ref, sb.Size, func() types.ReadSeekCloser {
				return blob.NewLazyReadSeekCloser(small, sb.Ref)
			}),
			Token: sb.Ref.StringMinusOne(), // streamer is >=, enumerate is >
		}:
			return nil
		case <-donec:
			return context.ErrCanceled
		}
	})
}

func (st largeBlobStreamer) StreamBlobs(ctx *context.Context, dest chan<- blobserver.BlobAndToken, contToken string) (err error) {
	defer close(dest)
	// TODO(bradfitz): implement
	return nil
}
