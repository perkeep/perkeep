/*
Copyright 2011 Google Inc.

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
	"camli/blobref"
	"io"
	"os"
)

type Partition string

const DefaultPartition = Partition("")

func (p Partition) IsDefault() bool {
	return len(p) == 0
}

type BlobReceiver interface {
	ReceiveBlob(blob *blobref.BlobRef, source io.Reader, mirrorPartions []Partition) (*blobref.SizedBlobRef, os.Error)
}

type BlobStatter interface {
	// Stat checks for the existence of blobs, writing their sizes
	// (if found back to the dest channel), and returning an error
	// or nil.  Stat() should NOT close the channel.
	// waitSeconds is the max time to wait for the blobs to exist
	// in the given partition, or 0 for no delay.
	Stat(dest chan *blobref.SizedBlobRef,
		partition Partition,
		blobs []*blobref.BlobRef,
		waitSeconds int) os.Error
}

type BlobEnumerator interface {
	// EnumerateBobs sends at most limit SizedBlobRef into dest,
	// sorted, as long as they are lexigraphically greater than
	// after (if provided).
	EnumerateBlobs(dest chan *blobref.SizedBlobRef,
		partition Partition,
		after string,
		limit uint,
		waitSeconds int) os.Error
}

type Storage interface {
	blobref.Fetcher
	BlobReceiver
	BlobStatter
	BlobEnumerator

	// Remove 0 or more blobs from provided partition, which
	// should be empty for the default partition.  Removal of
	// non-existent items isn't an error.  Returns failure if any
	// items existed but failed to be deleted.
	Remove(partition Partition, blobs []*blobref.BlobRef) os.Error

	// Returns the blob notification bus for a given partition.
	GetBlobHub(partition Partition) BlobHub
}
