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

/*
Package replica registers the "replica" blobserver storage type,
providing synchronous replication to one more backends.

Writes wait for minWritesForSuccess (default: all). Reads are
attempted in order and not load-balanced, randomized, or raced by
default.

Example config:

      "/repl/": {
          "handler": "storage-replica",
          "handlerArgs": {
              "backends": ["/b1/", "/b2/", "/b3/"],
              "minWritesForSuccess": 2
          }
      },
*/
package replica

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/context"
	"camlistore.org/pkg/jsonconfig"
)

const buffered = 8

type replicaStorage struct {
	// Replicas for writing:
	replicaPrefixes []string
	replicas        []blobserver.Storage

	// Replicas for reading:
	readPrefixes []string
	readReplicas []blobserver.Storage

	// Minimum number of writes that must succeed before
	// acknowledging success to the client.
	minWritesForSuccess int

	ctx *http.Request // optional per-request context
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	sto := &replicaStorage{
		replicaPrefixes: config.RequiredList("backends"),
		readPrefixes:    config.OptionalList("readBackends"),
	}
	nReplicas := len(sto.replicaPrefixes)
	sto.minWritesForSuccess = config.OptionalInt("minWritesForSuccess", nReplicas)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if nReplicas == 0 {
		return nil, errors.New("replica: need at least one replica")
	}
	if sto.minWritesForSuccess == 0 {
		sto.minWritesForSuccess = nReplicas
	}
	// readPrefixes defaults to the write prefixes.
	if len(sto.readPrefixes) == 0 {
		sto.readPrefixes = sto.replicaPrefixes
	}

	for _, prefix := range sto.replicaPrefixes {
		s, err := ld.GetStorage(prefix)
		if err != nil {
			// If it's not a storage interface, it might be an http Handler
			// that also supports being a target (e.g. a sync handler).
			h, _ := ld.GetHandler(prefix)
			var ok bool
			if s, ok = h.(blobserver.Storage); !ok {
				return nil, err
			}
		}
		sto.replicas = append(sto.replicas, s)
	}
	for _, prefix := range sto.readPrefixes {
		s, err := ld.GetStorage(prefix)
		if err != nil {
			return nil, err
		}
		sto.readReplicas = append(sto.readReplicas, s)
	}
	return sto, nil
}

func (sto *replicaStorage) FetchStreaming(b blob.Ref) (file io.ReadCloser, size int64, err error) {
	// TODO: race these? first to respond?
	for _, replica := range sto.readReplicas {
		file, size, err = replica.FetchStreaming(b)
		if err == nil {
			return
		}
	}
	return
}

// StatBlobs stats all read replicas.
func (sto *replicaStorage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	need := make(map[blob.Ref]bool)
	for _, br := range blobs {
		need[br] = true
	}

	ch := make(chan blob.SizedRef, buffered)
	donec := make(chan bool)

	go func() {
		for sb := range ch {
			if need[sb.Ref] {
				dest <- sb
				delete(need, sb.Ref)
			}
		}
		donec <- true
	}()

	errc := make(chan error, buffered)
	statReplica := func(s blobserver.Storage) {
		errc <- s.StatBlobs(ch, blobs)
	}

	for _, replica := range sto.readReplicas {
		go statReplica(replica)
	}

	var retErr error
	for _ = range sto.readReplicas {
		if err := <-errc; err != nil {
			retErr = err
		}
	}
	close(ch)
	<-donec

	// Safe to access need map now; as helper goroutine is
	// done with it.
	if len(need) == 0 {
		return nil
	}
	return retErr
}

type sizedBlobAndError struct {
	sb  blob.SizedRef
	err error
}

func (sto *replicaStorage) ReceiveBlob(b blob.Ref, source io.Reader) (_ blob.SizedRef, err error) {
	nReplicas := len(sto.replicas)
	rpipe, wpipe, writer := make([]*io.PipeReader, nReplicas), make([]*io.PipeWriter, nReplicas), make([]io.Writer, nReplicas)
	for idx := range sto.replicas {
		rpipe[idx], wpipe[idx] = io.Pipe()
		writer[idx] = wpipe[idx]
		// TODO: deal with slow/hung clients. this scheme of pipes +
		// multiwriter (even with a bufio.Writer thrown in) isn't
		// sufficient to guarantee forward progress. perhaps something
		// like &MoveOrDieWriter{Writer: wpipe[idx], HeartbeatSec: 10}
	}
	upResult := make(chan sizedBlobAndError, nReplicas)
	uploadToReplica := func(source io.Reader, dst blobserver.BlobReceiver) {
		sb, err := blobserver.Receive(dst, b, source)
		if err != nil {
			io.Copy(ioutil.Discard, source)
		}
		upResult <- sizedBlobAndError{sb, err}
	}
	for idx, replica := range sto.replicas {
		go uploadToReplica(rpipe[idx], replica)
	}
	size, err := io.Copy(io.MultiWriter(writer...), source)
	if err != nil {
		for i := range wpipe {
			wpipe[i].CloseWithError(err)
		}
		return
	}
	for idx := range sto.replicas {
		wpipe[idx].Close()
	}
	nSuccess, nFailures := 0, 0
	for _ = range sto.replicas {
		res := <-upResult
		switch {
		case res.err == nil && res.sb.Size == size:
			nSuccess++
			if nSuccess == sto.minWritesForSuccess {
				return res.sb, nil
			}
		case res.err == nil:
			nFailures++
			err = fmt.Errorf("replica: upload shard reported size %d, expected %d", res.sb.Size, size)
		default:
			nFailures++
			err = res.err
		}
	}
	if nFailures > 0 {
		log.Printf("replica: receiving blob, %d successes, %d failures; last error = %v",
			nSuccess, nFailures, err)
	}
	return
}

func (sto *replicaStorage) RemoveBlobs(blobs []blob.Ref) error {
	errch := make(chan error, buffered)
	removeFrom := func(s blobserver.Storage) {
		errch <- s.RemoveBlobs(blobs)
	}
	for _, replica := range sto.replicas {
		go removeFrom(replica)
	}
	var reterr error
	nSuccess := 0
	for _ = range errch {
		if err := <-errch; err != nil {
			reterr = err
		} else {
			nSuccess++
		}
	}
	if nSuccess > 0 {
		// TODO: decide on the return value. for now this is best
		// effort and we return nil if any of the blobservers said
		// success.  maybe a bit weird, though.
		return nil
	}
	return reterr
}

func (sto *replicaStorage) EnumerateBlobs(ctx *context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	return blobserver.MergedEnumerate(ctx, dest, sto.readReplicas, after, limit)
}

func init() {
	blobserver.RegisterStorageConstructor("replica", blobserver.StorageConstructor(newFromConfig))
}
