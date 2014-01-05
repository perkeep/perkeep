/*
Copyright 2013 Google Inc.

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
	"hash"
	"io"
	"strings"

	"camlistore.org/pkg/blob"
)

// ReceiveString uploads the blob given by the string s to dst
// and returns its blobref and size.
func ReceiveString(dst BlobReceiver, s string) (blob.SizedRef, error) {
	return Receive(dst, blob.RefFromString(s), strings.NewReader(s))
}

// Receive wraps calling a BlobReceiver's ReceiveBlob method,
// additionally providing verification of the src digest, and also
// notifying the blob hub on success.
// The error will be ErrCorruptBlob if the blobref didn't match.
func Receive(dst BlobReceiver, br blob.Ref, src io.Reader) (blob.SizedRef, error) {
	return receive(dst, br, src, true)
}

func ReceiveNoHash(dst BlobReceiver, br blob.Ref, src io.Reader) (blob.SizedRef, error) {
	return receive(dst, br, src, false)
}

func receive(dst BlobReceiver, br blob.Ref, src io.Reader, checkHash bool) (sb blob.SizedRef, err error) {
	src = io.LimitReader(src, MaxBlobSize)
	if checkHash {
		src = &checkHashReader{br.Hash(), br, src, false}
	}
	sb, err = dst.ReceiveBlob(br, src)
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
