/*
Copyright 2026 The Perkeep Authors

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
	"errors"
	"strings"
	"testing"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/blobserver"
)

func TestNoImplStorageUnsupported(t *testing.T) {
	var sto blobserver.NoImplStorage
	_, receiveErr := sto.ReceiveBlob(ctxbg, blob.RefFromString("x"), strings.NewReader("x"))
	for name, err := range map[string]error{
		"ReceiveBlob":    receiveErr,
		"StatBlobs":      sto.StatBlobs(ctxbg, nil, nil),
		"EnumerateBlobs": sto.EnumerateBlobs(ctxbg, make(chan blob.SizedRef), "", 1),
		"RemoveBlobs":    sto.RemoveBlobs(ctxbg, nil),
	} {
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s error %v doesn't match errors.ErrUnsupported", name, err)
		}
	}
}
