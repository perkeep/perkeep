/*
Copyright 2014 The Perkeep Authors

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

package main

import (
	"context"
	"io"
	"io/ioutil"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

type discardStorage struct {
	blobserver.NoImplStorage
}

func (discardStorage) ReceiveBlob(ctx context.Context, br blob.Ref, r io.Reader) (sb blob.SizedRef, err error) {
	n, err := io.Copy(ioutil.Discard, r)
	return blob.SizedRef{br, uint32(n)}, err
}

func (discardStorage) StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error {
	return nil
}

func (discardStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	return nil
}

func (discardStorage) RemoveBlobs(ctx context.Context, blobs []blob.Ref) error {
	return nil
}
