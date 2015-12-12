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
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"go4.org/jsonconfig"
	"golang.org/x/net/context"
)

var (
	_ blobserver.Generationer    = (*replicaStorage)(nil)
	_ blobserver.WholeRefFetcher = (*replicaStorage)(nil)
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
}

// NewForTest returns a replicated storage that writes, reads, and
// deletes from all the provided storages.
func NewForTest(sto []blobserver.Storage) blobserver.Storage {
	sto = append([]blobserver.Storage(nil), sto...) // clone
	names := make([]string, len(sto))
	for i := range names {
		names[i] = "/unknown-prefix/"
	}
	return &replicaStorage{
		replicaPrefixes:     names,
		replicas:            sto,
		readPrefixes:        names,
		readReplicas:        sto,
		minWritesForSuccess: len(sto),
	}
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

func (sto *replicaStorage) Fetch(b blob.Ref) (file io.ReadCloser, size uint32, err error) {
	// TODO: race these? first to respond?
	for _, replica := range sto.readReplicas {
		file, size, err = replica.Fetch(b)
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
	idx int
	sb  blob.SizedRef
	err error
}

func (sto *replicaStorage) ReceiveBlob(br blob.Ref, src io.Reader) (_ blob.SizedRef, err error) {
	// Slurp the whole blob before replicating. Bounded by 16 MB anyway.
	var buf bytes.Buffer
	size, err := io.Copy(&buf, src)
	if err != nil {
		return
	}

	nReplicas := len(sto.replicas)
	resc := make(chan sizedBlobAndError, nReplicas)
	uploadToReplica := func(idx int, dst blobserver.BlobReceiver) {
		// Using ReceiveNoHash because it's already been
		// verified implicitly by the io.Copy above:
		sb, err := blobserver.ReceiveNoHash(dst, br, bytes.NewReader(buf.Bytes()))
		resc <- sizedBlobAndError{idx, sb, err}
	}
	for idx, replica := range sto.replicas {
		go uploadToReplica(idx, replica)
	}

	nSuccess := 0
	var fails []sizedBlobAndError
	for _ = range sto.replicas {
		res := <-resc
		switch {
		case res.err == nil && int64(res.sb.Size) == size:
			nSuccess++
			if nSuccess == sto.minWritesForSuccess {
				return res.sb, nil
			}
		case res.err == nil:
			err = fmt.Errorf("replica: upload shard reported size %d, expected %d", res.sb.Size, size)
			res.err = err
			fails = append(fails, res)
		default:
			err = res.err
			fails = append(fails, res)
		}
	}
	for _, res := range fails {
		log.Printf("replica: receiving blob %v, %d successes, %d failures; backend %s reported: %v",
			br,
			nSuccess, len(fails),
			sto.replicaPrefixes[res.idx], res.err)
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
	for _ = range sto.replicas {
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

func (sto *replicaStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	return blobserver.MergedEnumerateStorage(ctx, dest, sto.readReplicas, after, limit)
}

func (sto *replicaStorage) ResetStorageGeneration() error {
	var ret error
	n := 0
	for _, replica := range sto.replicas {
		if g, ok := replica.(blobserver.Generationer); ok {
			n++
			if err := g.ResetStorageGeneration(); err != nil && ret == nil {
				ret = err
			}
		}
	}
	if n == 0 {
		return errors.New("ResetStorageGeneration not supported")
	}
	return ret
}

func (sto *replicaStorage) StorageGeneration() (initTime time.Time, random string, err error) {
	var buf bytes.Buffer
	n := 0
	for _, replica := range sto.replicas {
		if g, ok := replica.(blobserver.Generationer); ok {
			n++
			rt, rrand, rerr := g.StorageGeneration()
			if rerr != nil {
				err = rerr
			} else {
				if rt.After(initTime) {
					// Returning the max of all initialization times.
					// TODO: not sure whether max or min makes more sense.
					initTime = rt
				}
				if buf.Len() != 0 {
					buf.WriteByte('/')
				}
				buf.WriteString(rrand)
			}
		}
	}
	if n == 0 {
		err = errors.New("No replicas support StorageGeneration")
	}
	return initTime, buf.String(), err
}

func (sto *replicaStorage) OpenWholeRef(wholeRef blob.Ref, offset int64) (rc io.ReadCloser, wholeSize int64, err error) {
	// TODO: race these? first to respond?
	for _, replica := range sto.readReplicas {
		if v, ok := replica.(blobserver.WholeRefFetcher); ok {
			rc, wholeSize, err = v.OpenWholeRef(wholeRef, offset)
			if err == nil {
				return
			}
		}
	}
	if err == nil {
		err = os.ErrNotExist
	}
	return
}

func init() {
	blobserver.RegisterStorageConstructor("replica", blobserver.StorageConstructor(newFromConfig))
}
