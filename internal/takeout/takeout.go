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

// Package takeout reads Google Takeout exports, the sets of zip files
// that Google Takeout writes, from local disk or Google Drive, and finds
// which of their files a store such as a Perkeep server already has.
//
// A zip file's table of contents gives each file's size and CRC-32, but
// no cryptographic digest, so files are matched by their size and CRC-32,
// which the Perkeep index keeps. Only the files a store doesn't have need
// to be read, each with one read of its part of its zip file.
package takeout // import "perkeep.org/internal/takeout"

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go4.org/ziputil"
)

// An Export is one Google Takeout export: the zip files whose names start
// with the same prefix, such as "takeout-20240101T120000Z".
type Export struct {
	Name string    // the zip files' common prefix
	Time time.Time // when the export was made, from Name
	Zips []*Zip    // sorted by name
}

// A Zip is one zip file of an Export.
type Zip struct {
	Name string
	Size int64

	src zipSource
	toc *ziputil.Reader // or nil until LoadTOC
}

// zipSource reads a zip file from where it's stored.
type zipSource interface {
	// readTOC returns the end of the zip file holding its table of
	// contents, from its central directory to its end.
	readTOC(ctx context.Context) ([]byte, error)
	// openRange returns a reader of n bytes of the zip file from off.
	openRange(ctx context.Context, off, n int64) (io.ReadCloser, error)
}

// LoadTOC reads z's table of contents, if it hasn't yet.
func (z *Zip) LoadTOC(ctx context.Context) error {
	if z.toc != nil {
		return nil
	}
	toc, err := z.src.readTOC(ctx)
	if err != nil {
		return fmt.Errorf("reading the table of contents of %s: %w", z.Name, err)
	}
	r, err := ziputil.ParseTOC(z.Size, toc)
	if err != nil {
		return fmt.Errorf("parsing the table of contents of %s: %w", z.Name, err)
	}
	z.toc = r
	return nil
}

// readTOC returns the table of contents of a zip file of size bytes, read
// with readRange, which returns n bytes from off.
func readTOC(ctx context.Context, size int64, readRange func(ctx context.Context, off, n int64) ([]byte, error)) ([]byte, error) {
	// The end of central directory record is within the last 65 KiB,
	// even with the longest comment.
	tailLen := min(size, 65<<10)
	tail, err := readRange(ctx, size-tailLen, tailLen)
	if err != nil {
		return nil, err
	}
	tocSize, ok := ziputil.ZipTOCSize(size, tail)
	if !ok {
		return nil, errors.New("no zip end of central directory record found")
	}
	if tocSize <= int64(len(tail)) {
		return tail[len(tail)-int(tocSize):], nil
	}
	return readRange(ctx, size-tocSize, tocSize)
}

// LoadTOCs loads the tables of contents of e's zip files, reading up to
// workers of them at once.
func (e *Export) LoadTOCs(ctx context.Context, workers int) error {
	return forEach(ctx, e.Zips, workers, func(z *Zip) error { return z.LoadTOC(ctx) })
}

// Entries returns the files in e's zip files, sorted by name. Their
// tables of contents must be loaded.
func (e *Export) Entries() []Entry {
	var ents []Entry
	for _, z := range e.Zips {
		if z.toc == nil {
			panic("takeout: Entries before LoadTOCs")
		}
		for _, f := range z.toc.File {
			if !strings.HasSuffix(f.Name, "/") {
				ents = append(ents, Entry{Zip: z, File: f})
			}
		}
	}
	slices.SortStableFunc(ents, func(a, b Entry) int { return strings.Compare(a.File.Name, b.File.Name) })
	return ents
}

// An Entry is a file in a zip file of an Export.
type Entry struct {
	Zip  *Zip
	File *ziputil.FileHeader
}

// Size returns the size of the entry's contents.
func (e Entry) Size() int64 { return int64(e.File.UncompressedSize64) }

// Open returns a reader of the entry's contents, which fails at the end
// if they don't match its CRC-32.
func (e Entry) Open(ctx context.Context) (io.ReadCloser, error) {
	return ziputil.OpenWithReader(e.File, func(off, n int64) (io.ReadCloser, error) {
		return e.Zip.src.openRange(ctx, off, min(n, e.Zip.Size-off))
	})
}

