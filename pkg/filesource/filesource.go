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

// Package filesource reads trees of files from where they're kept, such
// as perkeepd's local disk or Google Drive, for importing them: listing
// directories and reading parts of files, as of large zip files, whose
// tables of contents are at their ends.
//
// Its subpackages implement Source for each kind of place.
package filesource // import "perkeep.org/pkg/filesource"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// A File is a file or directory of a [Source].
type File struct {
	// ID identifies the file to its Source, such as its path relative
	// to the Source's root, or its ID in Google Drive.
	ID string

	Name    string // its base name
	Dir     bool   // whether it's a directory
	Size    int64  // its size, if it's not a directory
	ModTime time.Time

	// Version identifies the file's contents, as of when it was listed,
	// among all of the Source's files: no other file has it, and it
	// changes when the file's contents do, so data derived from them can
	// be cached by it. It's empty for directories.
	Version string
}

// A Source is a tree of files.
type Source interface {
	// Root returns the root directory of the tree.
	Root(context.Context) (File, error)

	// List returns the files and directories in the directory dir.
	List(ctx context.Context, dir File) ([]File, error)

	// Stat returns the file or directory with the given ID.
	Stat(ctx context.Context, id string) (File, error)

	// OpenRange returns a reader of the n bytes of f's contents from
	// off. If f's contents have changed since f was listed, as by its
	// Version, OpenRange returns an error matching [ErrChanged], or a
	// reader of its contents as of then.
	OpenRange(ctx context.Context, f File, off, n int64) (io.ReadCloser, error)
}

// A Finder is a Source that can find the files or directories of a name
// in a directory without listing it all.
type Finder interface {
	Source

	// Find returns the files and directories named name in dir.
	Find(ctx context.Context, dir File, name string) ([]File, error)
}

// ErrChanged is returned by [Source.OpenRange] for a file whose contents
// have changed since it was listed.
var ErrChanged = errors.New("filesource: file changed since it was listed")

// Find returns the files and directories named name in dir of src, with
// src's Find, if it's a Finder, or else by listing dir.
func Find(ctx context.Context, src Source, dir File, name string) ([]File, error) {
	if f, ok := src.(Finder); ok {
		return f.Find(ctx, dir, name)
	}
	fs, err := src.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	var found []File
	for _, f := range fs {
		if f.Name == name {
			found = append(found, f)
		}
	}
	return found, nil
}

// ReadRange returns the n bytes of f's contents from off.
func ReadRange(ctx context.Context, src Source, f File, off, n int64) ([]byte, error) {
	rc, err := src.OpenRange(ctx, f, off, n)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	buf := make([]byte, n)
	if _, err := io.ReadFull(rc, buf); err != nil {
		return nil, fmt.Errorf("reading %d bytes at %d of %s: %w", n, off, f.Name, err)
	}
	return buf, nil
}

// CheckRange returns an error if off and n aren't a range of f's
// contents, for implementations of [Source.OpenRange].
func CheckRange(f File, off, n int64) error {
	if f.Dir || off < 0 || n < 0 || off+n > f.Size || off+n < off {
		return fmt.Errorf("filesource: invalid range of %d bytes at %d of %s, of %d bytes", n, off, f.Name, f.Size)
	}
	return nil
}
