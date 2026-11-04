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
	"fmt"
	"hash/crc32"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"go4.org/ziputil"
	"perkeep.org/pkg/filesource"
)

// A Cache caches data derived from zip files' contents, such as their
// tables of contents and small files, by keys that name the contents
// they're from. It's only an optimization: Put may drop data.
type Cache interface {
	Get(key string) (data []byte, ok bool)
	Put(key string, data []byte)
}

// DirCache is a Cache of files in a directory, named by their keys,
// which are slash-separated paths.
type DirCache string

func (d DirCache) Get(key string) ([]byte, bool) {
	data, err := os.ReadFile(filepath.Join(string(d), filepath.FromSlash(key)))
	return data, err == nil
}

func (d DirCache) Put(key string, data []byte) {
	writeFileAtomic(filepath.Join(string(d), filepath.FromSlash(key)), data)
}

func writeFileAtomic(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+".tmp*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), name)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// ExportsFromFiles returns the Takeout exports of the zip files files of
// src, ignoring files not named like a Takeout export's zip files, such
// as "takeout-20240101T120000Z-001.zip". If cache is non-nil, the zip
// files' tables of contents, and the small entries that Entry.ReadAll
// reads, are cached in it, by the files' Versions.
func ExportsFromFiles(src filesource.Source, files []filesource.File, cache Cache) []*Export {
	var zips []*Zip
	for _, f := range files {
		if f.Dir || f.Size <= 0 {
			continue
		}
		zips = append(zips, &Zip{Name: f.Name, Size: f.Size, src: &sourceZip{src: src, f: f, cache: cache}})
	}
	return groupExports(zips)
}

// FindExports returns the Takeout exports whose zip files are in the
// directory dir of src. (See ExportsFromFiles.)
func FindExports(ctx context.Context, src filesource.Source, dir filesource.File, cache Cache) ([]*Export, error) {
	files, err := src.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	return ExportsFromFiles(src, files, cache), nil
}

// defaultPaths are the paths of the folders where Google Takeout saves
// exports: in Google Drive, "Takeout" in its root (of which there may be
// more than one), and in Dropbox, its app's folder.
var defaultPaths = [][]string{
	{"Takeout"},
	{"Apps", "Google Download Your Data"},
}

// DefaultDirs returns the directories of src where Google Takeout saves
// exports by default, or else, for other places, the root itself.
func DefaultDirs(ctx context.Context, src filesource.Source) ([]filesource.File, error) {
	root, err := src.Root(ctx)
	if err != nil {
		return nil, err
	}
	var dirs []filesource.File
	for _, p := range defaultPaths {
		found, err := findDirs(ctx, src, []filesource.File{root}, p)
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, found...)
	}
	if len(dirs) == 0 {
		dirs = []filesource.File{root}
	}
	return dirs, nil
}

// findDirs returns the directories at the path p beneath those of in.
func findDirs(ctx context.Context, src filesource.Source, in []filesource.File, p []string) ([]filesource.File, error) {
	for _, name := range p {
		var next []filesource.File
		for _, d := range in {
			found, err := filesource.Find(ctx, src, d, name)
			if err != nil {
				return nil, err
			}
			for _, f := range found {
				if f.Dir {
					next = append(next, f)
				}
			}
		}
		in = next
	}
	return in, nil
}

// sourceZip is a zip file of a filesource.Source.
type sourceZip struct {
	src   filesource.Source
	f     filesource.File
	cache Cache // or nil
}

// File returns the zip file as its file source has it, if it's from one.
func (z *Zip) File() (f filesource.File, ok bool) {
	sz, ok := z.src.(*sourceZip)
	if !ok {
		return filesource.File{}, false
	}
	return sz.f, true
}

// tocKey and entryKey are the keys of the cached table of contents and
// entries of a file of Version v.
func tocKey(v string) string { return url.PathEscape(v) + ".toc" }

func entryKey(v string, f *ziputil.FileHeader) string {
	// The version's contents never change, so an entry's offset in it
	// identifies the entry.
	return fmt.Sprintf("entries/%s/%d-%08x", url.PathEscape(v), f.HeaderOffset, f.CRC32)
}

func (z *sourceZip) readTOC(ctx context.Context) ([]byte, error) {
	if z.cache != nil {
		if toc, ok := z.cache.Get(tocKey(z.f.Version)); ok {
			return toc, nil
		}
	}
	toc, err := readTOC(ctx, z.f.Size, func(ctx context.Context, off, n int64) ([]byte, error) {
		return filesource.ReadRange(ctx, z.src, z.f, off, n)
	})
	if err != nil {
		return nil, err
	}
	if z.cache != nil {
		z.cache.Put(tocKey(z.f.Version), toc)
	}
	return toc, nil
}

func (z *sourceZip) openRange(ctx context.Context, off, n int64) (io.ReadCloser, error) {
	return z.src.OpenRange(ctx, z.f, off, n)
}

func (z *sourceZip) cachedEntry(f *ziputil.FileHeader) ([]byte, bool) {
	if z.cache == nil {
		return nil, false
	}
	data, ok := z.cache.Get(entryKey(z.f.Version, f))
	if !ok || uint64(len(data)) != f.UncompressedSize64 || crc32.ChecksumIEEE(data) != f.CRC32 {
		return nil, false
	}
	return data, true
}

func (z *sourceZip) cacheEntry(f *ziputil.FileHeader, data []byte) {
	if z.cache != nil {
		z.cache.Put(entryKey(z.f.Version, f), data)
	}
}