// maxReadAll is the largest entry that ReadAll reads.
const maxReadAll = 16 << 20

// entryCache is implemented by zip sources that cache the contents of
// entries read with ReadAll.
type entryCache interface {
	cachedEntry(f *ziputil.FileHeader) ([]byte, bool)
	cacheEntry(f *ziputil.FileHeader, data []byte)
}

// ReadAll returns the contents of a small entry, such as a JSON file. If
// its zip file is in Google Drive and the Drive has a CacheDir, they're
// cached there.
func (e Entry) ReadAll(ctx context.Context) ([]byte, error) {
	if e.Size() > maxReadAll {
		return nil, fmt.Errorf("%s is too big to read whole, at %d bytes", e.File.Name, e.Size())
	}
	c, cached := e.Zip.src.(entryCache)
	if cached {
		if data, ok := c.cachedEntry(e.File); ok {
			return data, nil
		}
	}
	rc, err := e.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", e.File.Name, err)
	}
	if cached {
		c.cacheEntry(e.File, data)
	}
	return data, nil
}

// photosPrefix is the directory of Google Photos' files in an export, in
// English.
const photosPrefix = "Takeout/Google Photos/"

// IsPhotosMedia reports whether name, the path of a file in a Takeout
// export, is a photo or video from Google Photos, rather than another
// product's data or the JSON metadata that Takeout writes beside each
// photo.
func IsPhotosMedia(name string) bool {
	rest, ok := strings.CutPrefix(name, photosPrefix)
	if !ok || rest == "" || strings.HasSuffix(rest, "/") {
		return false
	}
	switch strings.ToLower(path.Ext(rest)) {
	case ".json", ".html", ".csv":
		return false
	}
	return true
}

// IsEdited reports whether name, the path of a Google Photos file in a
// Takeout export, is an edited copy of a photo or video, which Google
// Photos exports beside the original, named like "IMG_1234-edited.jpg"
// (in English) or, if that's taken, "IMG_1234-edited(1).jpg".
func IsEdited(name string) bool {
	base := path.Base(name)
	stem := strings.TrimSuffix(base, path.Ext(base))
	if i := strings.LastIndexByte(stem, '('); i >= 0 && strings.HasSuffix(stem, ")") {
		if _, err := strconv.Atoi(stem[i+1 : len(stem)-1]); err == nil {
			stem = stem[:i]
		}
	}
	return strings.HasSuffix(stem, "-edited")
}

// A Content is the contents of one or more entries with the same size
// and CRC-32, which are taken to be the same. Google Photos exports a
// photo once in its year's directory and again in each of its albums'.
type Content struct {
	Size    int64
	CRC32   uint32
	Entries []Entry // in the order given to Contents
}

// Digest returns c's CRC-32 as a Perkeep index digest, such as
// "crc32-cbf43926".
func (c *Content) Digest() string { return fmt.Sprintf("crc32-%08x", c.CRC32) }

// Contents groups ents by their contents, returning them in the order of
// their first entries.
func Contents(ents []Entry) []*Content {
	type key struct {
		size  int64
		crc32 uint32
	}
	m := make(map[key]*Content)
	var cs []*Content
	for _, e := range ents {
		k := key{e.Size(), e.File.CRC32}
		c, ok := m[k]
		if !ok {
			c = &Content{Size: k.size, CRC32: k.crc32}
			m[k] = c
			cs = append(cs, c)
		}
		c.Entries = append(c.Entries, e)
	}
	return cs
}

// A Store is where files are uploaded to, such as a Perkeep server.
type Store interface {
	// Has reports whether the store has a file of size bytes with the
	// given CRC-32.
	Has(ctx context.Context, size int64, crc32 uint32) (bool, error)
	// Put uploads e's contents, read from r.
	Put(ctx context.Context, e Entry, r io.Reader) error
}

