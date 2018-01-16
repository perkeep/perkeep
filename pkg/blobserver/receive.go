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
	if err == io.EOF && !c.br.HashMatches(c.h) {
		err = ErrCorruptBlob
		c.corrupt = true
	}
	return
}
