/*
Copyright 2011 The Perkeep Authors

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
	"io"
	"net/http"
	"time"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/constants"
)

// MaxBlobSize is the size of a single blob in Perkeep.
const MaxBlobSize = constants.MaxBlobSize

// ErrCorruptBlob is the error for a blob whose contents don't match its
// ref.
//
// For an operation a storage doesn't support, such as an optional
// interface's method that a wrapper implements but the storage it wraps
// doesn't, storage returns an error matching errors.ErrUnsupported, with
// context: fmt.Errorf("RemoveBlobs: %w", errors.ErrUnsupported).
var ErrCorruptBlob = errors.New("corrupt blob; digest doesn't match")

// BlobReceiver is the interface for receiving blobs.
type BlobReceiver interface {
	// ReceiveBlob accepts a newly uploaded blob and writes it to
	// permanent storage.
	//
	// Implementations of BlobReceiver downstream of the HTTP
	// server can trust that the source isn't larger than
	// MaxBlobSize and that its digest matches the provided blob
	// ref. (If not, the read of the source will fail before EOF)
	//
	// To ensure those guarantees, callers of ReceiveBlob should
	// not call ReceiveBlob directly but instead use either
	// blobserver.Receive or blobserver.ReceiveString, which also
	// take care of notifying the BlobReceiver's "BlobHub"
	// notification bus for observers.
	ReceiveBlob(ctx context.Context, br blob.Ref, source io.Reader) (blob.SizedRef, error)
}

// BlobsReceiver is an optional interface for a BlobReceiver that can
// receive several blobs together more cheaply than one at a time, such as
// by writing them with one sync to disk. Callers should use
// blobserver.ReceiveBlobs, not call ReceiveBlobs directly.
type BlobsReceiver interface {
	BlobReceiver

	// ReceiveBlobs writes blobs to permanent storage. It returns nil if
	// every blob was received, to the same standard as a successful
	// ReceiveBlob. If it returns a *BlobsError (or an error wrapping
	// one), the blobs it lists failed and the others were received. Any
	// other error means the call failed as a whole, and no blob can be
	// assumed received. A canceled ctx may leave some blobs received.
	//
	// The blobs are independent, not a transaction: some may be received
	// while others fail, and they may be received in any order. A blob
	// may also be received before ReceiveBlobs returns, but no blob may
	// be visible to StatBlobs, EnumerateBlobs, or Fetch before it's
	// durable.
	//
	// Work that fetches other blobs because of a received one (such as
	// packing a file, or indexing a schema blob's dependencies) must not
	// need a blob from the same call before that blob is durable.
	//
	// As with ReceiveBlob, implementations can trust that each blob's
	// contents match its ref and are at most MaxBlobSize, and blobs has
	// no duplicate refs. Implementations may split blobs into smaller
	// groups. They may keep reading the blobs after returning (as replica
	// does, for slower replicas), but never modify them. Receiving one
	// blob must be equivalent to ReceiveBlob.
	ReceiveBlobs(ctx context.Context, blobs []*blob.Blob) error
}

// BlobStatter is the interface for checking the size and existence of blobs.
type BlobStatter interface {
	// Stat checks for the existence of blobs, calling fn in
	// serial for each found blob, in any order, but with no
	// duplicates. The blobs slice should not have duplicates.
	//
	// If fn returns an error, StatBlobs returns with that value
	// and makes no further calls to fn.
	//
	// StatBlobs does not return an error on missing blobs, only
	// on failure to stat blobs.
	StatBlobs(ctx context.Context, blobs []blob.Ref, fn func(blob.SizedRef) error) error
}

type StatReceiver interface {
	BlobReceiver
	BlobStatter
}

type BlobEnumerator interface {
	// EnumerateBobs sends at most limit SizedBlobRef into dest,
	// sorted, as long as they are lexigraphically greater than
	// after (if provided).
	// limit will be supplied and sanity checked by caller.
	// EnumerateBlobs must close the channel.  (even if limit
	// was hit and more blobs remain, or an error is returned, or
	// the ctx is canceled)
	EnumerateBlobs(ctx context.Context,
		dest chan<- blob.SizedRef,
		after string,
		limit int) error

	// TODO: remove limit from this interface, since the caller
	// can cancel? see if that would simplify implementations and
	// callers.
}

// RefLister is an optional interface for a BlobEnumerator that can
// list all of its blobs faster than EnumerateBlobs when the caller
// needs neither their sizes nor sorted order. See ListAllRefs.
type RefLister interface {
	// ListBlobRefs calls fn once for each blob, in no particular
	// order. Calls to fn are not concurrent. If fn returns an
	// error, ListBlobRefs stops and returns that error.
	ListBlobRefs(ctx context.Context, fn func(blob.Ref) error) error
}

// BlobAndToken is the value used by the BlobStreamer interface,
// containing both a Blob and a continuation token.
type BlobAndToken struct {
	*blob.Blob
	// Token is the continuation token to resume streaming
	// starting at this blob in the future.
	Token string
}

type BlobStreamer interface {
	// BlobStream is an optional interface that may be implemented by
	// Storage implementations.
	//
	// StreamBlobs sends blobs to dest in an unspecified order. It is
	// expected that a Storage implementation implementing
	// BlobStreamer will send blobs to dest in the most efficient
	// order possible.
	//
	// The provided continuation token resumes the stream at a
	// point. To start from the beginning, send the empty string.
	// The token is opaque and must never be interpreted; its
	// format may change between versions of the server.
	//
	// If the content is canceled, the error value is
	// context.Canceled.
	//
	// StreamBlobs must unconditionally close dest before
	// returning, and it must return context.Canceled if
	// ctx.Done() becomes readable.
	//
	// When StreamBlobs reaches the end, the return value is nil.
	StreamBlobs(ctx context.Context, dest chan<- BlobAndToken, contToken string) error
}

// Cache is the minimal interface expected of a blob cache.
type Cache interface {
	blob.Fetcher
	BlobReceiver
	BlobStatter
}

// Config is how the server's blob handlers serve a storage, as it's
// configured for one URL prefix.
type Config struct {
	Writable    bool
	Readable    bool
	Deletable   bool
	CanLongPoll bool

	// the "http://host:port" and optional path (but without trailing slash) to have "/camli/*" appended
	URLBase       string
	HandlerFinder FindHandlerByTyper
}

type BlobRemover interface {
	// RemoveBlobs removes 0 or more blobs. Removal of
	// non-existent items isn't an error. Returns failure if any
	// items existed but failed to be deleted.
	// Storage types not implementing removal return an error matching
	// errors.ErrUnsupported.
	// If RemoveBlobs returns an error, it's possible that either
	// none or only some of the blobs were deleted.
	RemoveBlobs(ctx context.Context, blobs []blob.Ref) error
}

// Storage is the interface that must be implemented by a blobserver
// storage type. (e.g. localdisk, s3, encrypt, shard, replica, remote)
type Storage interface {
	blob.Fetcher
	BlobReceiver
	BlobStatter
	BlobEnumerator
	BlobRemover
}

type FetcherEnumerator interface {
	blob.Fetcher
	BlobEnumerator
}

// StorageHandler is a storage implementation that also exports an HTTP
// status page.
type StorageHandler interface {
	Storage
	http.Handler
}

// ShutdownStorage is an optional interface for storage
// implementations which can be asked to shut down
// cleanly. Regardless, all implementations should be able to survive
// crashes without data loss.
type ShutdownStorage interface {
	Storage
	io.Closer
}

// WholeRefFetcher is an optional fast-path interface exposed by the
// 'blobpacked' blob storage implementation, which packs pieces of
// files together and can efficiently serve them contigously.
type WholeRefFetcher interface {
	// OpenWholeRef returns a ReadCloser reading from offset bytes
	// into wholeRef (the blobref of an entire file).
	//
	// The returned wholeSize is the size of the file, without
	// subtracting any offset.
	//
	// The err will be os.ErrNotExist if the wholeref is not
	// known.
	OpenWholeRef(wholeRef blob.Ref, offset int64) (rc io.ReadCloser, wholeSize int64, err error)
}

/*
Generationer is an optional interface and an optimization and paranoia
facility for clients which can be implemented by Storage
implementations.

If the client sees the same random string in multiple upload sessions,
it assumes that the blobserver still has all the same blobs, and also
it's the same server.  This mechanism is not fundamental to
Perkeep's operation: the client could also check each blob before
uploading, or enumerate all blobs from the server too.  This is purely
an optimization so clients can mix this value into their "is this file
uploaded?" local cache keys.
*/
type Generationer interface {
	// Generation returns a Storage's initialization time and
	// and unique random string (or UUID).  Implementations
	// should call ResetStorageGeneration on demand if no
	// information is known.
	// The error matches errors.ErrUnsupported if an underlying storage
	// target doesn't support the Generationer interface.
	StorageGeneration() (initTime time.Time, random string, err error)

	// ResetGeneration deletes the information returned by Generation
	// and re-generates it.
	ResetStorageGeneration() error
}

// MaxEnumerateConfig is an optional interface implemented by Storage
// interfaces to advertise their max value for how many items can
// be enumerated at once.
type MaxEnumerateConfig interface {
	Storage

	// MaxEnumerate returns the max that this storage interface is
	// capable of enumerating at once.
	MaxEnumerate() int
}
