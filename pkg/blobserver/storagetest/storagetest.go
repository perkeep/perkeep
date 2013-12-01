/*
Copyright 2013 The Camlistore Authors

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

// Package storagetest tests blobserver.Storage implementations
package storagetest

import (
	"testing"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/test"
)

func Test(t *testing.T, fn func(*testing.T) (sto blobserver.Storage, cleanup func())) {
	sto, cleanup := fn(t)
	defer cleanup()

	t.Logf("Testing receive")

	b1 := &test.Blob{"foo"}
	b1s, err := sto.ReceiveBlob(b1.BlobRef(), b1.Reader())
	if err != nil {
		t.Fatalf("ReceiveBlob of b1: %v", err)
	}
	if b1s != b1.SizedRef() {
		t.Fatal("Received %v; want %v", b1s, b1.SizedRef())
	}

	// TODO: test all the other methods
}
