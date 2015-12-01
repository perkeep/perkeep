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

package cond

import (
	"testing"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/replica"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/test"
	"go4.org/jsonconfig"
)

func newCond(t *testing.T, ld *test.Loader, config jsonconfig.Obj) *condStorage {
	sto, err := newFromConfig(ld, config)
	if err != nil {
		t.Fatalf("Invalid config: %v", err)
	}
	return sto.(*condStorage)
}

func mustReceive(t *testing.T, dst blobserver.Storage, tb *test.Blob) blob.SizedRef {
	tbRef := tb.BlobRef()
	sb, err := blobserver.Receive(dst, tbRef, tb.Reader())
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
		sto := newCond(t, ld, map[string]interface{}{
			"write": map[string]interface{}{
				"if":   "isSchema",
				"then": "/good-schema/",
				"else": "/good-other/",
			},
			"read":   "/replica-all/",
			"remove": "/replica-all/",
		})
		return sto, func() {}
	})
}

func TestReceiveIsSchema(t *testing.T) {
	ld := test.NewLoader()
	sto := newCond(t, ld, map[string]interface{}{
		"write": map[string]interface{}{
			"if":   "isSchema",
			"then": "/good-schema/",
			"else": "/good-other/",
		},
		"read": "/good-other/",
	})
	otherBlob := &test.Blob{Contents: "stuff"}
	schemaBlob := &test.Blob{Contents: `{"camliVersion": 1, "camliType": "foo"}`}

	ssb := mustReceive(t, sto, schemaBlob)
	osb := mustReceive(t, sto, otherBlob)

	ssto, _ := ld.GetStorage("/good-schema/")
	osto, _ := ld.GetStorage("/good-other/")

	if _, err := blobserver.StatBlob(ssto, ssb.Ref); err != nil {
		t.Errorf("schema blob didn't end up on schema storage")
	}
	if _, err := blobserver.StatBlob(osto, osb.Ref); err != nil {
		t.Errorf("other blob didn't end up on other storage")
	}
}
