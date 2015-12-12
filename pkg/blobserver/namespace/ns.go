/*
Copyright 2014 The Camlistore Authors

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

// Package namespace implements the "namespace" blobserver storage type.
//
// A namespace storage is backed by another storage target but only
// has access and visibility to a subset of the blobs which have been
// uploaded through this namespace. The list of accessible blobs are
// stored in the provided "inventory" sorted key/value target.
package namespace

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/sorted"
	"go4.org/jsonconfig"
	"golang.org/x/net/context"

	"go4.org/strutil"
)

type nsto struct {
	inventory sorted.KeyValue
	master    blobserver.Storage
}

func init() {
	blobserver.RegisterStorageConstructor("namespace", blobserver.StorageConstructor(newFromConfig))
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	sto := &nsto{}
	invConf := config.RequiredObject("inventory")
	masterName := config.RequiredString("storage")
	if err := config.Validate(); err != nil {
		return nil, err
	}
	sto.inventory, err = sorted.NewKeyValue(invConf)
	if err != nil {
		return nil, fmt.Errorf("Invalid 'inventory' configuration: %v", err)
	}
	sto.master, err = ld.GetStorage(masterName)
	if err != nil {
		return nil, fmt.Errorf("Invalid 'storage' configuration: %v", err)
	}
	return sto, nil
}

func (ns *nsto) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	done := ctx.Done()

	it := ns.inventory.Find(after, "")
	first := true
	for limit > 0 && it.Next() {
		if first {
			first = false
			if after != "" && it.Key() == after {
				continue
			}
		}
		br, ok := blob.ParseBytes(it.KeyBytes())
		size, err := strutil.ParseUintBytes(it.ValueBytes(), 10, 32)
		if !ok || err != nil {
			log.Printf("Bogus namespace key %q / value %q", it.Key(), it.Value())
			continue
		}
		select {
		case dest <- blob.SizedRef{br, uint32(size)}:
		case <-done:
			return ctx.Err()
		}
		limit--
	}
	if err := it.Close(); err != nil {
		return err
	}
	return nil
}

func (ns *nsto) Fetch(br blob.Ref) (rc io.ReadCloser, size uint32, err error) {
	invSizeStr, err := ns.inventory.Get(br.String())
	if err == sorted.ErrNotFound {
		err = os.ErrNotExist
		return
	}
	if err != nil {
		return
	}
	invSize, err := strconv.ParseUint(invSizeStr, 10, 32)
	if err != nil {
		return
	}
	rc, size, err = ns.master.Fetch(br)
	if err != nil {
		return
	}
	if size != uint32(invSize) {
		log.Printf("namespace: on blob %v, unexpected inventory size %d for master size %d", br, invSize, size)
		return nil, 0, os.ErrNotExist
	}
	return rc, size, nil
}

func (ns *nsto) ReceiveBlob(br blob.Ref, src io.Reader) (sb blob.SizedRef, err error) {
	var buf bytes.Buffer
	size, err := io.Copy(&buf, src)
	if err != nil {
		return
	}

	// Check if a duplicate blob, already uploaded previously.
	if _, ierr := ns.inventory.Get(br.String()); ierr == nil {
		return blob.SizedRef{br, uint32(size)}, nil
	}

	sb, err = ns.master.ReceiveBlob(br, &buf)
	if err != nil {
		return
	}

	err = ns.inventory.Set(br.String(), strconv.Itoa(int(size)))
	return
}

func (ns *nsto) RemoveBlobs(blobs []blob.Ref) error {
	for _, br := range blobs {
		if err := ns.inventory.Delete(br.String()); err != nil {
			return err
		}
	}
	return nil
}

func (ns *nsto) StatBlobs(dest chan<- blob.SizedRef, blobs []blob.Ref) error {
	for _, br := range blobs {
		invSizeStr, err := ns.inventory.Get(br.String())
		if err == sorted.ErrNotFound {
			continue
		}
		if err != nil {
			return err
		}
		invSize, err := strconv.ParseUint(invSizeStr, 10, 32)
		if err != nil {
			log.Printf("Bogus namespace key %q / value %q", br.String(), invSizeStr)
		}
		dest <- blob.SizedRef{br, uint32(invSize)}
	}
	return nil
}
