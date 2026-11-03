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

// Package local implements a [filesource.Source] of a directory on local
// disk.
package local // import "perkeep.org/pkg/filesource/local"

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"perkeep.org/pkg/filesource"
)

// Source is a directory tree on local disk. A File's ID is its
// slash-separated path relative to the directory, which it can't leave,
// even by symbolic links.
//
// A Source holds nothing open between calls, so its directory may be
// removed, even on Windows, where open directories can't be.
type Source struct {
	dir string
}

var _ filesource.Source = (*Source)(nil)

// New returns the Source of the directory dir.
func New(dir string) (*Source, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	root.Close()
	return &Source{dir: dir}, nil
}

// Root returns the directory of the Source.
func (s *Source) Root(ctx context.Context) (filesource.File, error) {
	return s.Stat(ctx, ".")
}

// List lists dir, leaving out files other than regular files and
// directories, and symbolic links to them outside the Source.
func (s *Source) List(ctx context.Context, dir filesource.File) ([]filesource.File, error) {
	if !dir.Dir {
		return nil, fmt.Errorf("%s is not a directory", dir.ID)
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	des, err := fs.ReadDir(root.FS(), dir.ID)
	if err != nil {
		return nil, err
	}
	var files []filesource.File
	for _, de := range des {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := s.stat(root, path.Join(dir.ID, de.Name()))
		if err != nil {
			continue // such as a symbolic link leaving the Source
		}
		files = append(files, f)
	}
	slices.SortFunc(files, func(a, b filesource.File) int { return strings.Compare(a.Name, b.Name) })
	return files, nil
}

// Stat returns the file or directory at id, a slash-separated path
// relative to the Source's directory, following symbolic links.
func (s *Source) Stat(ctx context.Context, id string) (filesource.File, error) {
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return filesource.File{}, err
	}
	defer root.Close()
	return s.stat(root, id)
}

// stat is Stat, with root open.
func (s *Source) stat(root *os.Root, id string) (filesource.File, error) {
	id = path.Clean(id)
	fi, err := fs.Stat(root.FS(), id)
	if err != nil {
		return filesource.File{}, err
	}
	if !fi.Mode().IsRegular() && !fi.IsDir() {
		return filesource.File{}, fmt.Errorf("%s is neither a regular file nor a directory", id)
	}
	name := fi.Name()
	if id == "." {
		name = filepath.Base(s.dir)
	}
	f := filesource.File{ID: id, Name: name, Dir: fi.IsDir(), ModTime: fi.ModTime()}
	if !f.Dir {
		f.Size = fi.Size()
		f.Version = version(id, fi)
	}
	return f, nil
}

// version is the Version of the file at id with info fi: its path, size,
// and modification time, as the contents of a local file can only be
// told apart by reading them.
func version(id string, fi fs.FileInfo) string {
	return fmt.Sprintf("%s@%d.%d", id, fi.Size(), fi.ModTime().UnixNano())
}

// OpenRange opens n bytes of f's contents at off. If f's size or
// modification time has changed, it returns an error matching
// [filesource.ErrChanged].
func (s *Source) OpenRange(ctx context.Context, f filesource.File, off, n int64) (io.ReadCloser, error) {
	if err := filesource.CheckRange(f, off, n); err != nil {
		return nil, err
	}
	osf, err := os.OpenInRoot(s.dir, filepath.FromSlash(f.ID))
	if err != nil {
		return nil, err
	}
	fi, err := osf.Stat()
	if err != nil {
		osf.Close()
		return nil, err
	}
	if version(f.ID, fi) != f.Version {
		osf.Close()
		return nil, fmt.Errorf("%s: %w", f.ID, filesource.ErrChanged)
	}
	return struct {
		io.Reader
		io.Closer
	}{io.NewSectionReader(osf, off, n), osf}, nil
}
