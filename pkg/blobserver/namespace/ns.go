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
	"fmt"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/sorted"
)

type nsto struct {
	inventory sorted.KeyValue
	master    blobserver.Storage

	blobserver.NoImplStorage // TODO(bradfitz): remove this and finish implementing
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
