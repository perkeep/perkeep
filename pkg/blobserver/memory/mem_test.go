/*
Copyright 2014 The Perkeep Authors

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

package memory_test

import (
	"strings"
	"testing"

	"perkeep.org/pkg/blobserver"
	"perkeep.org/pkg/blobserver/memory"
	"perkeep.org/pkg/blobserver/storagetest"
	"perkeep.org/pkg/test"
)

// TestMemoryStorage tests against an in-memory blobserver.
func TestMemoryStorage(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) blobserver.Storage {
		return &memory.Storage{}
	})
}

func TestStreamer(t *testing.T) {
	s := new(memory.Storage)
	phrases := []string{"foo", "bar", "baz", "quux"}
	for _, str := range phrases {
		(&test.Blob{Contents: str}).MustUpload(t, s)
	}
	storagetest.TestStreamer(t, s, storagetest.WantN(len(phrases)))
}

func TestCache(t *testing.T) {
	c := memory.NewCache(1024)
	(&test.Blob{Contents: "foo"}).MustUpload(t, c)
	if got, want := c.SumBlobSize(), int64(3); got != want {
		t.Errorf("size = %d; want %d", got, want)
	}
	(&test.Blob{Contents: "bar"}).MustUpload(t, c)
	if got, want := c.SumBlobSize(), int64(6); got != want {
		t.Errorf("size = %d; want %d", got, want)
	}
	(&test.Blob{Contents: strings.Repeat("x", 1020)}).MustUpload(t, c)
	if got, want := c.SumBlobSize(), int64(1023); got != want {
		t.Errorf("size = %d; want %d", got, want)
	}
	(&test.Blob{Contents: "five!"}).MustUpload(t, c)
	if got, want := c.SumBlobSize(), int64(5); got != want {
		t.Errorf("size = %d; want %d", got, want)
	}
}
