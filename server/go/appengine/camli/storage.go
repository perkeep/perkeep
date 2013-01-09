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
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"appengine"
	"appengine/blobstore"
	"appengine/datastore"

	"camlistore.org/pkg/blobref"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
)

var _ = log.Printf

const (
	blobKind = "Blob"
	memKind  = "NsBlobMember" // blob membership in a namespace
)

var _ blobserver.Storage = (*appengineStorage)(nil)

type appengineStorage struct {
	*blobserver.SimpleBlobHubPartitionMap
	namespace string // never empty; config initializes to at least "-"
	ctx       appengine.Context
}

// blobEnt is stored once per unique blob, keyed by blobref.
type blobEnt struct {
	Size       []byte // an int64 as "%d" to make it unindexed
	BlobKey    []byte // an appengine.BlobKey
	Namespaces []byte // |-separated string of namespaces

	// TODO(bradfitz): IsCamliSchemaBlob bool? ... probably want
	// on enumeration (memEnt) too.
}

// memEnt is stored once per blob in a namespace, keyed by "ns|blobref"
type memEnt struct {
	Size []byte // an int64 as "%d" to make it unindexed
}

func (b *blobEnt) size() (int64, error) {
	return byteDecSize(b.Size)
}

func (m *memEnt) size() (int64, error) {
	return byteDecSize(m.Size)
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
	for _, in := range strings.Split(string(b.Namespaces), "|") {
		if ns == in {
			return true
		}
	}
	return false
}

func entKey(c appengine.Context, br *blobref.BlobRef) *datastore.Key {
	return datastore.NewKey(c, blobKind, br.String(), 0, nil)
}

func (s *appengineStorage) memKey(c appengine.Context, br *blobref.BlobRef) *datastore.Key {
	return datastore.NewKey(c, memKind, fmt.Sprintf("%s|%s", s.namespace, br.String()), 0, nil)
}

func fetchEnt(c appengine.Context, br *blobref.BlobRef) (*blobEnt, error) {
	row := new(blobEnt)
	err := datastore.Get(c, entKey(c, br), row)
	if err != nil {
		return nil, err
	}
	return row, nil
}

var errNoContext = errors.New("Internal error: no App Engine context is available")

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	sto := &appengineStorage{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
	}
	sto.namespace = config.OptionalString("namespace", "")
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto.namespace, err = sanitizeNamespace(sto.namespace)
	if err != nil {
		return nil, err
	}
	return sto, nil
}

var _ blobserver.ContextWrapper = (*appengineStorage)(nil)

func (sto *appengineStorage) WrapContext(req *http.Request) blobserver.Storage {
	s2 := new(appengineStorage)
	*s2 = *sto
	s2.ctx = appengine.NewContext(req)
	return s2
}

func (sto *appengineStorage) FetchStreaming(br *blobref.BlobRef) (file io.ReadCloser, size int64, err error) {
	if sto.ctx == nil {
		err = errNoContext
		return
	}
	row, err := fetchEnt(sto.ctx, br)
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
	size, err = row.size()
	if err != nil {
		return
	}
	reader := blobstore.NewReader(sto.ctx, appengine.BlobKey(string(row.BlobKey)))
	return ioutil.NopCloser(reader), size, nil
}

var crossGroupTransaction = &datastore.TransactionOptions{XG: true}