// Check returns which of cs the store has and which it's missing, in the
// order of cs, asking about up to workers of them at once. If progress is
// non-nil, it's called after each, from one goroutine at a time.
func Check(ctx context.Context, s Store, cs []*Content, workers int, progress func(done int)) (have, missing []*Content, err error) {
	has := make([]bool, len(cs))
	idx := make([]int, len(cs))
	for i := range idx {
		idx[i] = i
	}
	var mu sync.Mutex
	done := 0
	err = forEach(ctx, idx, workers, func(i int) error {
		ok, err := s.Has(ctx, cs[i].Size, cs[i].CRC32)
		if err != nil {
			return err
		}
		has[i] = ok
		if progress != nil {
			mu.Lock()
			defer mu.Unlock()
			done++
			progress(done)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	for i, c := range cs {
		if has[i] {
			have = append(have, c)
		} else {
			missing = append(missing, c)
		}
	}
	return have, missing, nil
}

// Upload puts each of cs in the store, read from its first entry, up to
// workers at once. It keeps going after a failure. If result is non-nil,
// it's called with each content's result, from one goroutine at a time.
// Upload returns the number of failures.
func Upload(ctx context.Context, s Store, cs []*Content, workers int, result func(c *Content, err error)) (failed int, err error) {
	var mu sync.Mutex
	err = forEach(ctx, cs, workers, func(c *Content) error {
		putErr := put(ctx, s, c.Entries[0])
		mu.Lock()
		defer mu.Unlock()
		if putErr != nil {
			failed++
		}
		if result != nil {
			result(c, putErr)
		}
		return nil
	})
	return failed, err
}

// cachedPutMax is the size of entries up to which put reads them with
// ReadAll, which may have them cached, such as sidecars that NewLibrary
// read.
const cachedPutMax = 1 << 20

func put(ctx context.Context, s Store, e Entry) error {
	if e.Size() <= cachedPutMax {
		data, err := e.ReadAll(ctx)
		if err != nil {
			return err
		}
		return s.Put(ctx, e, bytes.NewReader(data))
	}
	rc, err := e.Open(ctx)
	if err != nil {
		return err
	}
	defer rc.Close()
	return s.Put(ctx, e, rc)
}

// forEach calls fn on each of items, up to workers at once, stopping at
// the first error, which it returns.
func forEach[T any](ctx context.Context, items []T, workers int, fn func(T) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	work := make(chan T)
	var wg sync.WaitGroup
	for range max(1, min(workers, len(items))) {
		wg.Go(func() {
			for it := range work {
				if err := fn(it); err != nil {
					cancel(err)
				}
			}
		})
	}
feed:
	for _, it := range items {
		select {
		case work <- it:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	return context.Cause(ctx)
}

// groupExports groups zips into exports by their names' prefixes,
// ignoring files not named like a Takeout export's zip files, such as
// "takeout-20240101T120000Z-001.zip".
func groupExports(zips []*Zip) []*Export {
	m := make(map[string]*Export)
	for _, z := range zips {
		name, t, ok := exportName(z.Name)
		if !ok {
			continue
		}
		e, ok := m[name]
		if !ok {
			e = &Export{Name: name, Time: t}
			m[name] = e
		}
		e.Zips = append(e.Zips, z)
	}
	var exps []*Export
	for _, e := range m {
		slices.SortFunc(e.Zips, func(a, b *Zip) int { return strings.Compare(a.Name, b.Name) })
		exps = append(exps, e)
	}
	slices.SortFunc(exps, func(a, b *Export) int { return cmp.Compare(a.Name, b.Name) })
	return exps
}

// exportName returns the export that a zip file named zipName is part of,
// such as "takeout-20240101T120000Z" for
// "takeout-20240101T120000Z-001.zip".
func exportName(zipName string) (name string, t time.Time, ok bool) {
	rest, ok := strings.CutPrefix(zipName, "takeout-")
	if !ok || !strings.HasSuffix(rest, ".zip") {
		return "", time.Time{}, false
	}
	date, _, ok := strings.Cut(rest, "-")
	if !ok {
		return "", time.Time{}, false
	}
	t, err := time.Parse("20060102T150405Z", date)
	if err != nil {
		return "", time.Time{}, false
	}
	return "takeout-" + date, t, true
}

// FindExport returns the export of exps named name.
func FindExport(exps []*Export, name string) (*Export, error) {
	for _, e := range exps {
		if e.Name == name {
			return e, nil
		}
	}
	var names []string
	for _, e := range exps {
		names = append(names, e.Name)
	}
	return nil, fmt.Errorf("no Takeout export named %q; found %q", name, names)
}
