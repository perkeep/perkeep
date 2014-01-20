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

package namespace

import (
	"testing"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/test"
)

func newTestNamespace(t *testing.T) (sto blobserver.Storage, cleanup func()) {
	ld := test.NewLoader()
	sto, err := newFromConfig(ld, map[string]interface{}{
		"storage": "/good-storage/",
		"inventory": map[string]interface{}{
			"type": "memory",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sto, func() {}
}

func TestNamespace(t *testing.T) {
	storagetest.Test(t, newTestNamespace)
}
