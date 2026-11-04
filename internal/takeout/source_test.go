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

package takeout

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"perkeep.org/pkg/filesource/local"
)

func TestDefaultDirs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ids := func() []string {
		t.Helper()
		dirs, err := DefaultDirs(ctx, src)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, d := range dirs {
			ids = append(ids, d.ID)
		}
		return ids
	}
	if got := ids(); !slices.Equal(got, []string{"."}) {
		t.Errorf("DefaultDirs of an empty directory = %q; want the root", got)
	}
	// As Google Drive and Dropbox have them.
	for _, d := range []string{"Takeout", "Apps/Google Download Your Data", "Apps/Other"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := ids(), []string{"Takeout", "Apps/Google Download Your Data"}; !slices.Equal(got, want) {
		t.Errorf("DefaultDirs = %q; want %q", got, want)
	}
}
