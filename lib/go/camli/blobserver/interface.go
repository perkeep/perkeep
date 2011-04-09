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

var CorruptBlobError = os.NewError("corrupt blob; digest doesn't match")

type NamedPartition interface {
	Name() string  // "" for default, "queue-indexer", etc
}

type Partition interface {
	NamedPartition

	Writable() bool  // accepts direct uploads (excluding mirroring from default partition)
	Readable() bool  // can return blobs (e.g. indexer partition can't)
	IsQueue() bool   // is a temporary queue partition (supports deletes)

	// TODO: rename this.  just "UploadMirrors"?
	GetMirrorPartitions() []Partition

	// the "http://host:port" and optional path (but without trailing slash) to have "/camli/*" appended
	URLBase() string
}

type BlobReceiver interface {
	// ReceiveBlob accepts a newly uploaded blob and writes it to
	// disk.
	//
	// mirrorPartitions may not be supported by all instances
	// and may return an error if used.
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

type MaxEnumerateConfig interface {
	// Returns the max that this storage interface is capable
	// of enumerating at once.
	MaxEnumerate() uint
}

type BlobEnumerator interface {
	// EnumerateBobs sends at most limit SizedBlobRef into dest,
	// sorted, as long as they are lexigraphically greater than
	// after (if provided).
	// limit will be supplied and sanity checked by caller.
	// waitSeconds is the max time to wait for any blobs to exist
	// in the given partition, or 0 for no delay.
	// EnumerateBlobs must close the channel.  (even if limit
	// was hit and more blobs remain)
	EnumerateBlobs(dest chan *blobref.SizedBlobRef,
		partition Partition,
		after string,
		limit uint,
		waitSeconds int) os.Error
}

// Cache is the minimal interface expected of a blob cache.
type Cache interface {
	blobref.Fetcher
	BlobReceiver
	BlobStatter
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
	// Use nil for the default partition.
	// TODO: move this to be a method on the Partition interface?
	GetBlobHub(partition Partition) BlobHub
}
