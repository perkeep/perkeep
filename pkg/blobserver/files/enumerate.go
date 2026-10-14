/*
Copyright 2011 The Perkeep Authors

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

package files

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"perkeep.org/pkg/blob"

	"golang.org/x/sync/errgroup"
)

type readBlobRequest struct {
	ch      chan<- blob.SizedRef
	after   string
	remain  *int // limit countdown
	dirRoot string

	// Not used on initial request, only on recursion
	blobPrefix, pathInto string
}

type enumerateError struct {
	msg string
	err error
}

func (ee *enumerateError) Error() string {
	return fmt.Sprintf("files enumerate error: %s: %v", ee.msg, ee.err)
}

// readBlobs implements EnumerateBlobs. It calls itself recursively on subdirectories.
func (ds *Storage) readBlobs(ctx context.Context, opts readBlobRequest) error {
	dirFullPath := filepath.Join(opts.dirRoot, opts.pathInto)
	entries, err := ds.fs.ReadDir(dirFullPath)
	if err != nil {
		return &enumerateError{"readdir of " + dirFullPath, err}
	}
	if len(entries) == 0 {
		// remove empty blob dir if we are in a queue but not the queue root itself
		if strings.Contains(dirFullPath, "queue-") &&
			!strings.Contains(filepath.Base(dirFullPath), "queue-") {
			go ds.tryRemoveDir(dirFullPath)
		}
		return nil
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	stat := make(map[string]*future) // name -> future<os.FileInfo>

	var toStat []*future
	for _, de := range entries {
		name := de.Name()
		if skipDir(name) || isShardDir(name) || de.IsDir() {
			continue
		}
		if !strings.HasSuffix(name, ".dat") && !isSymlink(de) {
			continue
		}
		fullFile := filepath.Join(dirFullPath, name)
		f := newFuture(func() (os.FileInfo, error) {
			fi, err := ds.entryInfo(fullFile, de)
			if err != nil {
				return nil, &enumerateError{"stat", err}
			}
			return fi, nil
		})
		stat[name] = f
		toStat = append(toStat, f)
	}

	// Start pre-statting things.
	go func() {
		for _, f := range toStat {
			ds.statGate.Start()
			go func() {
				defer ds.statGate.Done()
				f.ForceLoad()
			}()
		}
	}()

	for _, de := range entries {
		if *opts.remain == 0 {
			return nil
		}
		name := de.Name()
		if skipDir(name) {
			continue
		}

		isDir := isShardDir(name) || de.IsDir()
		var fi os.FileInfo
		if !isDir {
			f, ok := stat[name]
			if !ok {
				continue
			}
			fi, err = f.Get()
			if err != nil {
				return err
			}
			isDir = fi.IsDir()
		}

		if isDir {
			var newBlobPrefix string
			if opts.blobPrefix == "" {
				newBlobPrefix = name + "-"
			} else {
				newBlobPrefix = opts.blobPrefix + name
			}
			if len(opts.after) > 0 {
				compareLen := min(len(opts.after), len(newBlobPrefix))
				if newBlobPrefix[:compareLen] < opts.after[:compareLen] {
					continue
				}
			}
			ropts := opts
			ropts.blobPrefix = newBlobPrefix
			ropts.pathInto = opts.pathInto + "/" + name
			if err := ds.readBlobs(ctx, ropts); err != nil {
				return err
			}
			continue
		}

		if !strings.HasSuffix(name, ".dat") {
			continue
		}
		blobName := strings.TrimSuffix(name, ".dat")
		if blobName <= opts.after {
			continue
		}
		if blobRef, ok := blob.Parse(blobName); ok {
			select {
			case opts.ch <- blob.SizedRef{Ref: blobRef, Size: uint32(fi.Size())}:
				(*opts.remain)--
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return nil
}

func isSymlink(de fs.DirEntry) bool { return de.Type()&fs.ModeSymlink != 0 }

// entryInfo returns the FileInfo for de, the directory entry at path,
// following de if it's a symlink.
func (ds *Storage) entryInfo(path string, de fs.DirEntry) (os.FileInfo, error) {
	if isSymlink(de) {
		return ds.fs.Stat(path)
	}
	return de.Info()
}

func (ds *Storage) EnumerateBlobs(ctx context.Context, dest chan<- blob.SizedRef, after string, limit int) error {
	defer close(dest)
	if limit == 0 {
		log.Printf("Warning: files.EnumerateBlobs called with a limit of 0")
	}

	limitMutable := limit
	return ds.readBlobs(ctx, readBlobRequest{
		ch:      dest,
		dirRoot: ds.root,
		after:   after,
		remain:  &limitMutable,
	})
}

// listDirConcurrency is how many directories ListBlobRefs reads at once.
// Reading a directory is mostly waiting on the disk (or the network, for
// network block storage), so this is more than the number of CPUs.
const listDirConcurrency = 32

// ListBlobRefs implements blobserver.RefLister. Unlike EnumerateBlobs,
// it reads many directories at once and doesn't stat blob files.
func (ds *Storage) ListBlobRefs(ctx context.Context, fn func(blob.Ref) error) error {
	grp, ctx := errgroup.WithContext(ctx)
	grp.SetLimit(listDirConcurrency)
	var (
		fnMu  sync.Mutex // serializes calls to fn
		fnErr error      // first error from fn; guarded by fnMu
	)
	var listDir func(dir string) error
	listDir = func(dir string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := ds.fs.ReadDir(dir)
		if err != nil {
			return &enumerateError{"readdir of " + dir, err}
		}
		var refs []blob.Ref
		for _, de := range entries {
			name := de.Name()
			if skipDir(name) {
				continue
			}
			fullPath := filepath.Join(dir, name)
			isDir := isShardDir(name) || de.IsDir()
			if !isDir && isSymlink(de) {
				fi, err := ds.fs.Stat(fullPath)
				if err != nil {
					return &enumerateError{"stat", err}
				}
				isDir = fi.IsDir()
			}
			if isDir {
				// Read subdirectories in other goroutines while
				// under the limit, and in this one otherwise, so
				// that a full group never waits on itself.
				if !grp.TryGo(func() error { return listDir(fullPath) }) {
					if err := listDir(fullPath); err != nil {
						return err
					}
				}
				continue
			}
			blobName, ok := strings.CutSuffix(name, ".dat")
			if !ok {
				continue
			}
			if br, ok := blob.Parse(blobName); ok {
				refs = append(refs, br)
			}
		}
		if len(refs) == 0 {
			return nil
		}
		fnMu.Lock()
		defer fnMu.Unlock()
		for _, br := range refs {
			if fnErr != nil {
				return fnErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			fnErr = fn(br)
		}
		return fnErr
	}
	grp.Go(func() error { return listDir(ds.root) })
	return grp.Wait()
}

func skipDir(name string) bool {
	// The partition directory is old. (removed from codebase, but
	// likely still on disk for some people). The cache, importer-cache,
	// and packed directories are default locations from serverinit/genconfig;
	// importer scratch files are not part of the main blob storage.
	return name == "partition" || name == "cache" || name == "importer-cache" || name == "packed"
}

func isShardDir(name string) bool {
	return len(name) == 2 && isHex(name[0]) && isHex(name[1])
}

func isHex(b byte) bool {
	return ('0' <= b && b <= '9') || ('a' <= b && b <= 'f')
}

// future is an os.FileInfo future.
type future struct {
	once sync.Once
	f    func() (os.FileInfo, error)
	v    os.FileInfo
	err  error
}

func newFuture(f func() (os.FileInfo, error)) *future {
	return &future{f: f}
}

func (f *future) Get() (os.FileInfo, error) {
	f.once.Do(f.run)
	return f.v, f.err
}

func (f *future) ForceLoad() {
	f.Get()
}

func (f *future) run() { f.v, f.err = f.f() }
