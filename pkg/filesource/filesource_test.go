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

package filesource

import (
	"context"
	"errors"
	"io"
	"testing"
)

// listSource is a Source of one directory, which isn't a Finder.
type listSource []File

func (s listSource) Root(context.Context) (File, error) { return File{ID: "root", Dir: true}, nil }

func (s listSource) List(context.Context, File) ([]File, error) { return s, nil }

func (s listSource) Stat(context.Context, string) (File, error) {
	return File{}, errors.New("unused")
}

func (s listSource) OpenRange(context.Context, File, int64, int64) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}

func TestFindByListing(t *testing.T) {
	s := listSource{{ID: "1", Name: "Takeout", Dir: true}, {ID: "2", Name: "other"}, {ID: "3", Name: "Takeout"}}
	got, err := Find(context.Background(), s, File{ID: "root", Dir: true}, "Takeout")
	if err != nil || len(got) != 2 || got[0].ID != "1" || got[1].ID != "3" {
		t.Errorf("Find = %+v, %v; want files 1 and 3", got, err)
	}
}

func TestCheckRange(t *testing.T) {
	f := File{Name: "f", Size: 10}
	for _, tt := range []struct {
		off, n int64
		ok     bool
	}{
		{0, 10, true},
		{5, 5, true},
		{10, 0, true},
		{5, 6, false},
		{-1, 2, false},
		{2, -1, false},
		{1, 1<<63 - 1, false}, // overflows
	} {
		if err := CheckRange(f, tt.off, tt.n); (err == nil) != tt.ok {
			t.Errorf("CheckRange(%d, %d) = %v; want ok %v", tt.off, tt.n, err, tt.ok)
		}
	}
	if err := CheckRange(File{Name: "d", Dir: true}, 0, 0); err == nil {
		t.Error("CheckRange of a directory succeeded")
	}
}
