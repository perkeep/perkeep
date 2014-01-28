/*
Copyright 2013 Google Inc.

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

package blobserver_test

import (
	"bytes"
	"testing"
	"time"

	"camlistore.org/pkg/blob"
	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/test"
)

func TestReceive(t *testing.T) {
	sto := new(test.Fetcher)
	data := []byte("some blob")
	br := blob.SHA1FromBytes(data)

	hub := blobserver.GetHub(sto)
	ch := make(chan blob.Ref, 1)
	hub.RegisterListener(ch)

	sb, err := blobserver.Receive(sto, br, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if sb.Size != uint32(len(data)) {
		t.Errorf("received blob size = %d; want %d", sb.Size, len(data))
	}
	if sb.Ref != br {
		t.Errorf("received blob = %v; want %v", sb.Ref, br)
	}
	select {
	case got := <-ch:
		if got != br {
			t.Errorf("blobhub notified about %v; want %v", got, br)
		}
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting on blobhub")
	}
}

func TestReceiveCorrupt(t *testing.T) {
	sto := new(test.Fetcher)
	data := []byte("some blob")
	br := blob.SHA1FromBytes(data)
	data[0] = 'X' // corrupt it
	_, err := blobserver.Receive(sto, br, bytes.NewReader(data))
	if err != blobserver.ErrCorruptBlob {
		t.Errorf("Receive = %v; want ErrCorruptBlob", err)
	}
	if len(sto.BlobrefStrings()) > 0 {
		t.Errorf("nothing should be stored. Got %q", sto.BlobrefStrings())
	}
}
