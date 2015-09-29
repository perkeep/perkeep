/*
Copyright 2011 The Camlistore Authors

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

package test

import (
	"io"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/memory"
)

// Fetcher is an in-memory implementation of the blobserver Storage
// interface.  It started as just a fetcher and grew. It also includes
// other convenience methods for testing.
type Fetcher struct {
	memory.Storage

	// ReceiveErr optionally returns the error to return on receive.
	ReceiveErr error

	// FetchErr, if non-nil, specifies the error to return on the next fetch call.
	// If it returns nil, fetches proceed as normal.
	FetchErr func() error
}

var (
	_ blobserver.Storage      = (*Fetcher)(nil)
	_ blobserver.BlobStreamer = (*Fetcher)(nil)
)

func (tf *Fetcher) Fetch(ref blob.Ref) (file io.ReadCloser, size uint32, err error) {
	if tf.FetchErr != nil {
		if err = tf.FetchErr(); err != nil {
			return
		}
	}
	file, size, err = tf.Storage.Fetch(ref)
	if err != nil {
		return
	}
	return file, size, nil
}

func (tf *Fetcher) SubFetch(ref blob.Ref, offset, length int64) (io.ReadCloser, error) {
	if tf.FetchErr != nil {
		if err := tf.FetchErr(); err != nil {
			return nil, err
		}
	}
	rc, err := tf.Storage.SubFetch(ref, offset, length)
	if err != nil {
		return rc, err
	}
	return rc, nil
}

func (tf *Fetcher) ReceiveBlob(br blob.Ref, source io.Reader) (blob.SizedRef, error) {
	sb, err := tf.Storage.ReceiveBlob(br, source)
	if err != nil {
		return sb, err
	}
	if err := tf.ReceiveErr; err != nil {
		tf.RemoveBlobs([]blob.Ref{br})
		return sb, err
	}
	return sb, nil
}

func (tf *Fetcher) AddBlob(b *Blob) {
	_, err := tf.ReceiveBlob(b.BlobRef(), b.Reader())
	if err != nil {
		panic(err)
	}
}
