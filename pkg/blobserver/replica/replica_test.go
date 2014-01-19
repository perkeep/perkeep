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

package replica

import (
	"testing"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/jsonconfig"
	"camlistore.org/pkg/test"
)

func newReplica(t *testing.T, config jsonconfig.Obj) *replicaStorage {
	ld := test.NewLoader()
	sto, err := newFromConfig(ld, config)
	if err != nil {
		t.Fatalf("Invalid config: %v", err)
	}
	return sto.(*replicaStorage)
}

func TestReceive(t *testing.T) {
	sto := newReplica(t, map[string]interface{}{
		"backends": []interface{}{"/good-1/", "/good-2/"},
	})
	tb := &test.Blob{Contents: "stuff"}
	tbRef := tb.BlobRef()
	sb, err := blobserver.Receive(sto, tbRef, tb.Reader())
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if int(sb.Size) != len(tb.Contents) {
		t.Fatalf("size = %d; want %d", sb.Size, len(tb.Contents))
	}
	if sb.Ref != tbRef {
		t.Fatal("wrong blob received")
	}
	if len(sto.replicas) != 2 {
		t.Fatalf("replicas = %d; want 2", len(sto.replicas))
	}
	for i, rep := range sto.replicas {
		got, err := blobserver.StatBlob(rep, sb.Ref)
		if err != nil {
			t.Errorf("Replica %s got stat error %v", sto.replicaPrefixes[i], err)
		} else if got != sb {
			t.Errorf("Replica %s got %+v; want %+v", sto.replicaPrefixes[i], got, sb)
		}
	}
}
