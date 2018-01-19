/*
Copyright 2017 The Perkeep Authors

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

package union

import (
	"context"
	"testing"

	"go4.org/jsonconfig"
	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
	_ "perkeep.org/pkg/blobserver/cond"
	"perkeep.org/pkg/blobserver/replica"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/test"
)

func newUnion(t *testing.T, ld *test.Loader, config jsonconfig.Obj) *unionStorage {
	sto, err := newFromConfig(ld, config)
	if err != nil {
		t.Fatalf("Invalid config: %v", err)
	}
	return sto.(*unionStorage)
}

func mustReceive(t *testing.T, dst blobserver.Storage, tb *test.Blob) blob.SizedRef {
	tbRef := tb.BlobRef()
	sb, err := blobserver.Receive(context.Background(), dst, tbRef, tb.Reader())
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if int(sb.Size) != len(tb.Contents) {
		t.Fatalf("size = %d; want %d", sb.Size, len(tb.Contents))
	}
	if sb.Ref != tbRef {
		t.Fatal("wrong blob received")
	}
	return sb
}

func TestStorageTest(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) (_ blobserver.Storage, cleanup func()) {
		ld := test.NewLoader()
		s1, _ := ld.GetStorage("/good-schema/")
		s2, _ := ld.GetStorage("/good-other/")
		ld.SetStorage("/replica-all/", replica.NewForTest([]blobserver.Storage{s1, s2}))
		uni := newUnion(t, ld, map[string]interface{}{
			"subsets": []interface{}{"/good-schema/", "/good-other/"},
		})
		ld.SetStorage("/union/", uni)
		cnd := newCond(t, ld, map[string]interface{}{
			"write":  "/good-schema/",
			"read":   "/union/",
			"remove": "/replica-all/",
		})
		return cnd, func() {}
	})
}
func newCond(t *testing.T, ld *test.Loader, config jsonconfig.Obj) blobserver.Storage {
	sto, err := blobserver.CreateStorage("cond", ld, config)
	if err != nil {
		t.Fatalf("Invalid config: %v", err)
	}
	return sto
}
