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
	"camlistore.org/pkg/index"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
)

func indexFromConfig(ld blobserver.Loader, config jsonconfig.Obj) (storage blobserver.Storage, err error) {
	//sto := &appengineIndexStorage{}
	ns := config.OptionalString("namespace", "")
	if err := config.Validate(); err != nil {
		return nil, err
	}
	_ = ns

	var sto index.IndexStorage
	// TODO
        ix := index.New(sto)
	return ix, nil

	/*
        ix.BlobSource = sto
	// Good enough, for now:
	ix.KeyFetcher = ix.BlobSource
	sto.namespace, err = sanitizeNamespace(ns)
	if err != nil {
		return nil, err
	}
	return sto, nil
	 */
}
