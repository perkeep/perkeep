// +build appengine

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

package appengine

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"appengine"
	"appengine/blobstore"
	"appengine/datastore"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"go4.org/jsonconfig"
	"golang.org/x/net/context"
)

const (
	blobKind = "Blob"
	memKind  = "NsBlobMember" // blob membership in a namespace
)

var _ blobserver.Storage = (*appengineStorage)(nil)

type appengineStorage struct {
	namespace string // never empty; config initializes to at least "-"
}

// blobEnt is stored once per unique blob, keyed by blobref.
type blobEnt struct {
	Size       int64             `datastore:"Size,noindex"`
	BlobKey    appengine.BlobKey `datastore:"BlobKey,noindex"`
	Namespaces string            `datastore:"Namespaces,noindex"` // |-separated string of namespaces

	// TODO(bradfitz): IsCamliSchemaBlob bool? ... probably want
	// on enumeration (memEnt) too.
}

// memEnt is stored once per blob in a namespace, keyed by "ns|blobref"
type memEnt struct {
	Size int64 `datastore:"Size,noindex"`
}

func byteDecSize(b []byte) (int64, error) {
	var size int64
	n, err := fmt.Fscanf(bytes.NewBuffer(b), "%d", &size)
	if n != 1 || err != nil {
		return 0, fmt.Errorf("invalid Size column in datastore: %q", string(b))
	}
	return size, nil
}

func (b *blobEnt) inNamespace(ns string) (out bool) {
	for _, in := range strings.Split(b.Namespaces, "|") {
		if ns == in {
			return true
		}
	}
	return false
}

func entKey(c appengine.Context, br blob.Ref) *datastore.Key {
	return datastore.NewKey(c, blobKind, br.String(), 0, nil)
}

func (s *appengineStorage) memKey(c appengine.Context, br blob.Ref) *datastore.Key {
	return datastore.NewKey(c, memKind, fmt.Sprintf("%s|%s", s.namespace, br.String()), 0, nil)
}

