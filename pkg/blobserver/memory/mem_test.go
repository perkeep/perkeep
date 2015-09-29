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

package memory_test

import (
	"strings"
	"testing"

	"camlistore.org/pkg/blobserver"
	"camlistore.org/pkg/blobserver/memory"
	"camlistore.org/pkg/blobserver/storagetest"
	"camlistore.org/pkg/test"
)

// TestMemoryStorage tests against an in-memory blobserver.
func TestMemoryStorage(t *testing.T) {
	storagetest.Test(t, func(t *testing.T) (blobserver.Storage, func()) {
		return &memory.Storage{}, func() {}
	})
}

func TestStreamer(t *testing.T) {
	s := new(memory.Storage)
	phrases := []string{"foo", "bar", "baz", "quux"}
	for _, str := range phrases {
		(&test.Blob{str}).MustUpload(t, s)
	}
	storagetest.TestStreamer(t, s, storagetest.WantN(len(phrases)))
}

func TestCache(t *testing.T) {
	c := memory.NewCache(1024)
	(&test.Blob{"foo"}).MustUpload(t, c)
	if got, want := c.SumBlobSize(), int64(3); got != want {
		t.Errorf("size = %d; want %d", got, want)
	}
	(&test.Blob{"bar"}).MustUpload(t, c)
	if got, want := c.SumBlobSize(), int64(6); got != want {
		t.Errorf("size = %d; want %d", got, want)
	}
	(&test.Blob{strings.Repeat("x", 1020)}).MustUpload(t, c)
	if got, want := c.SumBlobSize(), int64(1023); got != want {
		t.Errorf("size = %d; want %d", got, want)
	}
	(&test.Blob{"five!"}).MustUpload(t, c)
	if got, want := c.SumBlobSize(), int64(5); got != want {
		t.Errorf("size = %d; want %d", got, want)
	}
}
