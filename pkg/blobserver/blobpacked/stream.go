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
	"strings"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/types"
)

// StreamBlobs impl.

// Continuation token is:
// "s*" if we're in the small blobs, (or "" to start):
//   "s:pt:<underlying token>" (pass through)
//   "s:after:<last-blobref-set>" (blob ref of already-sent item)
// "l*" if we're in the large blobs:
//   "l:<big-blobref,lexically>:<offset>" (of blob data from beginning of zip)
//   TODO: also care about whether large supports blob streamer?
// First it streams from small (if available, else enumerates)
// Then it streams from large (if available, else enumerates),
// and for each large, streams the contents of the zips.
func (s *storage) StreamBlobs(ctx *context.Context, dest chan<- *blob.Blob, contToken string, limitBytes int64) (nextContinueToken string, err error) {
	defer close(dest)
	switch {
	case contToken == "" || strings.HasPrefix(contToken, "s:"):
		return s.streamSmallBlobs(ctx, dest, strings.TrimPrefix(contToken, "s:"), limitBytes)
	case strings.HasPrefix(contToken, "l:"):
		return s.streamLargeBlobs(ctx, dest, strings.TrimPrefix(contToken, "l:"), limitBytes)
	default:
		return "", errors.New("invalid continue token")
	}
}

func (s *storage) streamSmallBlobs(ctx *context.Context, dest chan<- *blob.Blob, contToken string, limitBytes int64) (nextContinueToken string, err error) {
	smallStream, ok := s.small.(blobserver.BlobStreamer)
	if ok {
		if contToken != "" || !strings.HasPrefix(contToken, "pt:") {
			return "", errors.New("invalid pass-through stream token")
		}
		next, err := smallStream.StreamBlobs(ctx, dest, strings.TrimPrefix(contToken, "pt"), limitBytes)
		if err == nil || next == "" {
			next = "l:" // now do large
		}
		return next, err
	}
	if contToken != "" || !strings.HasPrefix(contToken, "after:") {
		return "", errors.New("invalid continue token")
	}
	enumCtx := ctx.New()
	enumDone := enumCtx.Done()
	defer enumCtx.Cancel()
	sbc := make(chan blob.SizedRef, 100)
	enumErrc := make(chan error, 1)
	go func() {
		enumErrc <- blobserver.EnumerateAllFrom(enumCtx, s.small, strings.TrimPrefix(contToken, "after:"), func(sb blob.SizedRef) error {
			select {
			case sbc <- sb:
				return nil
			case <-enumDone:
				return context.ErrCanceled
			}
		})
	}()
	var sent int64
	var enumErr error
	var sawEnumEnd bool
	var lastRef blob.Ref
	for sent < limitBytes {
		select {
		case sb := <-sbc:
			opener := func() types.ReadSeekCloser {
				return blob.NewLazyReadSeekCloser(s.small, sb.Ref)
			}
			select {
			case dest <- blob.NewBlob(sb.Ref, sb.Size, opener):
				lastRef = sb.Ref
				sent += int64(sb.Size)
			case <-ctx.Done():
				return "", context.ErrCanceled
			}
		case err := <-enumErrc:
			sawEnumEnd = true
			enumErr = err
		}
	}
	// See if we didn't send anything due to enumeration errors.
	if sent == 0 {
		enumCtx.Cancel()
		if !sawEnumEnd {
			enumErr = <-enumErrc
		}
		return "", enumErr
	}
	return "s:after:" + lastRef.String(), nil
}

func (s *storage) streamLargeBlobs(ctx *context.Context, dest chan<- *blob.Blob, contToken string, limitBytes int64) (nextContinueToken string, err error) {
	panic("TODO")
}
