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
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"camlistore.org/pkg/blobref"
)

var ErrCorruptBlob = errors.New("corrupt blob; digest doesn't match")

type BlobReceiver interface {
	// ReceiveBlob accepts a newly uploaded blob and writes it to
	// disk.
	ReceiveBlob(blob *blobref.BlobRef, source io.Reader) (blobref.SizedBlobRef, error)
}

type BlobStatter interface {
	// Stat checks for the existence of blobs, writing their sizes
	// (if found back to the dest channel), and returning an error
	// or nil.  Stat() should NOT close the channel.
	// wait is the max time to wait for the blobs to exist,
	// or 0 for no delay.
	StatBlobs(dest chan<- blobref.SizedBlobRef,
		blobs []*blobref.BlobRef,
		wait time.Duration) error
}

func StatBlob(bs BlobStatter, br *blobref.BlobRef) (sb blobref.SizedBlobRef, err error) {
	c := make(chan blobref.SizedBlobRef, 1)
	err = bs.StatBlobs(c, []*blobref.BlobRef{br}, 0)
	if err != nil {
		return
	}
	select {
	case sb = <-c:
	default:
		err = os.ErrNotExist
	}
	return
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
	CreateQueue(name string) (Storage, error)
}

type MaxEnumerateConfig interface {
	// Returns the max that this storage interface is capable
	// of enumerating at once.
	MaxEnumerate() int
}

type BlobEnumerator interface {
	// EnumerateBobs sends at most limit SizedBlobRef into dest,
	// sorted, as long as they are lexigraphically greater than
	// after (if provided).
	// limit will be supplied and sanity checked by caller.
	// wait is the max time to wait for any blobs to exist,
	// or 0 for no delay.
	// EnumerateBlobs must close the channel.  (even if limit
	// was hit and more blobs remain)
	//
	// after and waitSeconds can't be used together. One must be
	// its zero value.
	EnumerateBlobs(dest chan<- blobref.SizedBlobRef,
		after string,
		limit int,
		wait time.Duration) error
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
	CanLongPoll        bool

	// the "http://host:port" and optional path (but without trailing slash) to have "/camli/*" appended
	URLBase string
}

type Configer interface {
	Config() *Config
}

// A GenerationNotSupportedError explains why a Storage
// value implemented the Generationer interface but failed due
// to a wrapped Storage value not implementing the interface.
type GenerationNotSupportedError string

func (s GenerationNotSupportedError) Error() string { return string(s) }

/* 
The optional Generationer interface is an optimization and paranoia
facility for clients which can be implemented by Storage
implementations.

If the client sees the same random string in multiple upload sessions,
it assumes that the blobserver still has all the same blobs, and also
it's the same server.  This mechanism is not fundamental to
Camlistore's operation: the client could also check each blob before
uploading, or enumerate all blobs from the server too.  This is purely
an optimization so clients can mix this value into their "is this file
uploaded?" local cache keys.
*/
type Generationer interface {
	// Generation returns a Storage's initialization time and
	// and unique random string (or UUID).  Implementations
	// should call ResetStorageGeneration on demand if no
	// information is known.
	// The error will be of type GenerationNotSupportedError if an underlying
	// storage target doesn't support the Generationer interface.
	StorageGeneration() (initTime time.Time, random string, err error)

	// ResetGeneration deletes the information returned by Generation
	// and re-generates it.
	ResetStorageGeneration() error
}

type Storage interface {
	blobref.StreamingFetcher
	BlobReceiver
	BlobStatter
	BlobEnumerator

	// Remove 0 or more blobs.  Removal of non-existent items
	// isn't an error.  Returns failure if any items existed but
	// failed to be deleted.
	RemoveBlobs(blobs []*blobref.BlobRef) error

	// Returns the blob notification bus
	GetBlobHub() BlobHub
}

type StorageConfiger interface {
	Storage
	Configer
}

type StorageQueueCreator interface {
	Storage
	QueueCreator
}

// ContextWrapper is an optional interface for App Engine.
//
// While Camlistore's internals are separated out into a part which
// maps http requests to the interfaces in this file
// (pkg/blobserver/handlers) and parts which map these
// interfaces to implementations (localdisk, s3, etc), the App Engine
// implementation requires access to the original HTTP
// request. (because a security token is stored on the incoming HTTP
// request in a magic header).  All the handlers will do an interface
// check on this type and use the resulting Storage instead.
type ContextWrapper interface {
	WrapContext(*http.Request) Storage
}

func MaybeWrapContext(sto Storage, req *http.Request) Storage {
	if req == nil {
		return sto
	}
	w, ok := sto.(ContextWrapper)
	if !ok {
		return sto
	}
	return w.WrapContext(req)
}

// Unwrap returns the wrapped Storage interface, if wrapped, else returns sto.
func Unwrap(sto interface{}) interface{} {
	type get interface {
		GetStorage() Storage
	}
	if g, ok := sto.(get); ok {
		return Unwrap(g.GetStorage())
	}
	return sto
}