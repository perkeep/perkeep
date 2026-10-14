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

package index_test

import (
	"context"
	"os"
	"testing"
	"time"

	"perkeep.org/pkg/index"
	"perkeep.org/pkg/index/indextest"
)

func TestIndexAVIF(t *testing.T) {
	data, err := os.ReadFile("../../internal/images/testdata/sample.avif")
	if err != nil {
		t.Fatal(err)
	}
	id := indextest.NewIndexDeps(index.NewMemoryIndex())
	id.Fataler = t
	defer id.Index.Close()
	for _, name := range []string{"sample.avif", "sample.bin"} {
		t.Run(name, func(t *testing.T) {
			ref, _ := id.UploadFile(name, string(data), time.Time{})
			fi, err := id.Index.GetFileInfo(context.Background(), ref)
			if err != nil {
				t.Fatal(err)
			}
			if fi.MIMEType != "image/avif" {
				t.Errorf("MIMEType = %q; want image/avif", fi.MIMEType)
			}
			ii, err := id.Index.GetImageInfo(context.Background(), ref)
			if err != nil {
				t.Fatal(err)
			}
			if ii.Width != 32 || ii.Height != 16 {
				t.Errorf("dimensions = %dx%d; want 32x16", ii.Width, ii.Height)
			}
		})
	}
}
