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

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/test"
	"go4.org/jsonconfig"
)

func newReplica(t *testing.T, config jsonconfig.Obj) *replicaStorage {
	ld := test.NewLoader()
	sto, err := newFromConfig(ld, config)
	if err != nil {
		t.Fatalf("Invalid config: %v", err)
	}
	return sto.(*replicaStorage)
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

func TestReceiveGood(t *testing.T) {
	sto := newReplica(t, map[string]interface{}{
		"backends": []interface{}{"/good-1/", "/good-2/"},
	})
	tb := &test.Blob{Contents: "stuff"}
	sb := mustReceive(t, sto, tb)

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

func TestReceiveOneGoodOneFail(t *testing.T) {
	sto := newReplica(t, map[string]interface{}{
		"backends":            []interface{}{"/good-1/", "/fail-1/"},
		"minWritesForSuccess": float64(1),
	})
	tb := &test.Blob{Contents: "stuff"}
	sb := mustReceive(t, sto, tb)

	if len(sto.replicas) != 2 {
		t.Fatalf("replicas = %d; want 2", len(sto.replicas))
	}
	for i, rep := range sto.replicas {
		got, err := blobserver.StatBlob(rep, sb.Ref)
		pfx := sto.replicaPrefixes[i]
		if (i == 0) != (err == nil) {
			t.Errorf("For replica %s, unexpected error: %v", pfx, err)
		}
		if err == nil && got != sb {
			t.Errorf("Replica %s got %+v; want %+v", sto.replicaPrefixes[i], got, sb)
		}
	}
}

func TestReplica(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) (sto blobserver.Storage, cleanup func()) {
		sto = newReplica(t, map[string]interface{}{
			"backends": []interface{}{"/good-1/", "/good-2/"},
		})
		return sto, func() {}
	})
}