func (sto *appengineStorage) ReceiveBlob(br *blobref.BlobRef, in io.Reader) (sb blobref.SizedBlobRef, err error) {
	if sto.ctx == nil {
		err = errNoContext
		return
	}

	var b bytes.Buffer
	hash := br.Hash()
	written, err := io.Copy(io.MultiWriter(hash, &b), in)
	if err != nil {
		return
	}
	if !br.HashMatches(hash) {
		err = blobserver.ErrCorruptBlob
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
		row, err := fetchEnt(sto.ctx, br)
		switch err {
		case datastore.ErrNoSuchEntity:
			if err := uploadBlob(sto.ctx); err != nil {
				tc.Errorf("uploadBlob failed: %v", err)
				return err
			}
			row = &blobEnt{
				Size:       []byte(fmt.Sprintf("%d", written)),
				BlobKey:    []byte(string(bkey)),
				Namespaces: []byte(sto.namespace),
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
			row.Namespaces = []byte(string(row.Namespaces) + "|" + sto.namespace)
			_, err = datastore.Put(tc, entKey(tc, br), row)
			if err != nil {
				return err
			}
		default:
			return err
		}

		// Add membership row
		_, err = datastore.Put(tc, sto.memKey(tc, br), &memEnt{
			Size: []byte(fmt.Sprintf("%d", written)),
		})
		return err
	}
	err = datastore.RunInTransaction(sto.ctx, tryFunc, crossGroupTransaction)
	if err != nil {
		if len(bkey) > 0 {
			// If we just created this blob but we
			// ultimately failed, try our best to delete
			// it so it's not orphaned.
			blobstore.Delete(sto.ctx, bkey)
		}
		return
	}
	return blobref.SizedBlobRef{br, written}, nil
}

// NOTE(bslatkin): No fucking clue if this works.
func (sto *appengineStorage) RemoveBlobs(blobs []*blobref.BlobRef) error {
	if sto.ctx == nil {
		return errNoContext
	}

	tryFunc := func(tc appengine.Context, br *blobref.BlobRef) error {
		// TODO(bslatkin): Make the DB gets in this a multi-get.
		// Remove the namespace from the blobEnt
		row, err := fetchEnt(sto.ctx, br)
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
			if v := strings.Join(newNS, "|"); v != string(row.Namespaces) {
				row.Namespaces = []byte(v)
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
			sto.ctx,
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

func (sto *appengineStorage) StatBlobs(dest chan<- blobref.SizedBlobRef, blobs []*blobref.BlobRef, wait time.Duration) error {
	if sto.ctx == nil {
		return errNoContext
	}
	var (
		keys = make([]*datastore.Key, 0, len(blobs))
		out  = make([]interface{}, 0, len(blobs))
		errs = make([]error, len(blobs))
	)
	for _, br := range blobs {
		keys = append(keys, sto.memKey(sto.ctx, br))
		out = append(out, new(memEnt))
	}
	err := datastore.GetMulti(sto.ctx, keys, out)
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
		size, err := ent.size()
		if err == nil {
			dest <- blobref.SizedBlobRef{br, size}
		} else {
			sto.ctx.Warningf("skipping corrupt blob %s: %v", br, err)
		}
	}
	return err
}

func (sto *appengineStorage) EnumerateBlobs(dest chan<- blobref.SizedBlobRef, after string, limit int, wait time.Duration) error {
	defer close(dest)
	if sto.ctx == nil {
		return errNoContext
	}
	prefix := sto.namespace + "|"
	keyBegin := datastore.NewKey(sto.ctx, memKind, prefix+after, 0, nil)
	keyEnd := datastore.NewKey(sto.ctx, memKind, sto.namespace+"~", 0, nil)

	q := datastore.NewQuery(memKind).Limit(int(limit)).Filter("__key__>", keyBegin).Filter("__key__<", keyEnd)
	it := q.Run(sto.ctx)
	var row memEnt
	for {
		key, err := it.Next(&row)
		if err == datastore.Done {
			break
		}
		if err != nil {
			return err
		}
		size, err := row.size()
		if err != nil {
			return err
		}
		dest <- blobref.SizedBlobRef{blobref.Parse(key.StringID()[len(prefix):]), size}
	}
	return nil
}

var validQueueName = regexp.MustCompile(`^[a-zA-Z0-9\-\_]+$`)

// TODO(bslatkin): This does not work on App Engine yet because there are no
// background threads to do the sync loop. The plan is to break the
// syncer code up into two parts: 1) accepts notifications of new blobs to
// sync, 2) does one unit of work enumerating recent blobs and syncing them.
// In App Engine land, 1) will result in a task to be enqueued, and 2) will
// be called from within that queue context.

func (sto *appengineStorage) CreateQueue(name string) (blobserver.Storage, error) {
	if !validQueueName.MatchString(name) {
		return nil, fmt.Errorf("invalid queue name %q", name)
	}
	if sto.namespace != "" && strings.HasPrefix(sto.namespace, "queue-") {
		return nil, fmt.Errorf("can't create queue %q on existing queue %q",
			name, sto.namespace)
	}
	q := &appengineStorage{
		SimpleBlobHubPartitionMap: &blobserver.SimpleBlobHubPartitionMap{},
		namespace:                 "queue-" + name,
	}
	return q, nil
}