func fetchEnt(c appengine.Context, br blob.Ref) (*blobEnt, error) {
	row := new(blobEnt)
	err := datastore.Get(c, entKey(c, br), row)
	if err != nil {
		return nil, err
	}
	return row, nil
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	sto := &appengineStorage{
		namespace: config.OptionalString("namespace", ""),
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto.namespace, err = sanitizeNamespace(sto.namespace)
	if err != nil {
		return nil, err
	}
	return sto, nil
}

func (sto *appengineStorage) Fetch(br blob.Ref) (file io.ReadCloser, size uint32, err error) {
	loan := ctxPool.Get()
	ctx := loan
	defer func() {
		if loan != nil {
			loan.Return()
		}
	}()

	row, err := fetchEnt(ctx, br)
	if err == datastore.ErrNoSuchEntity {
		err = os.ErrNotExist
		return
	}
	if err != nil {
		return
	}
	if !row.inNamespace(sto.namespace) {
		err = os.ErrNotExist
		return
	}

	closeLoan := loan
	var c io.Closer = &onceCloser{fn: func() { closeLoan.Return() }}
	loan = nil // take it, so it's not defer-closed

	reader := blobstore.NewReader(ctx, appengine.BlobKey(string(row.BlobKey)))
	type readCloser struct {
		io.Reader
		io.Closer
	}
	return readCloser{reader, c}, uint32(row.Size), nil
}

type onceCloser struct {
	once sync.Once
	fn   func()
}

func (oc *onceCloser) Close() error {
	oc.once.Do(oc.fn)
	return nil
}

var crossGroupTransaction = &datastore.TransactionOptions{XG: true}

func (sto *appengineStorage) ReceiveBlob(br blob.Ref, in io.Reader) (sb blob.SizedRef, err error) {
	loan := ctxPool.Get()
	defer loan.Return()
	ctx := loan

	var b bytes.Buffer
	written, err := io.Copy(&b, in)
	if err != nil {
		return
	}

	// bkey is non-empty once we've uploaded the blob.
	var bkey appengine.BlobKey

	// uploadBlob uploads the blob, unless it's already been done.
	uploadBlob := func(ctx appengine.Context) error {
		if len(bkey) > 0 {
			return nil // already done in previous transaction attempt
		}
		bw, err := blobstore.Create(ctx, "application/octet-stream")
		if err != nil {
			return err
		}
		_, err = io.Copy(bw, &b)
		if err != nil {
			// TODO(bradfitz): try to clean up; close it, see if we can find the key, delete it.
			ctx.Errorf("blobstore Copy error: %v", err)
			return err
		}
		err = bw.Close()
		if err != nil {
			// TODO(bradfitz): try to clean up; see if we can find the key, delete it.
			ctx.Errorf("blobstore Close error: %v", err)
			return err
		}
		k, err := bw.Key()
		if err == nil {
			bkey = k
		}
		return err
	}

	tryFunc := func(tc appengine.Context) error {
		row, err := fetchEnt(tc, br)
		switch err {
		case datastore.ErrNoSuchEntity:
			if err := uploadBlob(tc); err != nil {
				tc.Errorf("uploadBlob failed: %v", err)
				return err
			}
			row = &blobEnt{
				Size:       written,
				BlobKey:    bkey,
				Namespaces: sto.namespace,
			}
			_, err = datastore.Put(tc, entKey(tc, br), row)
			if err != nil {
				return err
			}
		case nil:
			if row.inNamespace(sto.namespace) {
				// Nothing to do
				return nil
			}
			row.Namespaces = row.Namespaces + "|" + sto.namespace
			_, err = datastore.Put(tc, entKey(tc, br), row)
			if err != nil {
				return err
			}
		default:
			return err
		}

		// Add membership row
		_, err = datastore.Put(tc, sto.memKey(tc, br), &memEnt{
			Size: written,
		})
		return err
	}
	err = datastore.RunInTransaction(ctx, tryFunc, crossGroupTransaction)
	if err != nil {
		if len(bkey) > 0 {
			// If we just created this blob but we
			// ultimately failed, try our best to delete
			// it so it's not orphaned.
			blobstore.Delete(ctx, bkey)
		}
		return
	}
	return blob.SizedRef{br, uint32(written)}, nil
}

// NOTE(bslatkin): No fucking clue if this works.
func (sto *appengineStorage) RemoveBlobs(blobs []blob.Ref) error {
	loan := ctxPool.Get()
	defer loan.Return()
	ctx := loan

	tryFunc := func(tc appengine.Context, br blob.Ref) error {
		// TODO(bslatkin): Make the DB gets in this a multi-get.
		// Remove the namespace from the blobEnt
		row, err := fetchEnt(tc, br)
		switch err {
		case datastore.ErrNoSuchEntity:
			// Doesn't exist, that means there should be no memEnt, but let's be
			// paranoid and double check anyways.
		case nil:
			// blobEnt exists, remove our namespace from it if possible.
			newNS := []string{}
			for _, val := range strings.Split(string(row.Namespaces), "|") {
				if val != sto.namespace {
					newNS = append(newNS, val)
				}
			}
			if v := strings.Join(newNS, "|"); v != row.Namespaces {
				row.Namespaces = v
				_, err = datastore.Put(tc, entKey(tc, br), row)
				if err != nil {
					return err
				}
			}
		default:
			return err
		}

		// Blindly delete the memEnt.
		err = datastore.Delete(tc, sto.memKey(tc, br))
		return err
	}

	for _, br := range blobs {
		ret := datastore.RunInTransaction(
			ctx,
			func(tc appengine.Context) error {
				return tryFunc(tc, br)
			},
			crossGroupTransaction)
		if ret != nil {
			return ret
		}
	}
	return nil
}

func (sto *appengineStorage) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	loan := ctxPool.Get()
	defer loan.Return()
	ctx := loan

	var (
		keys = make([]*datastore.Key, 0, len(blobs))
		out  = make([]interface{}, 0, len(blobs))
		errs = make([]error, len(blobs))
	)
	for _, br := range blobs {
		keys = append(keys, sto.memKey(ctx, br))
		out = append(out, new(memEnt))
	}
	err := datastore.GetMulti(ctx, keys, out)
	if merr, ok := err.(appengine.MultiError); ok {
		errs = []error(merr)
		err = nil
	}
	if err != nil {
		return err
	}
	for i, br := range blobs {
		thisErr := errs[i]
		if thisErr == datastore.ErrNoSuchEntity {
			continue
		}
		if thisErr != nil {
			err = errs[i] // just return last one found?
			continue
		}
		ent := out[i].(*memEnt)
		dest <- blob.SizedRef{br, uint32(ent.Size)}
	}
	return err
}

func (sto *appengineStorage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)

	loan := ctxPool.Get()
	defer loan.Return()
	actx := loan

	prefix := sto.namespace + "|"
	keyBegin := datastore.NewKey(actx, memKind, prefix+after, 0, nil)
	keyEnd := datastore.NewKey(actx, memKind, sto.namespace+"~", 0, nil)

	q := datastore.NewQuery(memKind).Limit(int(limit)).Filter("__key__>", keyBegin).Filter("__key__<", keyEnd)
	it := q.Run(actx)
	var row memEnt
	for {
		key, err := it.Next(&row)
		if err == datastore.Done {
			break
		}
		if err != nil {
			return err
		}
		select {
		case dest <- blob.SizedRef{blob.ParseOrZero(key.StringID()[len(prefix):]), uint32(row.Size)}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// TODO(bslatkin): sync does not work on App Engine yet because there are no
// background threads to do the sync loop. The plan is to break the
// syncer code up into two parts: 1) accepts notifications of new blobs to
// sync, 2) does one unit of work enumerating recent blobs and syncing them.
// In App Engine land, 1) will result in a task to be enqueued, and 2) will
// be called from within that queue context.
