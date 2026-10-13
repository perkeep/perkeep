/*
Copyright 2026 The Perkeep Authors.

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

package defaultkv

import (
	"path/filepath"
	"testing"

	"perkeep.org/pkg/sorted"
)

func TestDefaultKVFileTypeRegistered(t *testing.T) {
	kv, err := sorted.NewKeyValue(map[string]any{
		"type": sorted.DefaultKVFileType,
		"file": filepath.Join(t.TempDir(), "test."+sorted.DefaultKVFileType),
	})
	if err != nil {
		t.Fatalf("NewKeyValue(%q): %v", sorted.DefaultKVFileType, err)
	}
	if err := kv.Close(); err != nil {
		t.Fatal(err)
	}
}
