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

var ErrCorruptBlob = os.NewError("corrupt blob; digest doesn't match")

type BlobReceiver interface {
	// ReceiveBlob accepts a newly uploaded blob and writes it to
	// disk.
	ReceiveBlob(blob *blobref.BlobRef, source io.Reader) (blobref.SizedBlobRef, os.Error)
}

type BlobStatter interface {
	// Stat checks for the existence of blobs, writing their sizes
	// (if found back to the dest channel), and returning an error
	// or nil.  Stat() should NOT close the channel.
	// waitSeconds is the max time to wait for the blobs to exist,
	// or 0 for no delay.
	StatBlobs(dest chan<- blobref.SizedBlobRef,
		blobs []*blobref.BlobRef,
		waitSeconds int) os.Error
}

type StatReceiver interface {
	BlobReceiver
	BlobStatter
}

// QueueCreator is implemented by Storage interfaces which support
// creating queues in which all new uploads go to both the root
// storage as well as the named queue, which is then returned.  This
// is used by replication.
type QueueCreator interface {
	CreateQueue(name string) (Storage, os.Error)
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
	// waitSeconds is the max time to wait for any blobs to exist,
	// or 0 for no delay.
	// EnumerateBlobs must close the channel.  (even if limit
	// was hit and more blobs remain)
	//
	// after and waitSeconds can't be used together. One must be
	// its zero value.
	EnumerateBlobs(dest chan<- blobref.SizedBlobRef,
		after string,
		limit uint,
		waitSeconds int) os.Error
}

// Cache is the minimal interface expected of a blob cache.
type Cache interface {
	blobref.SeekFetcher
	BlobReceiver
	BlobStatter
}

type BlobReceiveConfiger interface {
	BlobReceiver
	Configer
}

type Config struct {
	Writable, Readable bool
	IsQueue            bool // supports deletes

	// the "http://host:port" and optional path (but without trailing slash) to have "/camli/*" appended
	URLBase string
}

type Configer interface {
	Config() *Config
}

type Storage interface {
	blobref.StreamingFetcher
	BlobReceiver
	BlobStatter
	BlobEnumerator

	// Remove 0 or more blobs.  Removal of non-existent items
	// isn't an error.  Returns failure if any items existed but
	// failed to be deleted.
	RemoveBlobs(blobs []*blobref.BlobRef) os.Error

	// Returns the blob notification bus
	GetBlobHub() BlobHub
}

type StorageConfiger interface {
	Storage
	Configer
}
