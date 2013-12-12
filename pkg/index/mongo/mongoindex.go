/*
Copyright 2011 The Camlistore Authors.

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

// Package mongo implements the Camlistore index storage abstraction
// on top of MongoDB.
package mongo

import (
	"os"
	"strconv"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/sorted"
	"camlistore.org/pkg/sorted/mongo"
)

func init() {
	blobserver.RegisterStorageConstructor("mongodbindexer",
		blobserver.StorageConstructor(newFromConfig))
}

func newFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (blobserver.Storage, error) {
	blobPrefix := config.RequiredString("blobSource")
	kv, err := mongo.NewKeyValue(config)
	if err != nil {
		return nil, err
	}
	// TODO(mpl): hack. remove once dbinit supports mongo.
	// https://camlistore-review.googlesource.com/1427
	if wipe, _ := strconv.ParseBool(os.Getenv("CAMLI_MONGO_WIPE")); wipe {
		wiper, ok := kv.(sorted.Wiper)
		if !ok {
			panic("mongo KeyValue not a Wiper")
		}
		err = wiper.Wipe()
		if err != nil {
			return nil, err
		}
	}
	ix := index.New(kv)
	sto, err := ld.GetStorage(blobPrefix)
	if err != nil {
		return nil, err
	}

	ix.BlobSource = sto

	// Good enough, for now:
	ix.KeyFetcher = ix.BlobSource

	return ix, err
}
