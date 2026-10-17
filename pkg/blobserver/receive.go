/*
Copyright 2013 The Perkeep Authors

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

package blobserver

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"sync"

	"perkeep.org/pkg/blob"
)

// ErrReadonly is the error value returned by read-only blobservers.
var ErrReadonly = errors.New("this blobserver is read only")

// ReceiveString uploads the blob given by the string s to dst
// and returns its blobref and size.
func ReceiveString(ctx context.Context, dst BlobReceiver, s string) (blob.SizedRef, error) {
	return Receive(ctx, dst, blob.RefFromString(s), strings.NewReader(s))
}

// Receive wraps calling a BlobReceiver's ReceiveBlob method,
// additionally providing verification of the src digest, and also
// notifying the blob hub on success.
// The error will be ErrCorruptBlob if the blobref didn't match.
func Receive(ctx context.Context, dst BlobReceiver, br blob.Ref, src io.Reader) (blob.SizedRef, error) {
	return receive(ctx, dst, br, src, true)
}

func ReceiveNoHash(ctx context.Context, dst BlobReceiver, br blob.Ref, src io.Reader) (blob.SizedRef, error) {
	return receive(ctx, dst, br, src, false)
}

func receive(ctx context.Context, dst BlobReceiver, br blob.Ref, src io.Reader, checkHash bool) (sb blob.SizedRef, err error) {
	src = io.LimitReader(src, MaxBlobSize)
	if checkHash {
		h := br.Hash()
		if h == nil {
			return sb, fmt.Errorf("invalid blob type %v; no registered hash function", br)
		}
		src = &checkHashReader{h, br, src, false}
	}
	sb, err = dst.ReceiveBlob(ctx, br, src)
	if err != nil {
		if checkHash && src.(*checkHashReader).corrupt {
			err = ErrCorruptBlob
		}
		return
	}
	err = GetHub(dst).NotifyBlobReceived(sb)
	return
}

// receiveBlobsConcurrency is how many concurrent ReceiveBlob calls
// ReceiveBlobs and ReceiveBlobsUnchecked make for a destination that
// isn't a BlobsReceiver.
const receiveBlobsConcurrency = 8

// ReceiveBlobs receives blobs into dst, returning an error as described by
// BlobsReceiver: nil if every blob was received, a *BlobsError listing the
// blobs that failed, or another error if the whole call failed.
//
// It checks each blob's contents against its ref first: a blob that
// doesn't match gets ErrCorruptBlob, and isn't passed to dst. Blobs with
// the same ref are received once. It then uses dst.ReceiveBlobs if dst is
// a BlobsReceiver, or else up to a few concurrent ReceiveBlob calls, and
// notifies dst's blob hub of each received blob. If dst's call fails as a
// whole, so does ReceiveBlobs, with the same error, unless some blobs
// already failed their checks.
func ReceiveBlobs(ctx context.Context, dst BlobReceiver, blobs []*blob.Blob) error {
	be := &BlobsError{Op: "receive"}
	seen := make(map[blob.Ref]bool, len(blobs))
	valid := make([]*blob.Blob, 0, len(blobs))
	for _, b := range blobs {
		br := b.Ref()
		if seen[br] {
			continue
		}
		seen[br] = true
		if err := checkBlob(ctx, b); err != nil {
			be.Add(br, err)
			continue
		}
		valid = append(valid, b)
	}
	if len(valid) == 0 {
		return be.OrNil()
	}

	refs := make([]blob.Ref, len(valid))
	for i, b := range valid {
		refs[i] = b.Ref()
	}
	err := ReceiveBlobsUnchecked(ctx, dst, valid)
	if _, ok := errors.AsType[*BlobsError](err); err != nil && !ok && be.Len() == 0 {
		return err // the call failed as a whole
	}
	be.AddResult(refs, err)

	hub := GetHub(dst)
	for _, b := range valid {
		if be.Err(b.Ref()) == nil {
			be.Add(b.Ref(), hub.NotifyBlobReceived(b.SizedRef()))
		}
	}
	return be.OrNil()
}

// checkBlob returns an error if b is too big, can't be read, or doesn't
// match its ref (ErrCorruptBlob).
func checkBlob(ctx context.Context, b *blob.Blob) error {
	if b.Size() > MaxBlobSize {
		return fmt.Errorf("blob %v is %d bytes, over the maximum of %d", b.Ref(), b.Size(), MaxBlobSize)
	}
	h := b.Ref().Hash()
	if h == nil {
		return fmt.Errorf("invalid blob type %v; no registered hash function", b.Ref())
	}
	r, err := b.ReadAll(ctx)
	if err != nil {
		return err
	}
	r.WriteTo(h) // a bytes.Reader writing to a hash can't fail
	if !b.Ref().HashMatches(h) {
		return ErrCorruptBlob
	}
	return nil
}

// ReceiveBlobsUnchecked passes blobs to dst, with dst.ReceiveBlobs if dst
// is a BlobsReceiver, or else up to a few concurrent ReceiveBlob calls,
// returning an error as described by BlobsReceiver. Unlike ReceiveBlobs,
// it doesn't check the blobs' contents or merge duplicate refs, and
// doesn't notify dst's blob hub. It's for a BlobsReceiver passing the
// blobs it was given on to storage it wraps; others should use
// ReceiveBlobs.
func ReceiveBlobsUnchecked(ctx context.Context, dst BlobReceiver, blobs []*blob.Blob) error {
	if bsr, ok := dst.(BlobsReceiver); ok {
		return bsr.ReceiveBlobs(ctx, blobs)
	}
	be := &BlobsError{Op: "receive"}
	receiveEach(ctx, dst, blobs, be)
	return be.OrNil()
}

// receiveEach receives blobs into dst with up to receiveBlobsConcurrency
// concurrent ReceiveBlob calls, recording failures in be. Blobs not yet
// started when ctx is done fail with ctx's error.
func receiveEach(ctx context.Context, dst BlobReceiver, blobs []*blob.Blob, be *BlobsError) {
	errs := make([]error, len(blobs))
	receive := func(i int) {
		if err := ctx.Err(); err != nil {
			errs[i] = err
			return
		}
		b := blobs[i]
		r, err := b.ReadAll(ctx)
		if err == nil {
			_, err = dst.ReceiveBlob(ctx, b.Ref(), r)
		}
		errs[i] = err
	}
	workers := min(len(blobs), receiveBlobsConcurrency)
	if workers == 1 {
		for i := range blobs {
			receive(i)
		}
	} else {
		next := make(chan int)
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				for i := range next {
					receive(i)
				}
			})
		}
		for i := range blobs {
			next <- i
		}
		close(next)
		wg.Wait()
	}
	for i, err := range errs {
		be.Add(blobs[i].Ref(), err)
	}
}

// checkHashReader is an io.Reader that wraps the src Reader but turns
// an io.EOF into an ErrCorruptBlob if the data read doesn't match the
// hash of br.
type checkHashReader struct {
	h       hash.Hash
	br      blob.Ref
	src     io.Reader
	corrupt bool
}

func (c *checkHashReader) Read(p []byte) (n int, err error) {
	n, err = c.src.Read(p)
	c.h.Write(p[:n])
	if errors.Is(err, io.EOF) && !c.br.HashMatches(c.h) {
		err = ErrCorruptBlob
		c.corrupt = true
	}
	return
}
