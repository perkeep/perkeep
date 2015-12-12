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
	"errors"
	"fmt"
	"strconv"
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/types"
	"golang.org/x/net/context"
)

// StreamBlobs impl.

func (s *storage) StreamBlobs(ctx context.Context, dest chan<- blobserver.BlobAndToken, contToken string) (err error) {
	return blobserver.NewMultiBlobStreamer(
		smallBlobStreamer{s},
		largeBlobStreamer{s},
	).StreamBlobs(ctx, dest, contToken)
}

type smallBlobStreamer struct{ sto *storage }
type largeBlobStreamer struct{ sto *storage }

// stream the loose blobs
func (st smallBlobStreamer) StreamBlobs(ctx context.Context, dest chan<- blobserver.BlobAndToken, contToken string) (err error) {
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
			return ctx.Err()
		}
	})
}

var errContToken = errors.New("blobpacked: bad continuation token")

// contToken is of forms:
//    ""                : start from beginning of zip files
//    "sha1-xxxxx:n"    : start at == (sha1-xxxx, file n), else next zip
func (st largeBlobStreamer) StreamBlobs(ctx context.Context, dest chan<- blobserver.BlobAndToken, contToken string) (err error) {
	defer close(dest)
	s := st.sto
	large := s.large

	var after string // for enumerateAll
	var skipFiles int
	var firstRef blob.Ref // first we care about

	if contToken != "" {
		f := strings.SplitN(contToken, ":", 2)
		if len(f) != 2 {
			return errContToken
		}
		firstRef, _ = blob.Parse(f[0])
		skipFiles, err = strconv.Atoi(f[1])
		if !firstRef.Valid() || err != nil {
			return errContToken
		}
		// EnumerateAllFrom takes a cursor that's greater, but
		// we want to start _at_ firstRef. So start
		// enumerating right before our target.
		after = firstRef.StringMinusOne()
	}
	return blobserver.EnumerateAllFrom(ctx, large, after, func(sb blob.SizedRef) error {
		if firstRef.Valid() {
			if sb.Ref.Less(firstRef) {
				// Skip.
				return nil
			}
			if firstRef.Less(sb.Ref) {
				skipFiles = 0 // reset it.
			}
		}
		fileN := 0
		return s.foreachZipBlob(sb.Ref, func(bap BlobAndPos) error {
			if skipFiles > 0 {
				skipFiles--
				fileN++
				return nil
			}
			select {
			case dest <- blobserver.BlobAndToken{
				Blob: blob.NewBlob(bap.Ref, bap.Size, func() types.ReadSeekCloser {
					return blob.NewLazyReadSeekCloser(s, bap.Ref)
				}),
				Token: fmt.Sprintf("%s:%d", sb.Ref, fileN),
			}:
				fileN++
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	})
}
